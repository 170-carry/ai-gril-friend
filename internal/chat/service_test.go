package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-gf/internal/llm"
	"ai-gf/internal/memory"
	"ai-gf/internal/memory/ranking"
	"ai-gf/internal/prompt"
	"ai-gf/internal/repo"
)

// fakeRepo 是用于单测的内存仓库桩。
type fakeRepo struct {
	inserted   []repo.ChatMessage
	listResult []repo.ChatMessage
	listLimit  int
	nextID     int64
}

// Insert 记录写入参数并返回自增 ID。
func (r *fakeRepo) Insert(ctx context.Context, msg repo.ChatMessage) (int64, error) {
	r.inserted = append(r.inserted, msg)
	r.nextID++
	return r.nextID, nil
}

// ListRecent 返回预设历史并记录查询 limit。
func (r *fakeRepo) ListRecent(ctx context.Context, userID, sessionID string, limit int) ([]repo.ChatMessage, error) {
	r.listLimit = limit
	return r.listResult, nil
}

// fakeProvider 是用于单测的模型桩，实现可控的流式输出。
type fakeProvider struct {
	seenMessages []llm.Message
	deltas       []string
}

// fakeMemoryEngine 是用于单测的记忆引擎桩。
type fakeMemoryEngine struct {
	contextResp        memory.ContextResult
	contextErr         error
	lastContextRequest memory.ContextRequest
	lastTurnInput      memory.TurnInput
	processErr         error
	processCalled      bool
}

// fakeTurnProcessor 是异步队列桩，记录是否收到入队请求。
type fakeTurnProcessor struct {
	lastInput     memory.TurnInput
	enqueueCalled bool
	enqueueErr    error
}

func (p *fakeTurnProcessor) Enqueue(in memory.TurnInput) error {
	p.lastInput = in
	p.enqueueCalled = true
	return p.enqueueErr
}

func (p *fakeTurnProcessor) Close() error {
	return nil
}

// BuildContext 返回预设记忆上下文，并记录入参。
func (m *fakeMemoryEngine) BuildContext(ctx context.Context, req memory.ContextRequest) (memory.ContextResult, error) {
	m.lastContextRequest = req
	if m.contextErr != nil {
		return memory.ContextResult{}, m.contextErr
	}
	return m.contextResp, nil
}

// ProcessTurn 记录一轮写回入参。
func (m *fakeMemoryEngine) ProcessTurn(ctx context.Context, in memory.TurnInput) error {
	m.lastTurnInput = in
	m.processCalled = true
	return m.processErr
}

// StreamGenerate 将预设增量逐个回调。
func (p *fakeProvider) StreamGenerate(ctx context.Context, req llm.GenerateRequest, onDelta func(delta string) error) error {
	p.seenMessages = req.Messages
	for _, d := range p.deltas {
		if err := onDelta(d); err != nil {
			return err
		}
	}
	return nil
}

// Generate 满足接口要求，当前测试用不到。
func (p *fakeProvider) Generate(ctx context.Context, req llm.GenerateRequest) (string, error) {
	return "", nil
}

// Embed 满足接口要求，当前测试用不到。
func (p *fakeProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, nil
}

// TestStreamChat_PersistsUserAndAssistantMessages 验证主流程会写入 user+assistant 两条消息。
func TestStreamChat_PersistsUserAndAssistantMessages(t *testing.T) {
	repoStub := &fakeRepo{
		listResult: []repo.ChatMessage{
			{Role: "user", Content: "你好"},
			{Role: "assistant", Content: "在呢"},
		},
	}
	providerStub := &fakeProvider{deltas: []string{"我", "在", "这"}}
	service := NewService(repoStub, providerStub, nil, nil, 0.8, 20, prompt.DefaultBudgetConfig())

	result, err := service.StreamChat(context.Background(), StreamChatInput{
		UserID:    "u1",
		SessionID: "s1",
		Message:   "今天有点累",
		Persona:   prompt.DefaultPersonaConfig(),
	}, nil)
	if err != nil {
		t.Fatalf("StreamChat returned error: %v", err)
	}

	if repoStub.listLimit != 40 {
		t.Fatalf("expected recent history limit 40, got %d", repoStub.listLimit)
	}
	if len(repoStub.inserted) != 2 {
		t.Fatalf("expected 2 inserted messages, got %d", len(repoStub.inserted))
	}
	if repoStub.inserted[0].Role != "user" {
		t.Fatalf("expected first inserted role user, got %s", repoStub.inserted[0].Role)
	}
	if repoStub.inserted[1].Role != "assistant" {
		t.Fatalf("expected second inserted role assistant, got %s", repoStub.inserted[1].Role)
	}
	if repoStub.inserted[1].Content != "我在这" {
		t.Fatalf("expected assistant content 我在这, got %s", repoStub.inserted[1].Content)
	}
	if result.AssistantMessageID == 0 {
		t.Fatalf("expected assistant message id > 0")
	}
	if len(providerStub.seenMessages) == 0 || providerStub.seenMessages[0].Role != "system" {
		t.Fatalf("expected first prompt message to be system")
	}
}

