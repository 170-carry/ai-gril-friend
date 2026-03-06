package conversation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

type topicSeed struct {
	TopicKey      string
	Label         string
	Summary       string
	CallbackHint  string
	ClusterKey    string
	AliasTerms    []string
	Importance    int
	SourceClauses []string
}

type rankedTopic struct {
	topic       TopicSnapshot
	score       float64
	matchReason string
	directMatch bool
}

func (e *Engine) resolveTopicStrategy(ctx context.Context, req ConversationRequest, intent Intent) TopicStrategy {
	text := strings.TrimSpace(req.UserMessage)
	lower := strings.ToLower(text)
	seeds, inferredLinks := inferTopicSeeds(ctx, text, intent, req.MemorySnapshot.ActiveTopics, e.topicSummarizer)
	ranked := rankTopicsForTurn(req.MemorySnapshot.ActiveTopics, req.MemorySnapshot.TopicGraph, seeds, lower, req.Now)

	if len(ranked) > 0 {
		best := ranked[0]
		switch {
		case best.directMatch:
			strategy := topicStrategyFromSnapshot("continue_existing", best.topic, best.matchReason)
			strategy.Related = collectRelatedTopics(best.topic, ranked[1:], seeds, inferredLinks, req.MemorySnapshot.TopicGraph, true)
			strategy.Links = buildStrategyLinks(strategy.TopicKey, strategy.Related, inferredLinks)
			return strategy
		case looksLikeTopicContinuation(lower) && !looksLikeFreshTopic(text, seeds):
			strategy := topicStrategyFromSnapshot("continue_existing", best.topic, "用户在沿着已有线程继续往下聊")
			strategy.Related = collectRelatedTopics(best.topic, ranked[1:], seeds, inferredLinks, req.MemorySnapshot.TopicGraph, true)
			strategy.Links = buildStrategyLinks(strategy.TopicKey, strategy.Related, inferredLinks)
			return strategy
		case isGenericReconnect(lower) && shouldGentleRecall(best.topic, req.Now):
			strategy := topicStrategyFromSnapshot("gentle_recall", best.topic, "当前消息偏寒暄，适合轻轻回钩未完话题")
			strategy.Related = collectRelatedTopics(best.topic, ranked[1:], nil, nil, req.MemorySnapshot.TopicGraph, false)
			return strategy
		}
	}

	if len(seeds) == 0 {
		return TopicStrategy{Mode: "none"}
	}

	primary := topicStrategyFromSeed("open_new", seeds[0], "当前轮出现新的可延续话题")
	primary.Related = collectRelatedSeeds(seeds[0], seeds[1:], inferredLinks)
	primary.Links = buildStrategyLinks(primary.TopicKey, primary.Related, inferredLinks)
	if len(primary.Related) > 0 {
		primary.Reason = "当前消息里同时展开了多条并行话题"
	}
	return primary
}

func resolveTopicQuestion(topic TopicStrategy, lowerUserMessage string) string {
	label := strings.TrimSpace(topic.TopicLabel)
	if label == "" {
		return ""
	}
	switch strings.TrimSpace(topic.Mode) {
	case "gentle_recall":
		if len(topic.Related) > 0 && strings.TrimSpace(topic.Related[0].TopicLabel) != "" {
			return fmt.Sprintf("昨天没讲完的「%s」，还有和「%s」连着那条线，后来怎么样啦？", label, topic.Related[0].TopicLabel)
		}
		return fmt.Sprintf("昨天没讲完的「%s」，后来推进到哪儿啦？", label)
	case "continue_existing":
		if looksLikeTopicContinuation(lowerUserMessage) {
			return fmt.Sprintf("那关于「%s」，你现在最卡的是哪一步？", label)
		}
	}
	return ""
}

func buildTopicActions(req ConversationRequest, intent Intent, topic TopicStrategy) []Action {
	if topic.Mode == "" || topic.Mode == "none" {
		return nil
	}
	if intent == IntentBoundarySafety || intent == IntentMetaProduct {
		return nil
	}

	status := deriveTopicStatus(req.UserMessage)
	importance := deriveTopicImportance(topic, intent, req.UserMessage)
	offset := topicRecallOffset(intent, importance)

	actions := []Action{buildTrackTopicAction(topicReferenceFromStrategy(topic), status, importance, offset)}
	linkSeen := map[string]struct{}{}
	if topic.Mode != "gentle_recall" {
		for _, related := range topic.Related {
			if strings.TrimSpace(related.TopicLabel) == "" {
				continue
			}
			actions = append(actions, buildTrackTopicAction(related, status, deriveReferenceImportance(related, intent), topicRecallOffset(intent, deriveReferenceImportance(related, intent))))
		}
		for _, link := range topic.Links {
			action := buildLinkTopicsAction(link)
			if action.Type == "" {
				continue
			}
			key := renderAction(action)
			if _, ok := linkSeen[key]; ok {
				continue
			}
			linkSeen[key] = struct{}{}
			actions = append(actions, action)
		}
		if len(topic.Links) == 0 {
			for _, related := range topic.Related {
				action := buildLinkTopicsAction(TopicLink{
					FromTopicKey: topic.TopicKey,
					ToTopicKey:   related.TopicKey,
					RelationType: related.RelationType,
					Weight:       relationWeightForType(related.RelationType),
				})
				if action.Type == "" {
					continue
				}
				key := renderAction(action)
				if _, ok := linkSeen[key]; ok {
					continue
				}
				linkSeen[key] = struct{}{}
				actions = append(actions, action)
			}
		}
	}

	if status == "resolved" || shouldStopAsking(req.UserMessage) {
		return dedupActions(actions)
	}

	primarySchedule := Action{
		Type: "SCHEDULE_TOPIC_REENGAGE",
		Params: map[string]string{
			"topic_key":          strings.TrimSpace(topic.TopicKey),
			"topic_label":        strings.TrimSpace(topic.TopicLabel),
			"summary":            strings.TrimSpace(topic.Summary),
			"callback_hint":      strings.TrimSpace(topic.CallbackHint),
			"cluster_key":        strings.TrimSpace(topic.ClusterKey),
			"alias_terms":        joinTopicTerms(topicReferenceFromStrategy(topic).AliasTerms),
			"related_topic_keys": joinTopicKeys(topic.Related),
			"status":             status,
			"importance":         fmt.Sprintf("%d", importance),
			"offset":             offset,
		},
		Reason: "未完话题适合稍后自然回钩",
	}
	if secondary := pickSecondaryRecallTopic(topic.Related); secondary.TopicKey != "" {
		primarySchedule.Params["secondary_topic_key"] = strings.TrimSpace(secondary.TopicKey)
		primarySchedule.Params["secondary_topic_label"] = strings.TrimSpace(secondary.TopicLabel)
		primarySchedule.Params["secondary_callback_hint"] = strings.TrimSpace(secondary.CallbackHint)
		primarySchedule.Params["secondary_relation_type"] = normalizeTopicRelationType(secondary.RelationType)
	}
	actions = append(actions, primarySchedule)
	return dedupActions(actions)
}

