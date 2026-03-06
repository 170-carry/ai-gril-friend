package memory

import (
	"strings"
	"testing"
	"time"

	"ai-gf/internal/repo"
)

func TestBuildTopicSnapshotsIncludesAliasesAndGraph(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	items := []repo.ConversationTopic{
		{
			TopicKey:     "sleep_state",
			TopicLabel:   "睡眠状态",
			CallbackHint: "这个脑子停不下来",
			ClusterKey:   "睡眠状态_脑子停不下来",
			Status:       "active",
			Importance:   4,
			Metadata: map[string]any{
				"alias_terms": []any{"脑子停不下来", "睡不着"},
			},
			LastDiscussedAt: &now,
		},
	}
	edges := []repo.ConversationTopicEdge{
		{
			FromTopicKey: "sleep_state",
			ToTopicKey:   "work_pressure",
			RelationType: "co_occurs",
			Weight:       2,
		},
	}

	out := buildTopicSnapshots(items, edges)
	if len(out) != 1 {
		t.Fatalf("expected 1 topic snapshot, got %d", len(out))
	}
	if len(out[0].AliasTerms) == 0 {
		t.Fatalf("expected alias terms from metadata, got %+v", out[0])
	}
	if len(out[0].RelatedTopicKeys) != 1 || out[0].RelatedTopicKeys[0] != "work_pressure" {
		t.Fatalf("expected related topic key from graph edge, got %+v", out[0])
	}
}

func TestFormatTopicContextIncludesRelatedThreads(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	items := []repo.ConversationTopic{
		{
			TopicKey:        "sleep_state",
			TopicLabel:      "睡眠状态",
			Summary:         "你最近总是因为脑子停不下来而睡不着。",
			CallbackHint:    "这个脑子停不下来",
			Status:          "active",
			Importance:      4,
			LastDiscussedAt: &now,
		},
		{
			TopicKey:        "work_pressure",
			TopicLabel:      "工作压力",
			Summary:         "老板最近压得很紧。",
			Status:          "active",
			Importance:      4,
			LastDiscussedAt: &now,
		},
	}
	edges := []repo.ConversationTopicEdge{
		{
			FromTopicKey: "sleep_state",
			ToTopicKey:   "work_pressure",
			RelationType: "co_occurs",
			Weight:       2,
		},
	}

	text := formatTopicContext(items, edges)
	if !strings.Contains(text, "关联线程") {
		t.Fatalf("expected related threads info in topic context, got %s", text)
	}
}
