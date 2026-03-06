package prompt

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"ai-gf/internal/llm"
)

// TestPromptBuilder_DeterministicAndWithinBudget 验证相同输入稳定输出且不超预算。
func TestPromptBuilder_DeterministicAndWithinBudget(t *testing.T) {
	builder := NewBuilder()
	req := BuildRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "今天有点焦虑，想让你陪我整理一下。",
		Budget: BudgetConfig{
			MaxPromptTokens: 800,
			ReserveTokens:   100,
			SystemRatio:     35,
			MemoryRatio:     25,
			HistoryRatio:    40,
		},
		Persona: PersonaConfig{
			BotName:           "Luna",
			UserName:          "Carry",
			RelationshipStage: "close",
			Emotion:           "anxious",
			EmotionIntensity:  0.75,
			UserProfile:       strings.Repeat("用户是后端工程师。", 30),
			UserPreferences:   strings.Repeat("喜欢先被共情再给方案。", 30),
			UserBoundaries:    "不要提家庭隐私细节",
			ImportantEvents:   strings.Repeat("周五有面试。", 30),
			RelevantMemories:  strings.Repeat("上次提过紧张。", 30),
			Language:          "zh-CN",
		},
		History: []llm.Message{
			{Role: "user", Content: strings.Repeat("最近对话A。", 20)},
			{Role: "assistant", Content: strings.Repeat("最近对话B。", 20)},
		},
	}

	out1, err := builder.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	out2, err := builder.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if !reflect.DeepEqual(out1.Messages, out2.Messages) {
		t.Fatalf("expected deterministic messages for same input")
	}

	if out1.Trace.TotalTokens > req.Budget.MaxPromptTokens {
		t.Fatalf("expected tokens <= %d, got %d", req.Budget.MaxPromptTokens, out1.Trace.TotalTokens)
	}
	if len(out1.Messages) < 2 {
		t.Fatalf("expected at least system + user messages")
	}
	if out1.Messages[0].Role != "system" {
		t.Fatalf("expected first message to be system")
	}
	if out1.Messages[len(out1.Messages)-1].Role != "user" {
		t.Fatalf("expected last message to be user")
	}
}