func buildTrackTopicAction(ref TopicReference, status string, importance int, offset string) Action {
	return Action{
		Type: "TRACK_TOPIC",
		Params: map[string]string{
			"topic_key":      strings.TrimSpace(ref.TopicKey),
			"topic_label":    strings.TrimSpace(ref.TopicLabel),
			"summary":        strings.TrimSpace(ref.Summary),
			"callback_hint":  strings.TrimSpace(ref.CallbackHint),
			"cluster_key":    strings.TrimSpace(ref.ClusterKey),
			"alias_terms":    joinTopicTerms(ref.AliasTerms),
			"source_clauses": joinTopicTerms(ref.SourceClauses),
			"status":         status,
			"importance":     fmt.Sprintf("%d", importance),
			"offset":         offset,
		},
		Reason: "更新当前对话的话题线程",
	}
}

func buildLinkTopicsAction(link TopicLink) Action {
	fromTopicKey := strings.TrimSpace(link.FromTopicKey)
	toTopicKey := strings.TrimSpace(link.ToTopicKey)
	if fromTopicKey == "" || toTopicKey == "" || fromTopicKey == toTopicKey {
		return Action{}
	}
	relationType := normalizeTopicRelationType(link.RelationType)
	return Action{
		Type: "LINK_TOPICS",
		Params: map[string]string{
			"from_topic_key": fromTopicKey,
			"to_topic_key":   toTopicKey,
			"relation_type":  relationType,
			"weight":         fmt.Sprintf("%.2f", maxFloat(link.Weight, relationWeightForType(relationType))),
		},
		Reason: "记录本轮并行话题之间的关系",
	}
}

func rankTopicsForTurn(topics []TopicSnapshot, graph []TopicEdgeSnapshot, seeds []topicSeed, lowerUserMessage string, now time.Time) []rankedTopic {
	if len(topics) == 0 {
		return nil
	}
	scored := make([]rankedTopic, 0, len(topics))
	directMatches := map[string]struct{}{}
	edgeLookup := buildTopicEdgeLookup(graph)

	for _, item := range topics {
		score := float64(clampInt(item.Importance, 1, 5)) * 4
		reasonParts := []string{}
		direct := false

		if strings.TrimSpace(item.Status) == "active" {
			score += 12
		}
		if item.LastDiscussedAt != nil {
			hours := now.Sub(*item.LastDiscussedAt).Hours()
			switch {
			case hours <= 24:
				score += 8
			case hours <= 72:
				score += 4
			}
		}
		if item.LastRecalledAt != nil && now.Sub(*item.LastRecalledAt) < 10*time.Hour {
			score -= 8
		}
		if len(item.RelatedTopicKeys) > 0 {
			score += float64(minInt(len(item.RelatedTopicKeys), 3))
		}

		matchScore, reason := topicTextMatchScore(item, lowerUserMessage)
		if matchScore > 0 {
			score += matchScore
			reasonParts = append(reasonParts, reason)
			direct = true
		}
		for _, seed := range seeds {
			seedScore, seedReason := topicSeedMatchScore(item, seed)
			if seedScore > 0 {
				score += seedScore
				reasonParts = append(reasonParts, seedReason)
				direct = true
			}
		}
		if direct {
			directMatches[item.TopicKey] = struct{}{}
		}
		if looksLikeTopicContinuation(lowerUserMessage) && strings.TrimSpace(item.Status) == "active" {
			score += 8
		}
		if isGenericReconnect(lowerUserMessage) && shouldGentleRecall(item, now) {
			score += 10
			reasonParts = append(reasonParts, "适合在寒暄开场时轻回钩")
		}
		scored = append(scored, rankedTopic{
			topic:       item,
			score:       score,
			matchReason: strings.Join(dedupStrings(reasonParts), "；"),
			directMatch: direct,
		})
	}

	if len(directMatches) > 0 {
		for i := range scored {
			for _, key := range scored[i].topic.RelatedTopicKeys {
				if _, ok := directMatches[key]; ok {
					relationType := strongestRelationType(scored[i].topic.TopicKey, key, edgeLookup)
					scored[i].score += relationBoostForType(relationType)
					if scored[i].matchReason == "" {
						scored[i].matchReason = graphMatchReason(relationType)
					} else {
						scored[i].matchReason += "；" + graphMatchReason(relationType)
					}
					break
				}
			}
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			left := time.Time{}
			right := time.Time{}
			if scored[i].topic.LastDiscussedAt != nil {
				left = *scored[i].topic.LastDiscussedAt
			}
			if scored[j].topic.LastDiscussedAt != nil {
				right = *scored[j].topic.LastDiscussedAt
			}
			return left.After(right)
		}
		return scored[i].score > scored[j].score
	})
	return scored
}

