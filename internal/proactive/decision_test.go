package proactive

import (
	"testing"
	"time"

	"ai-gf/internal/repo"
)

func TestDecisionEngine_CancelWhenReasonIsMissing(t *testing.T) {
	t.Parallel()

	engine := NewDecisionEngine(DefaultDecisionConfig())
	now := time.Date(2026, 3, 6, 15, 0, 0, 0, time.UTC)
	task := repo.ProactiveTask{
		ID:        1,
		TaskType:  "care_followup",
		Reason:    "提醒",
		RunAt:     now,
		SessionID: "s1",
		UserID:    "u1",
		Payload: map[string]any{
			"offset": "+24h",
		},
	}
	state := repo.ProactiveState{
		Enabled: true,
	}

	result := engine.Evaluate(task, state, now)
	if result.Action != DecisionCancel {
		t.Fatalf("expected cancel for vague reason, got %s", result.Action)
	}
	if result.Summary != "缺少明确的主动理由" {
		t.Fatalf("unexpected summary: %s", result.Summary)
	}
}

func TestDecisionEngine_DeferWhenRecentProactiveExists(t *testing.T) {
	t.Parallel()

	engine := NewDecisionEngine(DefaultDecisionConfig())
	last := time.Date(2026, 3, 6, 8, 0, 0, 0, time.UTC)
	now := last.Add(2 * time.Hour)
	task := repo.ProactiveTask{
		ID:              2,
		TaskType:        "care_followup",
		Reason:          "高强度负面情绪回访",
		RunAt:           now,
		CooldownSeconds: 12 * 60 * 60,
	}
	state := repo.ProactiveState{
		Enabled:         true,
		LastProactiveAt: &last,
	}

	result := engine.Evaluate(task, state, now)
	if result.Action != DecisionDefer {
		t.Fatalf("expected defer for recent proactive, got %s", result.Action)
	}
	if result.NextAttemptAt == nil {
		t.Fatalf("expected next attempt time to be returned")
	}
	if result.NextAttemptAt.Sub(last) != 12*time.Hour {
		t.Fatalf("expected next attempt at last+12h, got %v", result.NextAttemptAt.Sub(last))
	}
}

func TestDecisionEngine_CancelWhenCopyTooSimilar(t *testing.T) {
	t.Parallel()

	engine := NewDecisionEngine(DecisionConfig{
		DefaultRecentWindow: 30 * time.Minute,
		MinRecentWindow:     30 * time.Minute,
		SimilarityLookback:  24 * time.Hour,
		SimilarityThreshold: 0.9,
		ReasonMinRuneCount:  4,
	})
	last := time.Date(2026, 3, 6, 8, 0, 0, 0, time.UTC)
	now := last.Add(2 * time.Hour)
	task := repo.ProactiveTask{
		ID:        3,
		TaskType:  "care_followup",
		Reason:    "高强度负面情绪回访",
		UserID:    "u1",
		SessionID: "s1",
		RunAt:     now,
	}
	state := repo.ProactiveState{
		Enabled:               true,
		LastProactiveAt:       &last,
		LastProactiveTaskType: "care_followup",
		LastProactiveContent:  "我来回访一下你刚才的状态，现在感觉怎么样了？如果你愿意，我还在这陪你。",
	}

	result := engine.Evaluate(task, state, now)
	if result.Action != DecisionCancel {
		t.Fatalf("expected cancel for similar copy, got %s", result.Action)
	}
	if result.SimilarityScore < 0.9 {
		t.Fatalf("expected high similarity score, got %.3f", result.SimilarityScore)
	}
}

func TestTextSimilarity(t *testing.T) {
	t.Parallel()

	same := TextSimilarity("小提醒：你之前提到面试，现在差不多到时间啦。", "小提醒：你之前提到面试，现在差不多到时间啦。")
	diff := TextSimilarity("小提醒：你之前提到面试，现在差不多到时间啦。", "晚安，今天早点休息哦。")
	if same != 1 {
		t.Fatalf("expected exact match similarity to be 1, got %.3f", same)
	}
	if diff >= 0.5 {
		t.Fatalf("expected different texts similarity to stay low, got %.3f", diff)
	}
}