// TestPromptBuilder_InjectsConversationPlan 验证 CE 计划会注入 system prompt。
func TestPromptBuilder_InjectsConversationPlan(t *testing.T) {
	builder := NewBuilder()
	out, err := builder.Build(context.Background(), BuildRequest{
		UserID:           "u1",
		SessionID:        "s1",
		UserMessage:      "我今天有点焦虑",
		ConversationPlan: "- intent: emotional_support\n- mode: support_then_solve\n- ask: [\"你最担心哪一块？\"]",
		Budget:           DefaultBudgetConfig(),
		Persona:          PersonaConfig{Language: "zh-CN"},
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if len(out.Messages) == 0 {
		t.Fatalf("expected messages not empty")
	}
	if !strings.Contains(out.Messages[0].Content, "CONVERSATION_PLAN") {
		t.Fatalf("expected conversation plan injected into system prompt")
	}
}

// TestPromptBuilder_STMTrimKeepsLatestAndBuildsSummary 验证历史裁剪后保留最新消息并生成摘要。
func TestPromptBuilder_STMTrimKeepsLatestAndBuildsSummary(t *testing.T) {
	builder := NewBuilder()

	history := make([]llm.Message, 0, 28)
	for i := 0; i < 28; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		history = append(history, llm.Message{
			Role:    role,
			Content: fmt.Sprintf("history-%02d %s", i, strings.Repeat("内容", 45)),
		})
	}

	out, err := builder.Build(context.Background(), BuildRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "继续，我们接着聊。",
		Budget: BudgetConfig{
			MaxPromptTokens: 520,
			ReserveTokens:   120,
			SystemRatio:     35,
			MemoryRatio:     25,
			HistoryRatio:    40,
		},
		Persona: PersonaConfig{
			UserBoundaries: "不聊敏感隐私",
			Language:       "zh-CN",
		},
		History: history,
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	systemContent := out.Messages[0].Content
	if !strings.Contains(systemContent, "STM_SUMMARY") {
		t.Fatalf("expected summary injected when stm is trimmed")
	}

	foundLatest := false
	for _, msg := range out.Messages {
		if strings.Contains(msg.Content, "history-27") {
			foundLatest = true
			break
		}
	}
	if !foundLatest {
		t.Fatalf("expected latest stm message to be kept")
	}
}

// TestPromptBuilder_PolicyDropsRAGOnBoundaryConflict 验证边界冲突时会移除 RAG。
func TestPromptBuilder_PolicyDropsRAGOnBoundaryConflict(t *testing.T) {
	builder := NewBuilder()
	out, err := builder.Build(context.Background(), BuildRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "聊聊别的",
		Persona: PersonaConfig{
			UserBoundaries:   "不要提前任",
			RelevantMemories: "用户上次提到和前任吵架",
			Language:         "zh-CN",
		},
		Budget: DefaultBudgetConfig(),
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if strings.Contains(out.Messages[0].Content, "RELEVANT_MEMORIES") {
		t.Fatalf("expected rag block removed due boundary conflict")
	}
}

// TestPromptBuilder_ModuleDegradeAndRequiredFail 验证可选模块降级与必需模块失败行为。
func TestPromptBuilder_ModuleDegradeAndRequiredFail(t *testing.T) {
	degraded := PromptBuilder{
		modules: []Module{
			&mockModule{
				id:       "optional_fail",
				priority: 10,
				required: false,
				err:      errors.New("module failed"),
				degrade: []PromptBlock{
					{ID: "optional_fallback", Priority: 11, Kind: MessageKindSystem, Bucket: BucketProfile, Content: "fallback"},
				},
			},
			newUserMessageModule(),
		},
		policy: newDefaultPolicyEngine(),
		budget: newDefaultTokenBudgeter(),
		asm:    newDefaultAssembler(),
	}

	okOut, err := degraded.Build(context.Background(), BuildRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "hello",
		Budget: BudgetConfig{
			MaxPromptTokens: 400,
			ReserveTokens:   60,
			SystemRatio:     35,
			MemoryRatio:     25,
			HistoryRatio:    40,
		},
	})
	if err != nil {
		t.Fatalf("expected degraded build success, got %v", err)
	}
	if len(okOut.Messages) == 0 || !strings.Contains(okOut.Messages[0].Content, "fallback") {
		t.Fatalf("expected degrade fallback block in output")
	}

	failed := PromptBuilder{
		modules: []Module{
			&mockModule{
				id:       "required_fail",
				priority: 10,
				required: true,
				err:      errors.New("required failure"),
			},
		},
		policy: newDefaultPolicyEngine(),
		budget: newDefaultTokenBudgeter(),
		asm:    newDefaultAssembler(),
	}
	_, err = failed.Build(context.Background(), BuildRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "hello",
		Budget:      DefaultBudgetConfig(),
	})
	if err == nil {
		t.Fatalf("expected required module failure to return error")
	}
}

// TestPromptBuilder_RAGDegradesByK 验证预算紧张时 RAG 会走 K 递减降级。
func TestPromptBuilder_RAGDegradesByK(t *testing.T) {
	builder := NewBuilder()
	relevantMemories := strings.Join([]string{
		"- [id:m1 conf:0.95] 用户明天下午有面试，担心发挥不好",
		"- [id:m2 conf:0.90] 用户偏好先被共情，再给一个很小的执行建议",
		"- [id:m3 conf:0.86] 用户不喜欢被说教，讨厌模板化安慰",
		"- [id:m4 conf:0.82] 最近三天睡眠不足，容易焦虑",
		"- [id:m5 conf:0.80] 用户希望回复尽量简短，但不要冷淡",
	}, "\n")

	out, err := builder.Build(context.Background(), BuildRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我今晚还是有点紧张。",
		Budget: BudgetConfig{
			MaxPromptTokens: 520,
			ReserveTokens:   120,
			SystemRatio:     35,
			MemoryRatio:     25,
			HistoryRatio:    40,
		},
		Persona: PersonaConfig{
			UserBoundaries:   "不讨论家庭隐私",
			RelevantMemories: relevantMemories,
			Language:         "zh-CN",
		},
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if out.Trace.RAGStats.RequestedK <= 0 {
		t.Fatalf("expected requested rag k > 0")
	}
	if out.Trace.RAGStats.KeptK > out.Trace.RAGStats.RequestedK {
		t.Fatalf("expected kept rag k <= requested rag k")
	}
	if out.Trace.RAGStats.KeptK == out.Trace.RAGStats.RequestedK {
		t.Fatalf("expected rag to degrade under tight budget")
	}
}

// TestPromptBuilder_STMSummaryUsesSummaryBucket 验证 STM 摘要走独立 summary 桶。
func TestPromptBuilder_STMSummaryUsesSummaryBucket(t *testing.T) {
	builder := NewBuilder()

	history := make([]llm.Message, 0, 26)
	for i := 0; i < 26; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		history = append(history, llm.Message{
			Role:    role,
			Content: fmt.Sprintf("history-%02d %s", i, strings.Repeat("内容", 40)),
		})
	}

	out, err := builder.Build(context.Background(), BuildRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我们继续",
		Budget: BudgetConfig{
			MaxPromptTokens: 560,
			ReserveTokens:   140,
			SystemRatio:     35,
			MemoryRatio:     25,
			HistoryRatio:    40,
		},
		Persona: PersonaConfig{
			UserBoundaries: "不聊敏感隐私",
			Language:       "zh-CN",
		},
		History: history,
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if out.Trace.BucketBudget[BucketSummary] <= 0 {
		t.Fatalf("expected summary bucket budget > 0")
	}
	if out.Trace.BucketUsed[BucketSummary] <= 0 {
		t.Fatalf("expected summary bucket used > 0 when stm is dropped")
	}
}

// mockModule 是 PromptBuilder 单测中的模块桩。
type mockModule struct {
	id       string
	priority int
	required bool
	err      error
	degrade  []PromptBlock
}

// ID 返回桩模块 ID。
func (m *mockModule) ID() string { return m.id }

// Priority 返回桩模块优先级。
func (m *mockModule) Priority() int { return m.priority }

// Required 返回桩模块是否必需。
func (m *mockModule) Required() bool { return m.required }

// Build 根据预设错误决定是否失败。
func (m *mockModule) Build(ctx context.Context, req BuildRequest) ([]PromptBlock, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []PromptBlock{}, nil
}

// Degrade 返回预设降级 block。
func (m *mockModule) Degrade(ctx context.Context, req BuildRequest, err error) []PromptBlock {
	return m.degrade
}