func topicTextMatchScore(topic TopicSnapshot, lowerUserMessage string) (float64, string) {
	lowerUserMessage = normalizeTopicSeed(lowerUserMessage)
	if lowerUserMessage == "" {
		return 0, ""
	}
	best := 0.0
	reason := ""
	for _, raw := range topicComparableTexts(topic) {
		rawNorm := normalizeTopicSeed(raw)
		if rawNorm == "" {
			continue
		}
		switch {
		case strings.Contains(lowerUserMessage, rawNorm):
			if 20 > best {
				best = 20
				reason = "当前消息直接提到了已有话题或旧梗"
			}
		default:
			score := topicTextSimilarity(lowerUserMessage, rawNorm)
			if score >= 0.78 && 16 > best {
				best = 16
				reason = "当前消息和已有 callback / alias 高相似"
			} else if score >= 0.60 && 10 > best {
				best = 10
				reason = "当前消息和已有话题表达有明显相似"
			}
		}
	}
	return best, reason
}

func topicSeedMatchScore(topic TopicSnapshot, seed topicSeed) (float64, string) {
	if seed.TopicKey == "" && seed.ClusterKey == "" && seed.Label == "" {
		return 0, ""
	}
	switch {
	case strings.TrimSpace(seed.ClusterKey) != "" && strings.TrimSpace(seed.ClusterKey) == strings.TrimSpace(topic.ClusterKey):
		return 18, "当前消息新提到的线程与已有 cluster 相同"
	case strings.TrimSpace(seed.TopicKey) != "" && strings.TrimSpace(seed.TopicKey) == strings.TrimSpace(topic.TopicKey):
		return 16, "当前消息新提到的线程正好是已有 topic"
	}
	for _, alias := range seed.AliasTerms {
		for _, raw := range topicComparableTexts(topic) {
			if topicTextSimilarity(alias, raw) >= 0.72 {
				return 12, "当前消息里的旧梗和已有 callback / alias 可聚类"
			}
		}
	}
	return 0, ""
}

func topicComparableTexts(topic TopicSnapshot) []string {
	out := []string{topic.Label, topic.TopicKey, topic.CallbackHint, topic.Summary, topic.ClusterKey}
	out = append(out, topic.AliasTerms...)
	return out
}

func collectRelatedTopics(primary TopicSnapshot, ranked []rankedTopic, seeds []topicSeed, inferredLinks []TopicLink, graph []TopicEdgeSnapshot, includeSeeds bool) []TopicReference {
	out := make([]TopicReference, 0, 2)
	seen := map[string]struct{}{strings.TrimSpace(primary.TopicKey): {}}
	edgeLookup := buildTopicEdgeLookup(graph)
	for _, item := range ranked {
		if len(out) >= 2 {
			break
		}
		if item.score < 18 {
			continue
		}
		ref := topicReferenceFromSnapshot(item.topic, item.matchReason)
		ref.RelationType = strongestRelationType(primary.TopicKey, ref.TopicKey, edgeLookup)
		if ref.TopicKey == "" || ref.TopicKey == primary.TopicKey {
			continue
		}
		if _, ok := seen[ref.TopicKey]; ok {
			continue
		}
		seen[ref.TopicKey] = struct{}{}
		out = append(out, ref)
	}
	if includeSeeds {
		for _, seed := range seeds {
			if len(out) >= 2 {
				break
			}
			ref := topicReferenceFromSeed(seed, "当前消息还带出了另一条并行线程")
			ref.RelationType = relationTypeForPair(primary.TopicKey, ref.TopicKey, inferredLinks)
			key := strings.TrimSpace(ref.TopicKey)
			if key == "" {
				continue
			}
			if key == strings.TrimSpace(primary.TopicKey) || strings.TrimSpace(ref.ClusterKey) == strings.TrimSpace(primary.ClusterKey) {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ref)
		}
	}
	return out
}

func collectRelatedSeeds(primary topicSeed, seeds []topicSeed, inferredLinks []TopicLink) []TopicReference {
	out := make([]TopicReference, 0, minInt(len(seeds), 2))
	seen := map[string]struct{}{}
	for _, seed := range seeds {
		if len(out) >= 2 {
			break
		}
		if seed.TopicKey == "" {
			continue
		}
		if seed.ClusterKey != "" && seed.ClusterKey == primary.ClusterKey {
			continue
		}
		if _, ok := seen[seed.TopicKey]; ok {
			continue
		}
		seen[seed.TopicKey] = struct{}{}
		ref := topicReferenceFromSeed(seed, "当前消息还包含另一条并行话题")
		ref.RelationType = relationTypeForPair(primary.TopicKey, ref.TopicKey, inferredLinks)
		out = append(out, ref)
	}
	return out
}

func topicStrategyFromSnapshot(mode string, topic TopicSnapshot, reason string) TopicStrategy {
	return TopicStrategy{
		Mode:         mode,
		TopicKey:     strings.TrimSpace(topic.TopicKey),
		TopicLabel:   pickTopicLabel(topic),
		Summary:      strings.TrimSpace(topic.Summary),
		CallbackHint: pickTopicCallback(topic),
		ClusterKey:   strings.TrimSpace(topic.ClusterKey),
		Reason:       reason,
	}
}

func topicStrategyFromSeed(mode string, seed topicSeed, reason string) TopicStrategy {
	ref := topicReferenceFromSeed(seed, reason)
	return TopicStrategy{
		Mode:         mode,
		TopicKey:     ref.TopicKey,
		TopicLabel:   ref.TopicLabel,
		Summary:      ref.Summary,
		CallbackHint: ref.CallbackHint,
		ClusterKey:   ref.ClusterKey,
		Reason:       reason,
	}
}

