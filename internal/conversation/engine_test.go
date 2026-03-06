package conversation

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBuildPlan_PreferenceAndQuestion 验证偏好输入会生成记忆动作且追问不超过 1 个。
func TestBuildPlan_PreferenceAndQuestion(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "记住我喜欢喝拿铁，最近有点焦虑",
		Now:         time.Date(2026, 3, 5, 20, 0, 0, 0, time.FixedZone("CST", 8*3600)),
	})

	if plan.Intent != IntentEmotionalSupport {
		t.Fatalf("expected emotional support intent, got %s", plan.Intent)
	}
	if len(plan.Questions) > 1 {
		t.Fatalf("expected <=1 question, got %d", len(plan.Questions))
	}
	foundPreference := false
	for _, action := range plan.Actions {
		if action.Type == "SAVE_PREFERENCE" {
			foundPreference = true
			break
		}
	}
	if !foundPreference {
		t.Fatalf("expected SAVE_PREFERENCE action")
	}
}

// TestBuildPlan_StopAskingWhenUserRefuses 验证用户拒绝时会停止追问。
func TestBuildPlan_StopAskingWhenUserRefuses(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我有点累，先这样，别问了",
	})

	if len(plan.Questions) != 0 {
		t.Fatalf("expected no questions when user refuses, got %v", plan.Questions)
	}
	stopText := strings.Join(plan.StopRules, " | ")
	if !strings.Contains(stopText, "停止追问") {
		t.Fatalf("expected stop rules contain refusal handling, got %s", stopText)
	}
}

// TestBuildPlan_EventActions 验证事件语句会触发事件写入与提醒动作。
func TestBuildPlan_EventActions(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我明天下午三点有面试，提醒我别迟到",
	})

	hasSaveEvent := false
	hasReminder := false
	for _, action := range plan.Actions {
		if action.Type == "SAVE_EVENT" {
			hasSaveEvent = true
		}
		if action.Type == "SCHEDULE_EVENT_REMINDER" {
			hasReminder = true
		}
	}
	if !hasSaveEvent || !hasReminder {
		t.Fatalf("expected SAVE_EVENT and SCHEDULE_EVENT_REMINDER, actions=%+v", plan.Actions)
	}
}

// TestBuildPlan_NoFalseEventOnYouDian 验证“有点焦虑”不会误判成时间事件。
func TestBuildPlan_NoFalseEventOnYouDian(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我今天有点焦虑，想找个人陪我聊聊",
	})

	for _, action := range plan.Actions {
		if action.Type == "SAVE_EVENT" || action.Type == "SCHEDULE_EVENT_REMINDER" {
			t.Fatalf("did not expect event actions for non-event sentence, actions=%+v", plan.Actions)
		}
	}
}

// TestBuildPlan_SafetyGate 验证高风险词命中时进入 safety_support。
func TestBuildPlan_SafetyGate(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我有点想轻生",
	})

	if plan.Mode != "safety_support" {
		t.Fatalf("expected safety_support mode, got %s", plan.Mode)
	}
	if len(plan.Questions) != 0 {
		t.Fatalf("expected no questions in safety mode, got %v", plan.Questions)
	}
}

// TestRenderPlanForPrompt 验证计划渲染文本包含关键字段。
func TestRenderPlanForPrompt(t *testing.T) {
	t.Parallel()

	text := RenderPlanForPrompt(ConversationPlan{
		Intent:           IntentAdviceSolving,
		IntentConfidence: 0.88,
		Mode:             "solve_then_commit",
		CurrentState:     StateSolve,
		NextState:        StateCommit,
		Tone: ToneConfig{
			Warmth:      0.7,
			Playfulness: 0.2,
			Directness:  0.8,
			Length:      "short",
			EmojiLevel:  0,
		},
		ResponseStructure: []string{"mirror", "add_value", "ask", "close_softly"},
		Questions:         []string{"你更想先做哪一步？"},
		StopRules:         []string{"每轮最多提 1 个问题。"},
		Actions: []Action{
			{Type: "SAVE_SEMANTIC_MEMORY", Params: map[string]string{"sentence": "用户提到面试"}},
		},
	})

	if !strings.Contains(text, "intent: advice_problem_solving") {
		t.Fatalf("unexpected render text: %s", text)
	}
	if !strings.Contains(text, "structure: mirror -> add_value -> ask -> close_softly") {
		t.Fatalf("unexpected structure render: %s", text)
	}
}
