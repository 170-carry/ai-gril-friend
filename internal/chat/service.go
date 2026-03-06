package chat

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"ai-gf/internal/conversation"
	"ai-gf/internal/llm"
	"ai-gf/internal/memory"
	"ai-gf/internal/prompt"
	"ai-gf/internal/repo"
	"ai-gf/internal/signals"
)

// Service 封装聊天主流程：历史加载、prompt 构建、LLM 调用与消息落库。
type Service struct {
	repo             repo.ChatMessageRepository
	provider         llm.Provider
	memoryEngine     memory.Engine
	turnProcessor    memory.TurnProcessor
	conversationCE   *conversation.Engine
	promptBuilder    prompt.MessageBuilder
	promptBudget     prompt.BudgetConfig
	temperature      float64
	maxHistoryRounds int
}

// StreamChatInput 是流式聊天接口输入参数。
type StreamChatInput struct {
	UserID      string
	SessionID   string
	Message     string
	Persona     prompt.PersonaConfig
	Temperature *float64
}

// StreamChatResult 是流式聊天完成后的结果信息。
type StreamChatResult struct {
	AssistantMessageID int64
	AssistantContent   string
}

// DebugPromptInput 是 prompt debug 模式输入参数。
type DebugPromptInput struct {
	UserID    string
	SessionID string
	Message   string
	Persona   prompt.PersonaConfig
	ModelID   string
	Options   prompt.BuildOptions
}

// DebugPromptResult 是 prompt debug 模式输出。
type DebugPromptResult struct {
	Messages          []llm.Message
	Trace             prompt.BuildTrace
	Report            DebugPromptReport
	ConversationPlan  conversation.ConversationPlan
	HistoryCount      int
	MemoryContext     memory.ContextResult
	MemoryContextErr  string
	PersonaAfterMerge prompt.PersonaConfig
}

// NewService 创建聊天服务，并规范化历史轮数与预算配置。
func NewService(
	repository repo.ChatMessageRepository,
	provider llm.Provider,
	memoryEngine memory.Engine,
	turnProcessor memory.TurnProcessor,
	temperature float64,
	maxHistoryRounds int,
	promptBudget prompt.BudgetConfig,
	conversationEmbedders ...signals.Embedder,
) *Service {
	if maxHistoryRounds <= 0 {
		maxHistoryRounds = 20
	}

	builder := prompt.NewBuilder()
	promptBudget = promptBudget.Normalize()

	return &Service{
		repo:             repository,
		provider:         provider,
		memoryEngine:     memoryEngine,
		turnProcessor:    turnProcessor,
		conversationCE:   conversation.NewEngine(conversationEmbedders...),
		promptBuilder:    builder,
		promptBudget:     promptBudget,
		temperature:      temperature,
		maxHistoryRounds: maxHistoryRounds,
	}
}

func (s *Service) UseSignalAnalyzer(analyzer signals.Analyzer) {
	if s == nil || s.conversationCE == nil {
		return
	}
	s.conversationCE.UseSignalAnalyzer(analyzer)
}

func (s *Service) UseTopicSummarizer(summarizer conversation.TopicSummarizer) {
	if s == nil || s.conversationCE == nil {
		return
	}
	s.conversationCE.UseTopicSummarizer(summarizer)
}