func topicReferenceFromStrategy(topic TopicStrategy) TopicReference {
	aliasTerms := dedupStrings([]string{topic.TopicLabel, topic.CallbackHint})
	return TopicReference{
		TopicKey:     strings.TrimSpace(topic.TopicKey),
		TopicLabel:   strings.TrimSpace(topic.TopicLabel),
		Summary:      strings.TrimSpace(topic.Summary),
		CallbackHint: strings.TrimSpace(topic.CallbackHint),
		ClusterKey:   strings.TrimSpace(topic.ClusterKey),
		RelationType: "",
		AliasTerms:   aliasTerms,
	}
}

func topicReferenceFromSnapshot(topic TopicSnapshot, reason string) TopicReference {
	return TopicReference{
		TopicKey:     strings.TrimSpace(topic.TopicKey),
		TopicLabel:   pickTopicLabel(topic),
		Summary:      strings.TrimSpace(topic.Summary),
		CallbackHint: pickTopicCallback(topic),
		ClusterKey:   strings.TrimSpace(topic.ClusterKey),
		RelationType: "",
		AliasTerms:   append([]string(nil), topic.AliasTerms...),
		Reason:       reason,
	}
}

func topicReferenceFromSeed(seed topicSeed, reason string) TopicReference {
	return TopicReference{
		TopicKey:      strings.TrimSpace(seed.TopicKey),
		TopicLabel:    strings.TrimSpace(seed.Label),
		Summary:       strings.TrimSpace(seed.Summary),
		CallbackHint:  strings.TrimSpace(seed.CallbackHint),
		ClusterKey:    strings.TrimSpace(seed.ClusterKey),
		RelationType:  "",
		AliasTerms:    append([]string(nil), seed.AliasTerms...),
		SourceClauses: append([]string(nil), seed.SourceClauses...),
		Reason:        reason,
	}
}

func inferTopicSeeds(ctx context.Context, text string, intent Intent, activeTopics []TopicSnapshot, summarizer TopicSummarizer) ([]topicSeed, []TopicLink) {
	ruleSeeds := inferRuleTopicSeeds(text, intent)
	if !shouldUseTopicSummarizer(text) || summarizer == nil {
		return ruleSeeds, inferTopicLinks(text, ruleSeeds)
	}

	result, err := summarizer.Summarize(ctx, TopicSummaryRequest{
		UserMessage:  text,
		Intent:       intent,
		ActiveTopics: activeTopics,
	})
	if err != nil || len(result.Threads) == 0 {
		return ruleSeeds, inferTopicLinks(text, ruleSeeds)
	}

	llmSeeds := topicSeedsFromSummaryResult(result)
	if len(llmSeeds) == 0 {
		return ruleSeeds, inferTopicLinks(text, ruleSeeds)
	}
	merged := mergeTopicSeeds(llmSeeds, ruleSeeds)
	return merged, inferTopicLinks(text, merged)
}

func inferRuleTopicSeeds(text string, intent Intent) []topicSeed {
	text = strings.TrimSpace(text)
	if text == "" || shouldStopAsking(text) || intent == IntentBoundarySafety || intent == IntentMetaProduct {
		return nil
	}
	clauses := splitNarrativeClauses(text)
	if len(clauses) == 0 {
		clauses = []string{text}
	}

	seeds := make([]topicSeed, 0, len(clauses))
	for _, clause := range clauses {
		if seed, ok := inferTopicSeedFromClause(clause, intent); ok {
			seeds = append(seeds, seed)
		}
	}
	if len(seeds) == 0 {
		if seed, ok := inferTopicSeedFromClause(text, intent); ok {
			seeds = append(seeds, seed)
		}
	}
	return dedupTopicSeeds(seeds)
}

func shouldUseTopicSummarizer(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	clauseCount := len(splitNarrativeClauses(text))
	runeCount := len([]rune(text))
	return runeCount >= 48 || clauseCount >= 3
}

func topicSeedsFromSummaryResult(result TopicSummaryResult) []topicSeed {
	if len(result.Threads) == 0 {
		return nil
	}
	out := make([]topicSeed, 0, len(result.Threads))
	for _, thread := range result.Threads {
		label := strings.TrimSpace(thread.Label)
		summary := strings.TrimSpace(thread.Summary)
		if label == "" || summary == "" {
			continue
		}
		aliases := dedupStrings(append(append([]string(nil), thread.AliasTerms...), extractTopicAliasTerms(label, summary, thread.CallbackHint)...))
		clusterKey := buildTopicClusterKey(label, thread.CallbackHint, aliases)
		if clusterKey == "" {
			clusterKey = buildTopicClusterKey(label, summary, aliases)
		}
		out = append(out, topicSeed{
			TopicKey:      normalizeTopicSeed(label),
			Label:         label,
			Summary:       summary,
			CallbackHint:  strings.TrimSpace(thread.CallbackHint),
			ClusterKey:    clusterKey,
			AliasTerms:    aliases,
			Importance:    clampInt(thread.Importance, 1, 5),
			SourceClauses: dedupStrings(thread.SourceClauses),
		})
	}
	return dedupTopicSeeds(out)
}

func mergeTopicSeeds(primary []topicSeed, fallback []topicSeed) []topicSeed {
	if len(primary) == 0 {
		return dedupTopicSeeds(fallback)
	}
	out := append([]topicSeed(nil), primary...)
	for _, seed := range fallback {
		matched := false
		for i := range out {
			if topicSeedsMatch(out[i], seed) {
				out[i] = mergeTopicSeed(out[i], seed)
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, seed)
		}
	}
	return dedupTopicSeeds(out)
}

