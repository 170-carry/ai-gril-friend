package memory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"ai-gf/internal/proactive"
	"ai-gf/internal/repo"
)

func collectTopicKeys(items []repo.ConversationTopic) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		key := strings.TrimSpace(item.TopicKey)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func buildTopicSnapshots(items []repo.ConversationTopic, edges []repo.ConversationTopicEdge) []TopicSnapshot {
	if len(items) == 0 {
		return nil
	}
	related := buildTopicAdjacency(edges)
	out := make([]TopicSnapshot, 0, len(items))
	for _, item := range items {
		out = append(out, TopicSnapshot{
			TopicKey:         strings.TrimSpace(item.TopicKey),
			Label:            strings.TrimSpace(item.TopicLabel),
			Summary:          strings.TrimSpace(item.Summary),
			CallbackHint:     strings.TrimSpace(item.CallbackHint),
			ClusterKey:       strings.TrimSpace(item.ClusterKey),
			AliasTerms:       topicAliasTerms(item),
			RelatedTopicKeys: related[strings.TrimSpace(item.TopicKey)],
			Status:           strings.TrimSpace(item.Status),
			Importance:       clampInt(item.Importance, 1, 5),
			MentionCount:     maxInt(item.MentionCount, 0),
			RecallCount:      maxInt(item.RecallCount, 0),
			LastDiscussedAt:  item.LastDiscussedAt,
			NextRecallAt:     item.NextRecallAt,
			LastRecalledAt:   item.LastRecalledAt,
		})
	}
	return out
}

func buildTopicEdgeSnapshots(edges []repo.ConversationTopicEdge) []TopicEdgeSnapshot {
	if len(edges) == 0 {
		return nil
	}
	out := make([]TopicEdgeSnapshot, 0, len(edges))
	for _, item := range edges {
		out = append(out, TopicEdgeSnapshot{
			FromTopicKey:  strings.TrimSpace(item.FromTopicKey),
			ToTopicKey:    strings.TrimSpace(item.ToTopicKey),
			RelationType:  strings.TrimSpace(item.RelationType),
			Weight:        item.Weight,
			EvidenceCount: item.EvidenceCount,
		})
	}
	return out
}

func formatTopicContext(items []repo.ConversationTopic, edges []repo.ConversationTopicEdge) string {
	if len(items) == 0 {
		return "暂无"
	}
	related := buildTopicAdjacency(edges)
	labelByKey := map[string]string{}
	for _, item := range items {
		labelByKey[strings.TrimSpace(item.TopicKey)] = pickTopicLabelForContext(item)
	}

	limit := len(items)
	if limit > 4 {
		limit = 4
	}
	lines := make([]string, 0, limit)
	for _, item := range items[:limit] {
		label := pickTopicLabelForContext(item)
		if label == "" {
			continue
		}
		prefix := "未完话题"
		if strings.TrimSpace(item.Status) != "active" {
			prefix = "可回钩旧梗"
		}
		summary := strings.TrimSpace(item.Summary)
		if summary == "" {
			summary = "最近聊过这个话题"
		}
		line := fmt.Sprintf("- %s「%s」：%s", prefix, label, summary)
		callback := strings.TrimSpace(item.CallbackHint)
		if callback != "" && callback != label {
			line += "；回钩点：" + callback
		}
		if neighbors := related[strings.TrimSpace(item.TopicKey)]; len(neighbors) > 0 {
			neighborLabels := make([]string, 0, len(neighbors))
			for _, key := range neighbors {
				if neighbor := labelByKey[key]; neighbor != "" && neighbor != label {
					neighborLabels = append(neighborLabels, neighbor)
				}
			}
			if len(neighborLabels) > 0 {
				line += "；关联线程：" + strings.Join(neighborLabels, " / ")
			}
		}
		lines = append(lines, line)
	}
	return joinOrNone(lines)
}

func buildTopicAdjacency(edges []repo.ConversationTopicEdge) map[string][]string {
	if len(edges) == 0 {
		return map[string][]string{}
	}
	adj := map[string][]string{}
	seen := map[string]map[string]struct{}{}
	for _, edge := range edges {
		from := strings.TrimSpace(edge.FromTopicKey)
		to := strings.TrimSpace(edge.ToTopicKey)
		if from == "" || to == "" || from == to {
			continue
		}
		if seen[from] == nil {
			seen[from] = map[string]struct{}{}
		}
		if seen[to] == nil {
			seen[to] = map[string]struct{}{}
		}
		if _, ok := seen[from][to]; !ok {
			seen[from][to] = struct{}{}
			adj[from] = append(adj[from], to)
		}
		if _, ok := seen[to][from]; !ok {
			seen[to][from] = struct{}{}
			adj[to] = append(adj[to], from)
		}
	}
	for key := range adj {
		sort.Strings(adj[key])
	}
	return adj
}

func topicAliasTerms(item repo.ConversationTopic) []string {
	terms := []string{}
	seen := map[string]struct{}{}
	push := func(values ...string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			terms = append(terms, value)
		}
	}
	push(item.TopicLabel, item.CallbackHint)
	if raw, ok := item.Metadata["alias_terms"]; ok {
		switch v := raw.(type) {
		case []any:
			for _, item := range v {
				push(fmt.Sprint(item))
			}
		case []string:
			push(v...)
		case string:
			push(parseCSVList(v)...)
		}
	}
	return terms
}

func pickTopicLabelForContext(item repo.ConversationTopic) string {
	label := strings.TrimSpace(item.TopicLabel)
	if label != "" {
		return label
	}
	return strings.TrimSpace(item.TopicKey)
}