// StreamChat 执行完整聊天链路：读取历史、写入用户消息、流式生成、写入助手回复。
func (s *Service) StreamChat(ctx context.Context, in StreamChatInput, onDelta func(token string) error) (StreamChatResult, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.Message = strings.TrimSpace(in.Message)
	if in.SessionID == "" {
		in.SessionID = "default"
	}
	if in.UserID == "" {
		return StreamChatResult{}, fmt.Errorf("user_id is required")
	}
	if in.Message == "" {
		return StreamChatResult{}, fmt.Errorf("message is required")
	}

	historyMessages, err := s.loadHistoryMessages(ctx, in.UserID, in.SessionID)
	if err != nil {
		return StreamChatResult{}, err
	}

	userMessageID, err := s.repo.Insert(ctx, repo.ChatMessage{
		UserID:    in.UserID,
		SessionID: in.SessionID,
		Role:      "user",
		Content:   in.Message,
	})
	if err != nil {
		return StreamChatResult{}, err
	}

	finalPersona, memoryContext, memoryErr := s.mergePersonaWithMemory(ctx, in.UserID, in.SessionID, in.Message, in.Persona)
	if memoryErr != nil {
		// 记忆服务异常不应阻断聊天主流程，降级为仅使用入参 persona。
		log.Printf("memory build context failed user=%s session=%s: %v", in.UserID, in.SessionID, memoryErr)
	}
	plan, planText := s.buildConversationPlan(ctx, in.UserID, in.SessionID, in.Message, historyMessages, finalPersona, memoryContext)

	promptOutput, err := s.promptBuilder.Build(ctx, prompt.BuildRequest{
		UserID:           in.UserID,
		SessionID:        in.SessionID,
		UserMessage:      in.Message,
		ConversationPlan: planText,
		Now:              time.Now(),
		Budget:           s.promptBudget,
		Persona:          finalPersona,
		History:          historyMessages,
	})
	if err != nil {
		return StreamChatResult{}, err
	}
	temperature := s.temperature
	if in.Temperature != nil {
		temperature = *in.Temperature
	}

	var assistantReply strings.Builder
	err = s.provider.StreamGenerate(ctx, llm.GenerateRequest{
		Messages:    promptOutput.Messages,
		Temperature: temperature,
	}, func(delta string) error {
		assistantReply.WriteString(delta)
		if onDelta != nil {
			return onDelta(delta)
		}
		return nil
	})
	if err != nil {
		return StreamChatResult{}, err
	}

	// 保存助手最终完整内容，确保历史重放可还原。
	replyText := strings.TrimSpace(assistantReply.String())
	assistantMessageID, err := s.repo.Insert(ctx, repo.ChatMessage{
		UserID:    in.UserID,
		SessionID: in.SessionID,
		Role:      "assistant",
		Content:   replyText,
	})
	if err != nil {
		return StreamChatResult{}, err
	}

	turnInput := memory.TurnInput{
		UserID:              in.UserID,
		SessionID:           in.SessionID,
		UserMessageID:       userMessageID,
		AssistantMessageID:  assistantMessageID,
		UserMessage:         in.Message,
		AssistantMessage:    replyText,
		ConversationContext: buildConversationContext(historyMessages, in.Message),
		PlannedActions:      toMemoryActions(plan.Actions),
		Now:                 time.Now(),
	}
	if s.turnProcessor != nil {
		if err := s.turnProcessor.Enqueue(turnInput); err != nil {
			// 异步队列异常时降级同步，尽量保证记忆不丢。
			log.Printf("memory enqueue failed user=%s session=%s: %v; fallback to sync", in.UserID, in.SessionID, err)
			s.processMemoryTurnSync(ctx, turnInput, memoryContext.MemoryIDs)
		}
	} else {
		s.processMemoryTurnSync(ctx, turnInput, memoryContext.MemoryIDs)
	}

	return StreamChatResult{
		AssistantMessageID: assistantMessageID,
		AssistantContent:   replyText,
	}, nil
}

// processMemoryTurnSync 在无异步队列或入队失败时，回退到同步记忆处理。
func (s *Service) processMemoryTurnSync(ctx context.Context, in memory.TurnInput, memoryIDs []int64) {
	if s.memoryEngine == nil {
		return
	}
	if err := s.memoryEngine.ProcessTurn(ctx, in); err != nil {
		// 记忆抽取失败只影响后续召回，不影响当前聊天结果。
		log.Printf("memory process turn failed user=%s session=%s memory_ids=%v: %v", in.UserID, in.SessionID, memoryIDs, err)
	}
}