// TestBuildPromptDebug_DoesNotPersistMessages 验证 debug 模式不落库。
func TestBuildPromptDebug_DoesNotPersistMessages(t *testing.T) {
	repoStub := &fakeRepo{
		listResult: []repo.ChatMessage{
			{Role: "user", Content: "上次你说要早睡"},
			{Role: "assistant", Content: "对，今天也早点休息哦"},
		},
	}
	providerStub := &fakeProvider{}
	service := NewService(repoStub, providerStub, nil, nil, 0.8, 20, prompt.DefaultBudgetConfig())

	out, err := service.BuildPromptDebug(context.Background(), DebugPromptInput{
		UserID:    "u1",
		SessionID: "s1",
		Message:   "我现在有点焦虑",
		Persona:   prompt.DefaultPersonaConfig(),
	})
	if err != nil {
		t.Fatalf("BuildPromptDebug returned error: %v", err)
	}

	if repoStub.listLimit != 40 {
		t.Fatalf("expected recent history limit 40, got %d", repoStub.listLimit)
	}
	if len(repoStub.inserted) != 0 {
		t.Fatalf("expected no inserted message in debug mode, got %d", len(repoStub.inserted))
	}
	if len(out.Messages) < 2 {
		t.Fatalf("expected at least system and user messages")
	}
	if out.Messages[0].Role != "system" {
		t.Fatalf("expected first message to be system")
	}
	if out.Messages[len(out.Messages)-1].Role != "user" {
		t.Fatalf("expected last message to be user")
	}
	if len(out.Report.StageReports) == 0 {
		t.Fatalf("expected stage reports in debug output")
	}
	if len(out.Report.FinalBlocks) == 0 {
		t.Fatalf("expected final blocks report in debug output")
	}
	if out.ConversationPlan.Intent == "" {
		t.Fatalf("expected conversation plan intent in debug output")
	}
}

// TestStreamChat_MemoryContextAndProcessTurn 验证聊天链路会读取记忆并在结束后回写抽取结果。
func TestStreamChat_MemoryContextAndProcessTurn(t *testing.T) {
	repoStub := &fakeRepo{
		listResult: []repo.ChatMessage{
			{Role: "user", Content: "之前聊过猫"},
			{Role: "assistant", Content: "记得你喜欢橘猫"},
		},
	}
	providerStub := &fakeProvider{deltas: []string{"记", "得", "呀"}}
	memoryStub := &fakeMemoryEngine{
		contextResp: memory.ContextResult{
			UserPreferences:  "- music: kpop",
			UserBoundaries:   "- politics",
			ImportantEvents:  "- 03-06 14:00 面试",
			RelevantMemories: "- [id:m_11 conf:0.90 topic:event] 用户周五面试",
			RankTrace: []ranking.TraceItem{
				{CandidateID: "chunk_11", SourceID: 11, Kind: ranking.CandidateSemantic, Generated: true, Selected: true},
			},
		},
	}
	service := NewService(repoStub, providerStub, memoryStub, nil, 0.8, 20, prompt.DefaultBudgetConfig())

	result, err := service.StreamChat(context.Background(), StreamChatInput{
		UserID:    "u1",
		SessionID: "s1",
		Message:   "我明天面试",
		Persona: prompt.PersonaConfig{
			BotName:          "Luna",
			UserName:         "Carry",
			UserPreferences:  "暂无",
			UserBoundaries:   "暂无",
			ImportantEvents:  "暂无",
			RelevantMemories: "暂无",
		},
	}, nil)
	if err != nil {
		t.Fatalf("StreamChat returned error: %v", err)
	}
	if result.AssistantContent != "记得呀" {
		t.Fatalf("expected assistant content 记得呀, got %s", result.AssistantContent)
	}
	if memoryStub.lastContextRequest.UserID != "u1" || memoryStub.lastContextRequest.SessionID != "s1" {
		t.Fatalf("unexpected memory context request: %+v", memoryStub.lastContextRequest)
	}
	if !memoryStub.processCalled {
		t.Fatalf("expected memory ProcessTurn called")
	}
	if memoryStub.lastTurnInput.UserMessage != "我明天面试" || memoryStub.lastTurnInput.AssistantMessage != "记得呀" {
		t.Fatalf("unexpected turn input: %+v", memoryStub.lastTurnInput)
	}
	if len(providerStub.seenMessages) == 0 || providerStub.seenMessages[0].Role != "system" {
		t.Fatalf("expected first message to be system")
	}
}