func (s *Service) executeTrackTopic(
	ctx context.Context,
	userID string,
	sessionID string,
	sourceMessageID int64,
	now time.Time,
	params map[string]string,
) error {
	topicKey := strings.TrimSpace(params["topic_key"])
	label := strings.TrimSpace(params["topic_label"])
	if topicKey == "" && label == "" {
		return nil
	}
	if label == "" {
		label = topicKey
	}
	if topicKey == "" {
		topicKey = label
	}
	status := strings.TrimSpace(params["status"])
	if status == "" {
		status = "active"
	}

	var sourceID *int64
	if sourceMessageID > 0 {
		sourceID = &sourceMessageID
	}

	var nextRecallAt *time.Time
	if strings.ToLower(status) != "resolved" {
		offset := parseHourOrDayOffset(strings.TrimSpace(params["offset"]))
		if offset <= 0 {
			offset = defaultTopicRecallOffset(parseTopicImportance(params["importance"]))
		}
		next := now.Add(offset)
		nextRecallAt = &next
	}

	metadata := map[string]any{}
	if aliasTerms := parseCSVList(params["alias_terms"]); len(aliasTerms) > 0 {
		metadata["alias_terms"] = aliasTerms
	}
	if sourceClauses := parseCSVList(params["source_clauses"]); len(sourceClauses) > 0 {
		metadata["source_clauses"] = sourceClauses
	}

	_, err := s.repo.UpsertConversationTopic(ctx, repo.ConversationTopic{
		UserID:          userID,
		SessionID:       sessionID,
		TopicKey:        topicKey,
		TopicLabel:      label,
		Summary:         strings.TrimSpace(params["summary"]),
		CallbackHint:    strings.TrimSpace(params["callback_hint"]),
		ClusterKey:      strings.TrimSpace(params["cluster_key"]),
		Status:          status,
		Importance:      parseTopicImportance(params["importance"]),
		SourceMessageID: sourceID,
		Metadata:        metadata,
		LastDiscussedAt: &now,
		NextRecallAt:    nextRecallAt,
	})
	return err
}

func (s *Service) executeLinkTopics(
	ctx context.Context,
	userID string,
	sessionID string,
	now time.Time,
	params map[string]string,
) error {
	if s.repo == nil {
		return nil
	}
	fromTopicKey := strings.TrimSpace(params["from_topic_key"])
	toTopicKey := strings.TrimSpace(params["to_topic_key"])
	if fromTopicKey == "" || toTopicKey == "" || fromTopicKey == toTopicKey {
		return nil
	}
	weight, err := strconv.ParseFloat(strings.TrimSpace(params["weight"]), 64)
	if err != nil || weight <= 0 {
		weight = 1
	}
	return s.repo.UpsertConversationTopicEdge(ctx, repo.ConversationTopicEdge{
		UserID:       userID,
		SessionID:    sessionID,
		FromTopicKey: fromTopicKey,
		ToTopicKey:   toTopicKey,
		RelationType: strings.TrimSpace(params["relation_type"]),
		Weight:       weight,
		LastLinkedAt: &now,
	})
}

func (s *Service) executeScheduleTopicReengage(
	ctx context.Context,
	userID string,
	sessionID string,
	sourceMessageID int64,
	now time.Time,
	params map[string]string,
	reason string,
) error {
	if s.proactiveScheduler == nil {
		return nil
	}

	topicKey := strings.TrimSpace(params["topic_key"])
	label := strings.TrimSpace(params["topic_label"])
	if topicKey == "" && label == "" {
		return nil
	}
	if label == "" {
		label = topicKey
	}
	if topicKey == "" {
		topicKey = label
	}

	offset := parseHourOrDayOffset(strings.TrimSpace(params["offset"]))
	if offset <= 0 {
		offset = defaultTopicRecallOffset(parseTopicImportance(params["importance"]))
	}
	runAt := now.Add(offset)

	var sourceID *int64
	if sourceMessageID > 0 {
		sourceID = &sourceMessageID
	}
	return s.proactiveScheduler.Schedule(ctx, proactive.ScheduleRequest{
		UserID:          userID,
		SessionID:       sessionID,
		TaskType:        "topic_reengage",
		Reason:          strings.TrimSpace(reason),
		SourceMessageID: sourceID,
		RunAt:           runAt,
		CooldownSeconds: 18 * 60 * 60,
		Payload: map[string]any{
			"topic_key":               topicKey,
			"topic_label":             label,
			"topic_summary":           strings.TrimSpace(params["summary"]),
			"callback_hint":           strings.TrimSpace(params["callback_hint"]),
			"cluster_key":             strings.TrimSpace(params["cluster_key"]),
			"alias_terms":             parseCSVList(params["alias_terms"]),
			"related_topic_keys":      parseCSVList(params["related_topic_keys"]),
			"secondary_topic_key":     strings.TrimSpace(params["secondary_topic_key"]),
			"secondary_topic_label":   strings.TrimSpace(params["secondary_topic_label"]),
			"secondary_callback_hint": strings.TrimSpace(params["secondary_callback_hint"]),
			"secondary_relation_type": strings.TrimSpace(params["secondary_relation_type"]),
			"offset":                  strings.TrimSpace(params["offset"]),
			"source":                  "topic_engine",
			"topic_status":            strings.TrimSpace(params["status"]),
			"topic_importance":        parseTopicImportance(params["importance"]),
		},
	})
}

func parseTopicImportance(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 3
	}
	return clampInt(n, 1, 5)
}

func defaultTopicRecallOffset(importance int) time.Duration {
	if importance >= 4 {
		return 18 * time.Hour
	}
	return 28 * time.Hour
}

func parseCSVList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	replacer := strings.NewReplacer("\n", ",", "，", ",", "；", ",", ";", ",", "|", ",")
	parts := strings.Split(replacer.Replace(raw), ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}