// BuildPromptDebug 仅执行 prompt 构建与可观测报告，不写入数据库。
func (s *Service) BuildPromptDebug(ctx context.Context, in DebugPromptInput) (DebugPromptResult, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.Message = strings.TrimSpace(in.Message)
	if in.SessionID == "" {
		in.SessionID = "default"
	}
	if in.UserID == "" {
		return DebugPromptResult{}, fmt.Errorf("user_id is required")
	}
	if in.Message == "" {
		return DebugPromptResult{}, fmt.Errorf("message is required")
	}

	historyMessages, err := s.loadHistoryMessages(ctx, in.UserID, in.SessionID)
	if err != nil {
		return DebugPromptResult{}, err
	}

	finalPersona, memoryContext, memoryErr := s.mergePersonaWithMemory(ctx, in.UserID, in.SessionID, in.Message, in.Persona)
	plan, planText := s.buildConversationPlan(ctx, in.UserID, in.SessionID, in.Message, historyMessages, finalPersona, memoryContext)

	result, err := s.promptBuilder.Build(ctx, prompt.BuildRequest{
		UserID:           in.UserID,
		SessionID:        in.SessionID,
		UserMessage:      in.Message,
		ConversationPlan: planText,
		Now:              time.Now(),
		ModelID:          strings.TrimSpace(in.ModelID),
		Options:          in.Options,
		Budget:           s.promptBudget,
		Persona:          finalPersona,
		History:          historyMessages,
	})
	if err != nil {
		return DebugPromptResult{}, err
	}

	memoryErrText := ""
	if memoryErr != nil {
		memoryErrText = memoryErr.Error()
	}

	return DebugPromptResult{
		Messages:          result.Messages,
		Trace:             result.Trace,
		Report:            buildDebugPromptReport(result.Trace),
		ConversationPlan:  plan,
		HistoryCount:      len(historyMessages),
		MemoryContext:     memoryContext,
		MemoryContextErr:  memoryErrText,
		PersonaAfterMerge: finalPersona,
	}, nil
}

// loadHistoryMessages 加载最近 N 轮（user+assistant）并转换为 llm.Message。
func (s *Service) loadHistoryMessages(ctx context.Context, userID, sessionID string) ([]llm.Message, error) {
	historyLimit := s.maxHistoryRounds * 2
	history, err := s.repo.ListRecent(ctx, userID, sessionID, historyLimit)
	if err != nil {
		return nil, err
	}

	historyMessages := make([]llm.Message, 0, len(history))
	for _, msg := range history {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		historyMessages = append(historyMessages, llm.Message{Role: msg.Role, Content: msg.Content})
	}
	return historyMessages, nil
}

// buildConversationContext 把最近对话压缩为文本，供抽取器做短句补全判断。
func buildConversationContext(history []llm.Message, currentUserMessage string) string {
	if len(history) == 0 && strings.TrimSpace(currentUserMessage) == "" {
		return ""
	}
	limit := 6
	if len(history) < limit {
		limit = len(history)
	}
	lines := make([]string, 0, limit+1)
	for i := len(history) - limit; i < len(history); i++ {
		if i < 0 {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(history[i].Role))
		content := strings.TrimSpace(history[i].Content)
		if content == "" {
			continue
		}
		if role != "assistant" && role != "user" {
			continue
		}
		lines = append(lines, role+": "+content)
	}
	if msg := strings.TrimSpace(currentUserMessage); msg != "" {
		lines = append(lines, "user: "+msg)
	}
	return strings.Join(lines, "\n")
}