func topicSeedsMatch(left, right topicSeed) bool {
	switch {
	case left.TopicKey != "" && left.TopicKey == right.TopicKey:
		return true
	case left.ClusterKey != "" && left.ClusterKey == right.ClusterKey:
		return true
	}
	if topicTextSimilarity(left.Summary, right.Summary) >= 0.72 {
		return true
	}
	for _, alias := range left.AliasTerms {
		for _, other := range right.AliasTerms {
			if topicTextSimilarity(alias, other) >= 0.80 {
				return true
			}
		}
	}
	return false
}

func mergeTopicSeed(primary topicSeed, fallback topicSeed) topicSeed {
	if primary.TopicKey == "" {
		primary.TopicKey = fallback.TopicKey
	}
	if primary.Label == "" {
		primary.Label = fallback.Label
	}
	if primary.Summary == "" {
		primary.Summary = fallback.Summary
	}
	if primary.CallbackHint == "" {
		primary.CallbackHint = fallback.CallbackHint
	}
	if primary.ClusterKey == "" {
		primary.ClusterKey = fallback.ClusterKey
	}
	if fallback.Importance > primary.Importance {
		primary.Importance = fallback.Importance
	}
	primary.AliasTerms = dedupStrings(append(primary.AliasTerms, fallback.AliasTerms...))
	primary.SourceClauses = dedupStrings(append(primary.SourceClauses, fallback.SourceClauses...))
	return primary
}

func inferTopicSeedFromClause(clause string, intent Intent) (topicSeed, bool) {
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return topicSeed{}, false
	}

	label, importance := inferTopicLabelFromClause(clause, intent)
	if label == "" {
		return topicSeed{}, false
	}
	callback := extractCallbackHint(clause)
	if callback == "" && importance >= 4 && len([]rune(clause)) <= 18 {
		callback = trimRunes(clause, 16)
	}
	summary := trimRunes(clause, 40)
	aliases := extractTopicAliasTerms(label, summary, callback)
	clusterKey := buildTopicClusterKey(label, callback, aliases)
	return topicSeed{
		TopicKey:      normalizeTopicSeed(label),
		Label:         label,
		Summary:       summary,
		CallbackHint:  callback,
		ClusterKey:    clusterKey,
		AliasTerms:    aliases,
		Importance:    importance,
		SourceClauses: []string{trimRunes(clause, 24)},
	}, true
}

func inferTopicLabelFromClause(clause string, intent Intent) (string, int) {
	if title, _ := extractEventSignal(clause); title != "" {
		if title == "用户提到未来安排" {
			return trimRunes(firstClause(clause), 12), 4
		}
		if strings.Contains(clause, "准备") || strings.Contains(clause, "紧张") {
			return title + "准备", 5
		}
		return title, 4
	}
	lower := strings.ToLower(strings.TrimSpace(clause))
	switch {
	case containsAny(lower, []string{"面试"}):
		return "面试准备", 5
	case containsAny(lower, []string{"考试", "复习"}):
		return "考试准备", 5
	case containsAny(lower, []string{"汇报", "述职", "答辩"}):
		return "工作汇报", 4
	case containsAny(lower, []string{"老板", "同事", "开会", "需求", "方案", "被怼", "被骂", "加班"}):
		if containsAny(lower, []string{"被怼", "被骂", "冲突", "吵", "怼", "骂", "顶嘴"}) {
			return "工作冲突", 5
		}
		return "工作压力", 4
	case containsAny(lower, []string{"前任", "对象", "男朋友", "女朋友", "喜欢的人", "暧昧", "分手", "吵架"}):
		return "感情关系", 4
	case containsAny(lower, []string{"家里", "父母", "妈妈", "爸爸", "亲戚"}):
		return "家庭关系", 4
	case containsAny(lower, []string{"失眠", "睡不着", "早醒", "睡眠"}):
		return "睡眠状态", 4
	case containsAny(lower, []string{"房租", "租房", "搬家", "室友"}):
		return "居住安排", 3
	case containsAny(lower, []string{"工资", "借钱", "存款", "花销", "钱"}):
		return "金钱压力", 4
	case containsAny(lower, []string{"头疼", "胃疼", "发烧", "医院", "不舒服", "身体"}):
		return "身体状态", 4
	case containsAny(lower, []string{"离职", "转岗", "跳槽", "offer"}):
		return "职业选择", 4
	case intent == IntentStorySharing && len([]rune(clause)) >= 8:
		return trimRunes(firstClause(clause), 12), 3
	}
	return "", 0
}

func splitNarrativeClauses(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	replacer := strings.NewReplacer(
		"但是", "。但是",
		"不过", "。不过",
		"然后", "。然后",
		"后来", "。后来",
		"结果", "。结果",
		"同时", "。同时",
		"而且", "。而且",
	)
	text = replacer.Replace(text)
	text = strings.NewReplacer("！", "。", "？", "。", "，", "。", ",", "。", "\n", "。").Replace(text)
	parts := strings.Split(text, "。")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len([]rune(part)) < 4 {
			continue
		}
		out = append(out, part)
	}
	return out
}