// TestBuildPromptDebug_ContainsMemoryContext 验证 debug 输出包含记忆上下文与合并后 persona。
func TestBuildPromptDebug_ContainsMemoryContext(t *testing.T) {
	repoStub := &fakeRepo{
		listResult: []repo.ChatMessage{
			{Role: "user", Content: "你还记得我吗"},
			{Role: "assistant", Content: "当然记得"},
		},
	}
	providerStub := &fakeProvider{}
	memoryStub := &fakeMemoryEngine{
		contextResp: memory.ContextResult{
			UserProfile:      "昵称：小宇",
			UserPreferences:  "- game: dota2",
			UserBoundaries:   "- ex relationship",
			ImportantEvents:  "- 03-06 14:00 面试",
			RelevantMemories: "- [id:m_7 conf:0.88 topic:preference] 用户喜欢 dota2",
		},
	}
	service := NewService(repoStub, providerStub, memoryStub, nil, 0.8, 20, prompt.DefaultBudgetConfig())

	out, err := service.BuildPromptDebug(context.Background(), DebugPromptInput{
		UserID:    "u1",
		SessionID: "s1",
		Message:   "我又有点紧张",
		Persona: prompt.PersonaConfig{
			BotName:          "Luna",
			UserName:         "Carry",
			UserProfile:      "暂无",
			UserPreferences:  "暂无",
			UserBoundaries:   "暂无",
			ImportantEvents:  "暂无",
			RelevantMemories: "暂无",
		},
	})
	if err != nil {
		t.Fatalf("BuildPromptDebug returned error: %v", err)
	}
	if out.MemoryContext.UserProfile != "昵称：小宇" {
		t.Fatalf("expected memory context profile merged, got %s", out.MemoryContext.UserProfile)
	}
	if out.MemoryContextErr != "" {
		t.Fatalf("expected empty memory error, got %s", out.MemoryContextErr)
	}
	if out.PersonaAfterMerge.UserProfile != "昵称：小宇" {
		t.Fatalf("expected merged persona profile, got %s", out.PersonaAfterMerge.UserProfile)
	}
}

// TestBuildPromptDebug_MemoryErrorNonFatal 验证记忆构建失败时 debug 仍可返回 prompt。
func TestBuildPromptDebug_MemoryErrorNonFatal(t *testing.T) {
	repoStub := &fakeRepo{
		listResult: []repo.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
	}
	providerStub := &fakeProvider{}
	memoryStub := &fakeMemoryEngine{
		contextErr: errors.New("memory down"),
	}
	service := NewService(repoStub, providerStub, memoryStub, nil, 0.8, 20, prompt.DefaultBudgetConfig())

	out, err := service.BuildPromptDebug(context.Background(), DebugPromptInput{
		UserID:    "u1",
		SessionID: "s1",
		Message:   "test",
		Persona:   prompt.DefaultPersonaConfig(),
	})
	if err != nil {
		t.Fatalf("BuildPromptDebug returned error: %v", err)
	}
	if out.MemoryContextErr != "memory down" {
		t.Fatalf("expected memory error text, got %s", out.MemoryContextErr)
	}
	if len(out.Messages) == 0 {
		t.Fatalf("expected prompt messages even when memory fails")
	}
	if memoryStub.lastContextRequest.Now.IsZero() {
		t.Fatalf("expected context request timestamp set")
	}
	if time.Since(memoryStub.lastContextRequest.Now) > time.Minute {
		t.Fatalf("unexpected old context timestamp: %v", memoryStub.lastContextRequest.Now)
	}
}

// TestStreamChat_UsesTurnProcessorQueue 验证配置异步处理器时会优先入队。
func TestStreamChat_UsesTurnProcessorQueue(t *testing.T) {
	repoStub := &fakeRepo{
		listResult: []repo.ChatMessage{
			{Role: "user", Content: "上次我们聊到面试"},
			{Role: "assistant", Content: "嗯，我记得"},
		},
	}
	providerStub := &fakeProvider{deltas: []string{"好", "呀"}}
	memoryStub := &fakeMemoryEngine{
		contextResp: memory.ContextResult{
			RelevantMemories: "- [id:m_3 conf:0.88 topic:event] 用户周五有面试",
		},
	}
	queueStub := &fakeTurnProcessor{}
	service := NewService(repoStub, providerStub, memoryStub, queueStub, 0.8, 20, prompt.DefaultBudgetConfig())

	_, err := service.StreamChat(context.Background(), StreamChatInput{
		UserID:    "u1",
		SessionID: "s1",
		Message:   "我明天有点紧张",
		Persona:   prompt.DefaultPersonaConfig(),
	}, nil)
	if err != nil {
		t.Fatalf("StreamChat returned error: %v", err)
	}
	if !queueStub.enqueueCalled {
		t.Fatalf("expected turn processor enqueue called")
	}
	if memoryStub.processCalled {
		t.Fatalf("expected sync ProcessTurn not called when queue enqueue succeeds")
	}
	if queueStub.lastInput.UserMessage == "" || queueStub.lastInput.AssistantMessage == "" {
		t.Fatalf("expected turn input to carry user/assistant message")
	}
	if len(queueStub.lastInput.PlannedActions) == 0 {
		t.Fatalf("expected planned actions passed to async worker")
	}
}