// mergePersonaWithMemory 先召回长记忆，再以“用户显式输入优先”合并到 persona。
func (s *Service) mergePersonaWithMemory(
	ctx context.Context,
	userID, sessionID, userMessage string,
	base prompt.PersonaConfig,
) (prompt.PersonaConfig, memory.ContextResult, error) {
	if s.memoryEngine == nil {
		return base, memory.ContextResult{}, nil
	}

	memoryCtx, err := s.memoryEngine.BuildContext(ctx, memory.ContextRequest{
		UserID:      userID,
		SessionID:   sessionID,
		UserMessage: userMessage,
		Now:         time.Now(),
		K:           5,
	})
	if err != nil {
		return base, memory.ContextResult{}, err
	}

	merged := base
	merged.UserProfile = fillPersonaField(merged.UserProfile, memoryCtx.UserProfile)
	merged.UserPreferences = fillPersonaField(merged.UserPreferences, memoryCtx.UserPreferences)
	merged.UserBoundaries = fillPersonaField(merged.UserBoundaries, memoryCtx.UserBoundaries)
	merged.ImportantEvents = fillPersonaField(merged.ImportantEvents, memoryCtx.ImportantEvents)
	merged.TopicContext = fillPersonaField(merged.TopicContext, memoryCtx.TopicContext)
	merged.RelevantMemories = fillPersonaField(merged.RelevantMemories, memoryCtx.RelevantMemories)
	merged.RelationshipSummary = fillPersonaField(merged.RelationshipSummary, memoryCtx.RelationshipSummary)
	if !merged.RelationshipStageProvided && strings.TrimSpace(memoryCtx.RelationshipState.Stage) != "" {
		merged.RelationshipStage = strings.TrimSpace(memoryCtx.RelationshipState.Stage)
	}
	if strings.TrimSpace(memoryCtx.RelationshipState.Stage) != "" {
		merged.RelationshipFamiliarity = memoryCtx.RelationshipState.Familiarity
		merged.RelationshipIntimacy = memoryCtx.RelationshipState.Intimacy
		merged.RelationshipTrust = memoryCtx.RelationshipState.Trust
		merged.RelationshipFlirt = memoryCtx.RelationshipState.Flirt
		merged.RelationshipBoundaryRisk = memoryCtx.RelationshipState.BoundaryRisk
		merged.RelationshipSupportNeed = memoryCtx.RelationshipState.SupportNeed
		merged.RelationshipPlayfulness = memoryCtx.RelationshipState.Playfulness
		merged.RelationshipHeat = memoryCtx.RelationshipState.InteractionHeat
	}
	return merged, memoryCtx, nil
}

// fillPersonaField 仅在当前字段为空占位时才回填记忆值，避免覆盖调用方显式传参。
func fillPersonaField(current, fallback string) string {
	if !isPersonaPlaceholder(current) {
		return current
	}
	if isPersonaPlaceholder(fallback) {
		return current
	}
	return strings.TrimSpace(fallback)
}

// buildConversationPlan 汇总当前上下文并生成 CE 计划文本，失败时返回空计划（不影响主流程）。
func (s *Service) buildConversationPlan(
	ctx context.Context,
	userID, sessionID, userMessage string,
	history []llm.Message,
	persona prompt.PersonaConfig,
	memoryCtx memory.ContextResult,
) (conversation.ConversationPlan, string) {
	if s.conversationCE == nil {
		return conversation.ConversationPlan{}, ""
	}
	plan := s.conversationCE.BuildPlan(ctx, conversation.ConversationRequest{
		UserID:      userID,
		SessionID:   sessionID,
		UserMessage: userMessage,
		LastTurns:   history,
		MemorySnapshot: conversation.MemorySnapshot{
			UserProfile:      coalesceNonPlaceholder(memoryCtx.UserProfile, persona.UserProfile),
			UserPreferences:  coalesceNonPlaceholder(memoryCtx.UserPreferences, persona.UserPreferences),
			UserBoundaries:   coalesceNonPlaceholder(memoryCtx.UserBoundaries, persona.UserBoundaries),
			ImportantEvents:  coalesceNonPlaceholder(memoryCtx.ImportantEvents, persona.ImportantEvents),
			TopicContext:     coalesceNonPlaceholder(memoryCtx.TopicContext, persona.TopicContext),
			RelevantMemories: coalesceNonPlaceholder(memoryCtx.RelevantMemories, persona.RelevantMemories),
			ActiveTopics:     toConversationTopics(memoryCtx.ActiveTopics),
			TopicGraph:       toConversationTopicGraph(memoryCtx.TopicGraph),
		},
		Emotion:           persona.Emotion,
		EmotionIntensity:  persona.EmotionIntensity,
		RelationshipState: persona.RelationshipStage,
		RelationshipSnapshot: conversation.RelationshipSnapshot{
			Stage:           persona.RelationshipStage,
			Familiarity:     persona.RelationshipFamiliarity,
			Intimacy:        persona.RelationshipIntimacy,
			Trust:           persona.RelationshipTrust,
			Flirt:           persona.RelationshipFlirt,
			BoundaryRisk:    persona.RelationshipBoundaryRisk,
			SupportNeed:     persona.RelationshipSupportNeed,
			Playfulness:     persona.RelationshipPlayfulness,
			InteractionHeat: persona.RelationshipHeat,
			Summary:         persona.RelationshipSummary,
		},
		Now: time.Now(),
	})
	return plan, conversation.RenderPlanForPrompt(plan)
}

