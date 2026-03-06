package proactive

import (
	"strings"
	"testing"
	"time"

	"ai-gf/internal/repo"
)

func TestBuildDedupKeyStable(t *testing.T) {
	runAt := time.Date(2026, 3, 6, 14, 0, 0, 0, time.UTC)
	req := ScheduleRequest{
		UserID:    "u1",
		SessionID: "s1",
		TaskType:  "event_reminder",
		RunAt:     runAt,
		Payload: map[string]any{
			"target_title": "面试",
			"offset":       "-2h",
		},
	}

	key1, err := BuildDedupKey(req)
	if err != nil {
		t.Fatalf("BuildDedupKey returned error: %v", err)
	}
	key2, err := BuildDedupKey(req)
	if err != nil {
		t.Fatalf("BuildDedupKey returned error on second call: %v", err)
	}
	if key1 != key2 {
		t.Fatalf("expected stable dedup key, got %s vs %s", key1, key2)
	}
}

func TestShouldDeferForQuietHours(t *testing.T) {
	state := repo.ProactiveState{
		QuietHoursEnabled: true,
		QuietStartMinute:  22 * 60,
		QuietEndMinute:    8 * 60,
		Timezone:          "Asia/Shanghai",
	}
	now := time.Date(2026, 3, 6, 23, 30, 0, 0, time.FixedZone("CST", 8*3600))

	defered, nextAt := ShouldDeferForQuietHours(state, now)
	if !defered {
		t.Fatalf("expected quiet hours deferral")
	}
	if nextAt.Hour() != 8 || nextAt.Minute() != 0 {
		t.Fatalf("expected next quiet end at 08:00, got %v", nextAt)
	}
}

func TestShouldDeferForCooldown(t *testing.T) {
	last := time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)
	state := repo.ProactiveState{
		CooldownSeconds: 12 * 60 * 60,
		LastProactiveAt: &last,
	}
	task := repo.ProactiveTask{
		CooldownSeconds: 6 * 60 * 60,
	}

	defered, nextAt := ShouldDeferForCooldown(state, task, last.Add(2*time.Hour))
	if !defered {
		t.Fatalf("expected cooldown deferral")
	}
	if nextAt.Sub(last) != 12*time.Hour {
		t.Fatalf("expected state cooldown to win, got %v", nextAt.Sub(last))
	}
}

func TestBuildOutboundMessage(t *testing.T) {
	item := repo.OutboundQueueItem{
		ID:       11,
		TaskID:   22,
		Reason:   "事件前提醒（主动钩子）",
		DedupKey: "event_reminder_xxx",
		Payload: map[string]any{
			"task_type":    "event_reminder",
			"target_title": "面试",
		},
	}

	text, payload := BuildOutboundMessage(item)
	if !strings.Contains(text, "面试") {
		t.Fatalf("expected outbound content contains target title, got %s", text)
	}
	if got := payload["kind"]; got != "event_reminder" {
		t.Fatalf("unexpected kind: %v", got)
	}
	if got := payload["task_id"]; got != int64(22) {
		t.Fatalf("unexpected task_id: %v", got)
	}
}