func extractTopicAliasTerms(label, summary, callback string) []string {
	out := make([]string, 0, 6)
	seen := map[string]struct{}{}
	push := func(items ...string) {
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	push(label)
	if callback != "" {
		push(callback)
	}
	push(extractQuotedSegments(summary)...)
	for _, phrase := range candidatePhrases(summary) {
		if len([]rune(phrase)) >= 2 && len([]rune(phrase)) <= 8 {
			push(phrase)
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func extractQuotedSegments(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	pairs := [][2]string{{"“", "”"}, {"「", "」"}, {"\"", "\""}}
	out := []string{}
	for _, pair := range pairs {
		start := strings.Index(text, pair[0])
		end := strings.LastIndex(text, pair[1])
		if start >= 0 && end > start {
			out = append(out, strings.TrimSpace(text[start+len(pair[0]):end]))
		}
	}
	return dedupStrings(out)
}

func candidatePhrases(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	replacer := strings.NewReplacer("这个", "", "那个", "", "真的", "", "有点", "", "今天", "", "昨天", "", "就是", "")
	text = replacer.Replace(text)
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '，' || r == '。' || r == ',' || r == ' ' || r == '！' || r == '？'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len([]rune(part)) >= 2 && len([]rune(part)) <= 10 {
			out = append(out, part)
		}
	}
	return out
}

func buildTopicClusterKey(label, callback string, aliasTerms []string) string {
	parts := []string{normalizeTopicSeed(label)}
	if callback != "" {
		parts = append(parts, normalizeTopicSeed(trimRunes(callback, 8)))
	}
	for _, alias := range aliasTerms {
		alias = normalizeTopicSeed(trimRunes(alias, 6))
		if alias == "" {
			continue
		}
		parts = append(parts, alias)
	}
	return strings.Trim(strings.Join(dedupStrings(parts), "_"), "_")
}

func dedupTopicSeeds(seeds []topicSeed) []topicSeed {
	if len(seeds) == 0 {
		return nil
	}
	type entry struct {
		seed topicSeed
		idx  int
	}
	byKey := map[string]entry{}
	for i, seed := range seeds {
		key := strings.TrimSpace(seed.ClusterKey)
		if key == "" {
			key = strings.TrimSpace(seed.TopicKey)
		}
		if key == "" {
			continue
		}
		current, ok := byKey[key]
		if !ok {
			byKey[key] = entry{seed: seed, idx: i}
			continue
		}
		if seed.Importance > current.seed.Importance {
			current.seed.Importance = seed.Importance
			current.seed.Label = seed.Label
		}
		if current.seed.Summary == "" {
			current.seed.Summary = seed.Summary
		}
		if current.seed.CallbackHint == "" {
			current.seed.CallbackHint = seed.CallbackHint
		}
		current.seed.AliasTerms = dedupStrings(append(current.seed.AliasTerms, seed.AliasTerms...))
		current.seed.SourceClauses = dedupStrings(append(current.seed.SourceClauses, seed.SourceClauses...))
		byKey[key] = current
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		left := byKey[keys[i]]
		right := byKey[keys[j]]
		if left.seed.Importance == right.seed.Importance {
			return left.idx < right.idx
		}
		return left.seed.Importance > right.seed.Importance
	})
	out := make([]topicSeed, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key].seed)
	}
	return out
}

func inferTopicLinks(text string, seeds []topicSeed) []TopicLink {
	if len(seeds) < 2 {
		return nil
	}
	out := make([]TopicLink, 0, len(seeds)-1)
	for i := 0; i < len(seeds)-1; i++ {
		from := seeds[i]
		to := seeds[i+1]
		if strings.TrimSpace(from.TopicKey) == "" || strings.TrimSpace(to.TopicKey) == "" || from.TopicKey == to.TopicKey {
			continue
		}
		relationType := inferRelationTypeBetweenSeeds(text, from, to)
		out = append(out, TopicLink{
			FromTopicKey: from.TopicKey,
			ToTopicKey:   to.TopicKey,
			RelationType: relationType,
			Weight:       relationWeightForType(relationType),
		})
	}
	return dedupTopicLinks(out)
}

func inferRelationTypeBetweenSeeds(text string, from topicSeed, to topicSeed) string {
	joined := strings.ToLower(strings.TrimSpace(strings.Join(append(append([]string(nil), from.SourceClauses...), to.SourceClauses...), " ")))
	textLower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case containsAny(joined, []string{"因为", "所以", "结果", "导致", "搞得", "于是"}) || containsAny(textLower, []string{"结果现在", "结果就", "所以现在", "搞得我"}):
		return "cause_effect"
	case containsAny(joined, []string{"后来", "然后", "接着", "之后", "下一步"}):
		return "progression"
	case containsAny(joined, []string{"但是", "不过", "反而", "却"}):
		return "contrast"
	case looksLikeCauseChain(from, to):
		return "cause_effect"
	case containsAny(joined, []string{"同时", "另外", "另一边", "一边", "还有"}):
		return "context"
	default:
		return "co_occurs"
	}
}

func looksLikeCauseChain(from topicSeed, to topicSeed) bool {
	fromLabel := strings.TrimSpace(from.Label)
	toLabel := strings.TrimSpace(to.Label)
	switch {
	case isStressLikeTopic(fromLabel) && containsAny(toLabel, []string{"睡眠状态", "身体状态"}):
		return true
	case fromLabel == "工作压力" && toLabel == "感情关系":
		return false
	}
	return false
}

func isStressLikeTopic(label string) bool {
	return containsAny(strings.TrimSpace(label), []string{
		"工作压力", "工作冲突", "家庭关系", "感情关系", "金钱压力", "职业选择",
	})
}

func dedupTopicLinks(items []TopicLink) []TopicLink {
	if len(items) == 0 {
		return nil
	}
	out := make([]TopicLink, 0, len(items))
	best := map[string]TopicLink{}
	for _, item := range items {
		item.FromTopicKey = strings.TrimSpace(item.FromTopicKey)
		item.ToTopicKey = strings.TrimSpace(item.ToTopicKey)
		item.RelationType = normalizeTopicRelationType(item.RelationType)
		if item.FromTopicKey == "" || item.ToTopicKey == "" || item.FromTopicKey == item.ToTopicKey {
			continue
		}
		key := item.FromTopicKey + "->" + item.ToTopicKey
		if current, ok := best[key]; !ok || item.Weight > current.Weight {
			best[key] = item
		}
	}
	keys := make([]string, 0, len(best))
	for key := range best {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, best[key])
	}
	return out
}