// coalesceNonPlaceholder 仅当 primary 不是占位值时优先 primary，否则回退 fallback。
func coalesceNonPlaceholder(primary, fallback string) string {
	if !isPersonaPlaceholder(primary) {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(fallback)
}

// toMemoryActions 将 CE 动作映射到 memory 层动作，供异步 worker 执行。
func toMemoryActions(actions []conversation.Action) []memory.TurnAction {
	if len(actions) == 0 {
		return nil
	}
	out := make([]memory.TurnAction, 0, len(actions))
	for _, action := range actions {
		actionType := strings.TrimSpace(action.Type)
		if actionType == "" {
			continue
		}
		params := map[string]string{}
		for k, v := range action.Params {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			params[k] = strings.TrimSpace(v)
		}
		out = append(out, memory.TurnAction{
			Type:   actionType,
			Params: params,
			Reason: strings.TrimSpace(action.Reason),
		})
	}
	return out
}

func toConversationTopics(items []memory.TopicSnapshot) []conversation.TopicSnapshot {
	if len(items) == 0 {
		return nil
	}
	out := make([]conversation.TopicSnapshot, 0, len(items))
	for _, item := range items {
		out = append(out, conversation.TopicSnapshot{
			TopicKey:         strings.TrimSpace(item.TopicKey),
			Label:            strings.TrimSpace(item.Label),
			Summary:          strings.TrimSpace(item.Summary),
			CallbackHint:     strings.TrimSpace(item.CallbackHint),
			ClusterKey:       strings.TrimSpace(item.ClusterKey),
			AliasTerms:       append([]string(nil), item.AliasTerms...),
			RelatedTopicKeys: append([]string(nil), item.RelatedTopicKeys...),
			Status:           strings.TrimSpace(item.Status),
			Importance:       item.Importance,
			MentionCount:     item.MentionCount,
			RecallCount:      item.RecallCount,
			LastDiscussedAt:  item.LastDiscussedAt,
			NextRecallAt:     item.NextRecallAt,
			LastRecalledAt:   item.LastRecalledAt,
		})
	}
	return out
}

func toConversationTopicGraph(items []memory.TopicEdgeSnapshot) []conversation.TopicEdgeSnapshot {
	if len(items) == 0 {
		return nil
	}
	out := make([]conversation.TopicEdgeSnapshot, 0, len(items))
	for _, item := range items {
		out = append(out, conversation.TopicEdgeSnapshot{
			FromTopicKey:  strings.TrimSpace(item.FromTopicKey),
			ToTopicKey:    strings.TrimSpace(item.ToTopicKey),
			RelationType:  strings.TrimSpace(item.RelationType),
			Weight:        item.Weight,
			EvidenceCount: item.EvidenceCount,
		})
	}
	return out
}

// isPersonaPlaceholder 判断字段是否为“未提供/暂无”占位。
func isPersonaPlaceholder(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || v == "暂无"
}