func looksLikeTopicContinuation(lower string) bool {
	return containsAny(lower, []string{
		"那个", "这件事", "昨天那个", "上次那个", "后来", "然后", "结果", "进展", "还没", "继续", "怎么办",
	})
}

func isGenericReconnect(lower string) bool {
	if lower == "" {
		return false
	}
	if len([]rune(lower)) <= 4 {
		return true
	}
	return containsAny(lower, []string{
		"在吗", "想你", "早安", "晚安", "下班了", "刚到家", "哈哈", "抱抱", "摸摸", "聊聊", "今天回来啦",
	})
}

func shouldGentleRecall(topic TopicSnapshot, now time.Time) bool {
	if strings.TrimSpace(topic.Status) != "active" {
		return false
	}
	if topic.NextRecallAt != nil && !topic.NextRecallAt.IsZero() {
		return !now.Before(*topic.NextRecallAt)
	}
	if topic.LastDiscussedAt == nil {
		return false
	}
	hours := now.Sub(*topic.LastDiscussedAt).Hours()
	return hours >= 12 && hours <= 96
}

func looksLikeFreshTopic(text string, seeds []topicSeed) bool {
	return len(seeds) > 0 && !isGenericReconnect(strings.ToLower(strings.TrimSpace(text)))
}

func extractCallbackHint(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	if !containsAny(lower, []string{"哈哈", "笑死", "梗", "你还记得", "又来了", "这个破", "离谱"}) {
		return ""
	}
	return trimRunes(firstClause(text), 16)
}

func deriveTopicStatus(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	if containsAny(lower, []string{"搞定了", "解决了", "没事了", "好多了", "已经好了", "我去做了"}) {
		return "resolved"
	}
	return "active"
}

func deriveTopicImportance(topic TopicStrategy, intent Intent, userMessage string) int {
	if strings.Contains(topic.TopicLabel, "面试") || strings.Contains(topic.TopicLabel, "考试") {
		return 5
	}
	switch intent {
	case IntentPlanningEvent, IntentAdviceSolving:
		return 4
	case IntentEmotionalSupport:
		return 4
	case IntentStorySharing, IntentRelationship:
		return 3
	default:
		if isGenericReconnect(strings.ToLower(strings.TrimSpace(userMessage))) {
			return 2
		}
		return 3
	}
}

func deriveReferenceImportance(ref TopicReference, intent Intent) int {
	if strings.Contains(ref.TopicLabel, "面试") || strings.Contains(ref.TopicLabel, "考试") {
		return 5
	}
	switch intent {
	case IntentPlanningEvent, IntentAdviceSolving, IntentEmotionalSupport:
		return 4
	default:
		return 3
	}
}

func topicRecallOffset(intent Intent, importance int) string {
	switch {
	case importance >= 5:
		return "18h"
	case intent == IntentEmotionalSupport:
		return "20h"
	case intent == IntentPlanningEvent || intent == IntentAdviceSolving:
		return "24h"
	default:
		return "30h"
	}
}

func buildStrategyLinks(primaryKey string, related []TopicReference, inferred []TopicLink) []TopicLink {
	primaryKey = strings.TrimSpace(primaryKey)
	if primaryKey == "" {
		return nil
	}
	out := make([]TopicLink, 0, len(related))
	for _, link := range inferred {
		if strings.TrimSpace(link.FromTopicKey) == primaryKey || strings.TrimSpace(link.ToTopicKey) == primaryKey {
			out = append(out, link)
		}
	}
	for _, ref := range related {
		key := strings.TrimSpace(ref.TopicKey)
		if key == "" || key == primaryKey || hasTopicLinkBetween(out, primaryKey, key) {
			continue
		}
		out = append(out, TopicLink{
			FromTopicKey: primaryKey,
			ToTopicKey:   key,
			RelationType: normalizeTopicRelationType(ref.RelationType),
			Weight:       relationWeightForType(ref.RelationType),
		})
	}
	return dedupTopicLinks(out)
}

func hasTopicLinkBetween(items []TopicLink, leftKey, rightKey string) bool {
	leftKey = strings.TrimSpace(leftKey)
	rightKey = strings.TrimSpace(rightKey)
	for _, item := range items {
		from := strings.TrimSpace(item.FromTopicKey)
		to := strings.TrimSpace(item.ToTopicKey)
		if (from == leftKey && to == rightKey) || (from == rightKey && to == leftKey) {
			return true
		}
	}
	return false
}

func relationTypeForPair(leftKey, rightKey string, links []TopicLink) string {
	leftKey = strings.TrimSpace(leftKey)
	rightKey = strings.TrimSpace(rightKey)
	for _, link := range links {
		from := strings.TrimSpace(link.FromTopicKey)
		to := strings.TrimSpace(link.ToTopicKey)
		if (from == leftKey && to == rightKey) || (from == rightKey && to == leftKey) {
			return normalizeTopicRelationType(link.RelationType)
		}
	}
	return "co_occurs"
}

func normalizeTopicRelationType(relationType string) string {
	switch strings.TrimSpace(relationType) {
	case "cause_effect":
		return "cause_effect"
	case "progression":
		return "progression"
	case "contrast":
		return "contrast"
	case "context":
		return "context"
	default:
		return "co_occurs"
	}
}

func relationWeightForType(relationType string) float64 {
	switch normalizeTopicRelationType(relationType) {
	case "cause_effect":
		return 1.35
	case "progression":
		return 1.25
	case "contrast":
		return 1.15
	case "context":
		return 1.10
	default:
		return 1.00
	}
}

func relationBoostForType(relationType string) float64 {
	switch normalizeTopicRelationType(relationType) {
	case "cause_effect":
		return 8
	case "progression":
		return 7
	case "contrast":
		return 6
	case "context":
		return 6
	default:
		return 4
	}
}

func graphMatchReason(relationType string) string {
	switch normalizeTopicRelationType(relationType) {
	case "cause_effect":
		return "与当前命中的并行话题存在因果链"
	case "progression":
		return "与当前命中的并行话题处于同一推进链"
	case "contrast":
		return "与当前命中的并行话题形成对照变化"
	case "context":
		return "与当前命中的并行话题属于同一上下文"
	default:
		return "与当前命中的并行话题在 topic graph 中相连"
	}
}

func buildTopicEdgeLookup(edges []TopicEdgeSnapshot) map[string]TopicEdgeSnapshot {
	if len(edges) == 0 {
		return map[string]TopicEdgeSnapshot{}
	}
	out := map[string]TopicEdgeSnapshot{}
	for _, edge := range edges {
		from := strings.TrimSpace(edge.FromTopicKey)
		to := strings.TrimSpace(edge.ToTopicKey)
		if from == "" || to == "" || from == to {
			continue
		}
		edge.RelationType = normalizeTopicRelationType(edge.RelationType)
		key := from + "->" + to
		if current, ok := out[key]; !ok || edge.Weight > current.Weight {
			out[key] = edge
		}
		rev := TopicEdgeSnapshot{
			FromTopicKey:  to,
			ToTopicKey:    from,
			RelationType:  edge.RelationType,
			Weight:        edge.Weight,
			EvidenceCount: edge.EvidenceCount,
		}
		revKey := to + "->" + from
		if current, ok := out[revKey]; !ok || rev.Weight > current.Weight {
			out[revKey] = rev
		}
	}
	return out
}

func strongestRelationType(leftKey, rightKey string, lookup map[string]TopicEdgeSnapshot) string {
	leftKey = strings.TrimSpace(leftKey)
	rightKey = strings.TrimSpace(rightKey)
	if leftKey == "" || rightKey == "" {
		return "co_occurs"
	}
	if edge, ok := lookup[leftKey+"->"+rightKey]; ok {
		return normalizeTopicRelationType(edge.RelationType)
	}
	return "co_occurs"
}

func pickTopicLabel(topic TopicSnapshot) string {
	label := strings.TrimSpace(topic.Label)
	if label != "" {
		return label
	}
	return strings.TrimSpace(topic.TopicKey)
}

func pickTopicCallback(topic TopicSnapshot) string {
	if hint := strings.TrimSpace(topic.CallbackHint); hint != "" {
		return hint
	}
	for _, alias := range topic.AliasTerms {
		if alias = strings.TrimSpace(alias); alias != "" {
			return alias
		}
	}
	return strings.TrimSpace(topic.Summary)
}

func pickSecondaryRecallTopic(items []TopicReference) TopicReference {
	for _, item := range items {
		if strings.TrimSpace(item.TopicKey) == "" {
			continue
		}
		switch normalizeTopicRelationType(item.RelationType) {
		case "cause_effect", "progression", "context":
			return item
		}
	}
	return TopicReference{}
}

func firstClause(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	seps := []string{"。", "！", "？", "，", ",", "\n"}
	cut := len([]rune(text))
	for _, sep := range seps {
		if idx := strings.Index(text, sep); idx >= 0 {
			r := []rune(text[:idx])
			if len(r) < cut {
				cut = len(r)
			}
		}
	}
	runes := []rune(text)
	if cut > len(runes) {
		cut = len(runes)
	}
	return strings.TrimSpace(string(runes[:cut]))
}

func normalizeTopicSeed(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer(
		" ", "",
		"\n", "",
		"\t", "",
		"，", "",
		"。", "",
		"、", "",
		",", "",
		".", "",
		"？", "",
		"?", "",
		"！", "",
		"!", "",
		"：", "",
		":", "",
		"；", "",
		";", "",
		"“", "",
		"”", "",
		"「", "",
		"」", "",
		"（", "",
		"）", "",
		"(", "",
		")", "",
	)
	return replacer.Replace(text)
}

func topicTextSimilarity(a, b string) float64 {
	a = normalizeComparableTopicText(a)
	b = normalizeComparableTopicText(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	left := buildTopicBigrams(a)
	right := buildTopicBigrams(b)
	leftCount, rightCount := 0, 0
	for _, count := range left {
		leftCount += count
	}
	for _, count := range right {
		rightCount += count
	}
	if leftCount == 0 || rightCount == 0 {
		return 0
	}
	intersection := 0
	for gram, count := range left {
		if other, ok := right[gram]; ok {
			if count < other {
				intersection += count
			} else {
				intersection += other
			}
		}
	}
	return float64(2*intersection) / float64(leftCount+rightCount)
}

func normalizeComparableTopicText(in string) string {
	in = strings.TrimSpace(strings.ToLower(in))
	if in == "" {
		return ""
	}
	var out []rune
	for _, r := range in {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.Is(unicode.Han, r):
			out = append(out, r)
		}
	}
	return string(out)
}

func buildTopicBigrams(in string) map[string]int {
	runes := []rune(in)
	if len(runes) < 2 {
		if len(runes) == 1 {
			return map[string]int{string(runes): 1}
		}
		return map[string]int{}
	}
	out := make(map[string]int, len(runes)-1)
	for i := 0; i < len(runes)-1; i++ {
		out[string(runes[i:i+2])]++
	}
	return out
}

func joinTopicTerms(items []string) string {
	items = dedupStrings(items)
	return strings.Join(items, "|")
}

func joinTopicKeys(items []TopicReference) string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		if key := strings.TrimSpace(item.TopicKey); key != "" {
			keys = append(keys, key)
		}
	}
	return joinTopicTerms(keys)
}

func dedupStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
