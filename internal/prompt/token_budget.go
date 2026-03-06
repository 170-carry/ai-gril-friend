package prompt

import (
	"context"
	"sort"
	"strings"
)

// defaultTokenBudgeter 以确定性分桶策略执行预算与降级。
// 核心规则：hard block 属于语义硬约束，始终优先保留。
type defaultTokenBudgeter struct{}

func newDefaultTokenBudgeter() TokenBudgeter {
	return &defaultTokenBudgeter{}
}

// Fit 分四步完成预算拟合：
// 1) 先保留 hard/user 基础开销。
// 2) 将剩余软预算按 system/memory/history 比例拆分。
// 3) 按桶执行结构化降级（避免 RAG/profile 半截语义）。
// 4) 在最终硬上限下再做一轮同策略收敛。
func (b *defaultTokenBudgeter) Fit(ctx context.Context, req BuildRequest, blocks []PromptBlock, trace *BuildTrace) []PromptBlock {
	cfg := req.Budget.Normalize()
	userTokens := countBucketTokens(blocks, BucketUser)
	ragRequestedFromBlocks := countRAGRequestedK(blocks)
	if trace.RAGStats.RequestedK < ragRequestedFromBlocks {
		trace.RAGStats.RequestedK = ragRequestedFromBlocks
	}

	promptCap := cfg.MaxPromptTokens - cfg.ReserveTokens
	if promptCap < 256 {
		promptCap = cfg.MaxPromptTokens
	}
	if promptCap < 128 {
		promptCap = 128
	}

	hardBlocks, softBlocks, stmBlocks, userBlocks := partitionBudgetBlocks(blocks)
	hardTokens := countBlocksTokens(hardBlocks)

	nonUserCap := promptCap - userTokens
	if nonUserCap < 0 {
		nonUserCap = 0
	}
	softCapRaw := nonUserCap - hardTokens
	if softCapRaw < 0 {
		softCapRaw = 0
	}

	// 估算器安全边际：为 emoji/中英混排/代码块等偏差预留余量。
	safetyMargin := computeSafetyMargin(promptCap, softCapRaw)
	softCap := softCapRaw
	if softCap > 0 {
		if safetyMargin > softCap/2 {
			safetyMargin = softCap / 2
		}
		softCap -= safetyMargin
		softCap = softCap * 9 / 10 // 接近上限时使用保守目标，减少超限波动
	}
	if softCap < 0 {
		softCap = 0
	}
	if softCapRaw > 0 && softCap == 0 {
		softCap = maxInt(32, softCapRaw/4)
	}

	systemBudget, memoryBudget, stmBudget := splitBudgets(softCap, cfg)
	profileBudget, ragBudget, summaryBudget := splitMemoryBudgets(memoryBudget)
	emotionBudget := systemBudget

	budgets := map[BudgetBucket]int{
		BucketHard:    hardTokens,
		BucketProfile: profileBudget,
		BucketRAG:     ragBudget,
		BucketEmotion: emotionBudget,
		BucketSummary: summaryBudget,
		BucketSTM:     stmBudget,
		BucketUser:    userTokens,
	}
	trace.BucketBudget = budgets
	if safetyMargin > 0 {
		trace.TrimLogs = append(trace.TrimLogs, "token safety margin applied: "+itoaSafe(safetyMargin))
	}

	kept := make([]PromptBlock, 0, len(blocks))
	used := map[BudgetBucket]int{}

	// hard block 是语义硬约束，独立于比例预算，先行保留。
	for _, block := range hardBlocks {
		kept = append(kept, block)
		used[block.Bucket] += block.TokensEst
		trace.markStage("budget", block, true, "budget keep(hard attribute)")
	}

	// 对软约束桶执行结构化降级链。
	for _, block := range softBlocks {
		limit := budgets[block.Bucket]
		remain := limit - used[block.Bucket]
		fitted, ok, reason := fitSoftBlock(block, remain)
		if ok {
			kept = append(kept, fitted)
			used[block.Bucket] += fitted.TokensEst
			trace.markStage("budget", fitted, true, reason)
			recordRAGDegrade(trace, block, fitted)
			if fitted.TokensEst < block.TokensEst {
				trace.TrimLogs = append(trace.TrimLogs, "degraded "+block.ID+" in "+string(block.Bucket)+": "+reason)
			}
			continue
		}

		trace.markStage("budget", block, false, reason)
		trace.TrimLogs = append(trace.TrimLogs, "dropped "+block.ID+" in "+string(block.Bucket)+": "+reason)
		recordRAGDrop(trace, block)
	}

	// 历史对话优先保留最近轮次，旧轮次压缩到独立 summary 桶。
	keptSTM, droppedSTM := fitSTMBlocks(stmBlocks, budgets[BucketSTM])
	used[BucketSTM] += countBlocksTokens(keptSTM)
	kept = append(kept, keptSTM...)
	for _, block := range keptSTM {
		trace.markStage("budget", block, true, "budget keep(stm)")
	}
	for _, block := range droppedSTM {
		trace.markStage("budget", block, false, "budget drop(stm)")
		trace.TrimLogs = append(trace.TrimLogs, "dropped "+block.ID+" in "+string(block.Bucket)+": budget drop(stm)")
	}

	if len(droppedSTM) > 0 && budgets[BucketSummary] > 0 {
		summary := summarizeSTM(droppedSTM, budgets[BucketSummary])
		if summary != "" {
			summaryBlock := PromptBlock{
				ID:         "stm_summary",
				Priority:   95,
				Kind:       MessageKindSystem,
				Bucket:     BucketSummary,
				Content:    summary,
				TokensEst:  estimateTextTokens(summary),
				Hard:       false,
				Redactable: true,
			}
			remain := budgets[BucketSummary] - used[BucketSummary]
			fitted, ok, reason := fitSummaryBlock(summaryBlock, remain)
			if ok {
				kept = append(kept, fitted)
				used[BucketSummary] += fitted.TokensEst
				trace.markStage("budget", fitted, true, "budget add(stm_summary): "+reason)
				trace.TrimLogs = append(trace.TrimLogs, "added stm summary after history trimming")
			} else {
				trace.markStage("budget", summaryBlock, false, "summary drop: "+reason)
				trace.TrimLogs = append(trace.TrimLogs, "dropped stm_summary: "+reason)
			}
		}
	}

	sortBlocksStable(kept)

	// 非 user 内容拟合完成后，再拼接当前用户消息。
	sortBlocksStable(userBlocks)
	kept = append(kept, userBlocks...)
	sortBlocksStable(kept)

	// 最终硬上限检查：仅使用结构化降级，不做危险字符截断。
	if promptTokensFromBlocks(kept) > promptCap {
		kept = hardCapPrompt(kept, promptCap, trace)
		sortBlocksStable(kept)
	}

	trace.BucketUsed = map[BudgetBucket]int{
		BucketHard:    countBucketTokens(kept, BucketHard),
		BucketProfile: countBucketTokens(kept, BucketProfile),
		BucketRAG:     countBucketTokens(kept, BucketRAG),
		BucketEmotion: countBucketTokens(kept, BucketEmotion),
		BucketSummary: countBucketTokens(kept, BucketSummary),
		BucketSTM:     countBucketTokens(kept, BucketSTM),
		BucketUser:    countBucketTokens(kept, BucketUser),
	}

	finalRAGK := countRAGFinalK(kept)
	trace.RAGStats.KeptK = finalRAGK
	if trace.RAGStats.RequestedK > finalRAGK {
		trace.RAGStats.DroppedK = trace.RAGStats.RequestedK - finalRAGK
	} else {
		trace.RAGStats.DroppedK = 0
	}
	if trace.RAGStats.DroppedK > 0 {
		trace.TrimLogs = append(trace.TrimLogs, ragKLog(trace.RAGStats.RequestedK, trace.RAGStats.KeptK))
	}

	return kept
}

// partitionBudgetBlocks 按 hard/soft/stm/user 维度拆分 block。
func partitionBudgetBlocks(blocks []PromptBlock) (hard []PromptBlock, soft []PromptBlock, stm []PromptBlock, user []PromptBlock) {
	hard = make([]PromptBlock, 0, len(blocks))
	soft = make([]PromptBlock, 0, len(blocks))
	stm = make([]PromptBlock, 0, len(blocks))
	user = make([]PromptBlock, 0, len(blocks))
	for _, block := range blocks {
		switch {
		case block.Bucket == BucketUser:
			user = append(user, block)
		case block.Bucket == BucketSTM:
			stm = append(stm, block)
		case block.Hard:
			hard = append(hard, block)
		default:
			soft = append(soft, block)
		}
	}
	return hard, soft, stm, user
}

// computeSafetyMargin 根据上下文规模给估算器预留安全边际。
func computeSafetyMargin(promptCap int, softCapRaw int) int {
	margin := 200
	switch {
	case promptCap >= 5000:
		margin = 400
	case promptCap >= 3000:
		margin = 320
	}
	if softCapRaw <= 0 {
		return 0
	}
	if softCapRaw < margin {
		margin = softCapRaw / 3
	}
	if margin < 32 {
		margin = 32
	}
	return margin
}

// splitMemoryBudgets 将 memory 预算拆分为 profile/rag/summary 三个子桶。
func splitMemoryBudgets(memoryBudget int) (profileBudget, ragBudget, summaryBudget int) {
	if memoryBudget <= 0 {
		return 0, 0, 0
	}
	// 独立 summary 桶，避免 STM 摘要与 profile/rag 直接抢预算。
	summaryBudget = maxInt(80, memoryBudget/10)
	summaryCap := maxInt(32, memoryBudget/2)
	if summaryBudget > summaryCap {
		summaryBudget = summaryCap
	}
	if summaryBudget > memoryBudget {
		summaryBudget = memoryBudget
	}

	remain := memoryBudget - summaryBudget
	if remain < 0 {
		remain = 0
	}
	profileBudget = remain * 55 / 100
	ragBudget = remain - profileBudget
	return profileBudget, ragBudget, summaryBudget
}

// fitSoftBlock 根据桶类型执行结构化降级，返回是否可保留。
func fitSoftBlock(block PromptBlock, remain int) (PromptBlock, bool, string) {
	if remain <= 0 {
		return PromptBlock{}, false, "bucket exhausted"
	}
	if block.TokensEst <= remain {
		return block, true, "budget keep"
	}

	switch block.Bucket {
	case BucketProfile:
		return fitProfileBlock(block, remain)
	case BucketRAG:
		return fitRAGBlock(block, remain)
	case BucketEmotion:
		return fitEmotionBlock(block, remain)
	case BucketSummary:
		return fitSummaryBlock(block, remain)
	case BucketSTM:
		return fitSTMBlock(block, remain)
	default:
		if canTrimAsPlainText(block) {
			return fitPlainTextBlock(block, remain)
		}
		return PromptBlock{}, false, "drop(non-structured block)"
	}
}

// fitProfileBlock 对 profile 类 block 按候选链路降级拟合。
func fitProfileBlock(block PromptBlock, remain int) (PromptBlock, bool, string) {
	for _, c := range profileCandidates(block) {
		if c.TokensEst <= remain {
			return c, true, "profile degrade"
		}
	}
	return PromptBlock{}, false, "profile drop(after structured degrade)"
}

// profileCandidates 构建 profile 的多级降级候选（topN/压缩/核心字段）。
func profileCandidates(block PromptBlock) []PromptBlock {
	candidates := make([]PromptBlock, 0, 8)
	seen := map[string]struct{}{}
	add := func(content string) {
		content = strings.TrimSpace(content)
		if content == "" {
			return
		}
		if _, ok := seen[content]; ok {
			return
		}
		seen[content] = struct{}{}
		nb := block
		nb.Content = content
		nb.TokensEst = estimateTextTokens(content)
		candidates = append(candidates, nb)
	}

	add(block.Content)
	header, items := parseSectionItems(block.Content)
	if len(items) == 0 {
		return candidates
	}

	for _, n := range profileTopNForBlock(block.ID, len(items)) {
		add(composeSection(header, items[:n]))
	}

	compacted := compactItems(items, 18)
	for _, n := range []int{2, 1} {
		if n > len(compacted) {
			continue
		}
		add(composeSection(header, compacted[:n]))
	}

	if block.ID == "profile" {
		core := extractProfileCore(items)
		if len(core) > 0 {
			add(composeSection(header, core))
		}
	}

	return candidates
}

// profileTopNForBlock 返回不同 profile 子类型的 topN 降级序列。
func profileTopNForBlock(blockID string, count int) []int {
	base := []int{3, 2, 1}
	switch blockID {
	case "profile":
		base = []int{4, 2, 1}
	case "events":
		base = []int{3, 2, 1}
	case "preferences":
		base = []int{3, 2, 1}
	}
	out := make([]int, 0, len(base))
	seen := map[int]struct{}{}
	for _, n := range base {
		if n <= 0 || n > count {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 && count > 0 {
		out = append(out, 1)
	}
	return out
}

// extractProfileCore 从 profile 条目中抽取最核心字段（称呼/时区等）。
func extractProfileCore(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	keywords := []string{"昵称", "称呼", "时区", "timezone", "name"}
	out := make([]string, 0, 3)
	for _, item := range items {
		lower := strings.ToLower(item)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				out = append(out, trimTextToSentenceTokens(item, 18))
				break
			}
		}
		if len(out) >= 3 {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, trimTextToSentenceTokens(items[0], 18))
	}
	return out
}

// fitRAGBlock 对 RAG block 按 K 递减和紧凑化候选拟合。
func fitRAGBlock(block PromptBlock, remain int) (PromptBlock, bool, string) {
	for _, c := range ragCandidates(block) {
		if c.TokensEst <= remain {
			if ragItemCount(c.Content) < ragItemCount(block.Content) {
				return c, true, "rag degrade(k)"
			}
			if c.TokensEst < block.TokensEst {
				return c, true, "rag degrade(compact)"
			}
			return c, true, "budget keep"
		}
	}
	return PromptBlock{}, false, "rag drop(after K degrade)"
}

// ragCandidates 构建 RAG 候选链路（原文 -> K 降级 -> 紧凑句）。
func ragCandidates(block PromptBlock) []PromptBlock {
	candidates := make([]PromptBlock, 0, 12)
	seen := map[string]struct{}{}
	add := func(content string) {
		content = strings.TrimSpace(content)
		if content == "" {
			return
		}
		if _, ok := seen[content]; ok {
			return
		}
		seen[content] = struct{}{}
		nb := block
		nb.Content = content
		nb.TokensEst = estimateTextTokens(content)
		candidates = append(candidates, nb)
	}

	add(block.Content)
	header, entries := parseRAGItems(block.Content)
	if len(entries) == 0 {
		return candidates
	}

	levels := uniquePositiveInts([]int{
		len(entries),
		minInt(5, len(entries)),
		minInt(3, len(entries)),
		minInt(2, len(entries)),
		1,
	})
	for _, k := range levels {
		add(composeSection(header, entries[:k]))
	}

	compactedEntries := compactItems(entries, 20)
	for _, k := range uniquePositiveInts([]int{minInt(3, len(compactedEntries)), minInt(2, len(compactedEntries)), 1}) {
		add(composeSection(header, compactedEntries[:k]))
	}
	return candidates
}

// fitEmotionBlock 在预算不足时优先退化为短模板，否则丢弃。
func fitEmotionBlock(block PromptBlock, remain int) (PromptBlock, bool, string) {
	short := buildCompactEmotionBlock(block)
	if short.TokensEst <= remain {
		return short, true, "emotion degrade(short_template)"
	}
	return PromptBlock{}, false, "emotion drop(low priority)"
}

// buildCompactEmotionBlock 生成极短情绪指引模板。
func buildCompactEmotionBlock(block PromptBlock) PromptBlock {
	emotion := extractEmotionName(block.Content)
	if emotion == "" {
		emotion = "current"
	}
	content := "EMOTION_GUIDE\n- " + emotion + ": 先共情，再问一个小问题。"
	nb := block
	nb.Content = content
	nb.TokensEst = estimateTextTokens(content)
	return nb
}

// fitSummaryBlock 控制摘要 block 的压缩与保留策略。
func fitSummaryBlock(block PromptBlock, remain int) (PromptBlock, bool, string) {
	if remain <= 0 {
		return PromptBlock{}, false, "summary budget exhausted"
	}
	if block.TokensEst <= remain {
		return block, true, "budget keep"
	}

	lines := strings.Split(block.Content, "\n")
	if len(lines) > 3 {
		compacted := strings.Join(lines[:3], "\n")
		nb := block
		nb.Content = compacted
		nb.TokensEst = estimateTextTokens(compacted)
		if nb.TokensEst <= remain {
			return nb, true, "summary degrade(compact_lines)"
		}
	}

	trimmed := trimTextToSentenceTokens(block.Content, remain)
	if trimmed != "" {
		nb := block
		nb.Content = trimmed
		nb.TokensEst = estimateTextTokens(trimmed)
		if nb.TokensEst <= remain {
			return nb, true, "summary degrade(sentence_trim)"
		}
	}
	return PromptBlock{}, false, "summary drop(after degrade)"
}

// fitSTMBlock 允许最近历史在 hard-cap 阶段做句级压缩，而不是直接整条丢掉。
// 这样既能保住最近上下文锚点，也能给摘要块留出空间。
func fitSTMBlock(block PromptBlock, remain int) (PromptBlock, bool, string) {
	if remain <= 0 {
		return PromptBlock{}, false, "stm budget exhausted"
	}
	if block.TokensEst <= remain {
		return block, true, "budget keep"
	}

	trimmed := trimTextToSentenceTokens(block.Content, remain)
	if strings.TrimSpace(trimmed) == "" {
		return fitSTMBlockWithFallback(block, remain, "stm trim failed")
	}

	nb := block
	nb.Content = trimmed
	nb.TokensEst = estimateTextTokens(trimmed)
	if nb.TokensEst > remain {
		return fitSTMBlockWithFallback(block, remain, "stm trim overflow")
	}
	return nb, true, "stm degrade(sentence_trim)"
}

// fitSTMBlockWithFallback 在句级裁剪不够精确时，继续用 token 级裁剪保住最小上下文锚点。
func fitSTMBlockWithFallback(block PromptBlock, remain int, failure string) (PromptBlock, bool, string) {
	for budget := remain; budget >= 1; budget-- {
		trimmed := trimTextToTokens(block.Content, budget)
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		if estimateTextTokens(trimmed) > remain {
			continue
		}

		nb := block
		nb.Content = trimmed
		nb.TokensEst = estimateTextTokens(trimmed)
		return nb, true, "stm degrade(token_trim)"
	}
	return PromptBlock{}, false, failure
}

// canTrimAsPlainText 仅允许通用说明类 block 走文本截断。
func canTrimAsPlainText(block PromptBlock) bool {
	if !block.Redactable {
		return false
	}
	// 仅允许通用解释类规则做文本截断，避免业务语义被截坏。
	return strings.HasPrefix(block.ID, "rule_") || strings.HasPrefix(block.ID, "note_")
}

// fitPlainTextBlock 对可截断文本执行句级截断拟合。
func fitPlainTextBlock(block PromptBlock, remain int) (PromptBlock, bool, string) {
	trimmed := trimTextToSentenceTokens(block.Content, remain)
	if strings.TrimSpace(trimmed) == "" {
		return PromptBlock{}, false, "plain text trim failed"
	}
	nb := block
	nb.Content = trimmed
	nb.TokensEst = estimateTextTokens(trimmed)
	if nb.TokensEst > remain {
		return PromptBlock{}, false, "plain text trim overflow"
	}
	return nb, true, "plain text trim"
}

// fitSTMBlocks 在历史预算内优先保留最新对话，必要时保底保留最后一条。
func fitSTMBlocks(stmBlocks []PromptBlock, budget int) ([]PromptBlock, []PromptBlock) {
	if len(stmBlocks) == 0 {
		return nil, nil
	}
	if budget <= 0 {
		// 即使预算为 0，也保底保留最后一条，避免上下文瞬间断裂。
		sort.SliceStable(stmBlocks, func(i, j int) bool {
			return getSeq(stmBlocks[i]) < getSeq(stmBlocks[j])
		})
		latest := stmBlocks[len(stmBlocks)-1]
		dropped := append([]PromptBlock{}, stmBlocks[:len(stmBlocks)-1]...)
		return []PromptBlock{latest}, dropped
	}

	sort.SliceStable(stmBlocks, func(i, j int) bool {
		return getSeq(stmBlocks[i]) < getSeq(stmBlocks[j])
	})

	selected := make([]bool, len(stmBlocks))
	used := 0
	for i := len(stmBlocks) - 1; i >= 0; i-- {
		tokens := stmBlocks[i].TokensEst
		if used+tokens > budget {
			continue
		}
		selected[i] = true
		used += tokens
	}

	kept := make([]PromptBlock, 0, len(stmBlocks))
	dropped := make([]PromptBlock, 0, len(stmBlocks))
	for i := range stmBlocks {
		if selected[i] {
			kept = append(kept, stmBlocks[i])
		} else {
			dropped = append(dropped, stmBlocks[i])
		}
	}
	if len(kept) == 0 && len(stmBlocks) > 0 {
		latest := stmBlocks[len(stmBlocks)-1]
		kept = append(kept, latest)
		dropped = dropped[:0]
		for i := 0; i < len(stmBlocks)-1; i++ {
			dropped = append(dropped, stmBlocks[i])
		}
	}
	return kept, dropped
}

// hardCapPrompt 在最终硬上限下继续执行结构化降级，直到不超限。
func hardCapPrompt(blocks []PromptBlock, cap int, trace *BuildTrace) []PromptBlock {
	if cap <= 0 {
		return blocks
	}

	out := make([]PromptBlock, 0, len(blocks))
	out = append(out, blocks...)

	for promptTokensFromBlocks(out) > cap {
		idx := findOverflowCandidate(out)
		if idx < 0 {
			break
		}

		total := promptTokensFromBlocks(out)
		block := out[idx]
		overflow := total - cap
		target := block.TokensEst - overflow
		if target < 0 {
			target = 0
		}
		if block.Bucket == BucketSTM && idx == latestSTMIndex(out) && target < 4 {
			target = 4
		}

		fitted, ok, reason := fitSoftBlock(block, target)
		if ok && fitted.TokensEst < block.TokensEst {
			out[idx] = fitted
			trace.markStage("hard_cap", fitted, true, "hard-cap "+reason)
			trace.TrimLogs = append(trace.TrimLogs, "hard-cap degrade "+block.ID+": "+reason)
			recordRAGDegrade(trace, block, fitted)
			continue
		}

		trace.markStage("hard_cap", block, false, "hard-cap drop")
		trace.TrimLogs = append(trace.TrimLogs, "hard-cap drop "+block.ID)
		recordRAGDrop(trace, block)
		out = append(out[:idx], out[idx+1:]...)
	}

	return out
}

// findOverflowCandidate 选择最适合被降级/丢弃的候选 block。
func findOverflowCandidate(blocks []PromptBlock) int {
	idx := -1
	maxScore := -1
	for i, block := range blocks {
		if block.Hard || block.Bucket == BucketUser {
			continue
		}

		score := overflowScore(block)
		if score > maxScore {
			maxScore = score
			idx = i
			continue
		}
		if score == maxScore && idx >= 0 && block.TokensEst > blocks[idx].TokensEst {
			idx = i
		}
	}
	return idx
}

// latestSTMIndex 返回当前保留下来的最新一条历史消息位置。
func latestSTMIndex(blocks []PromptBlock) int {
	idx := -1
	maxSeq := -1
	for i, block := range blocks {
		if block.Bucket != BucketSTM {
			continue
		}
		seq := getSeq(block)
		if seq >= maxSeq {
			maxSeq = seq
			idx = i
		}
	}
	return idx
}

// overflowScore 计算候选优先淘汰分数，分数越高越先处理。
func overflowScore(block PromptBlock) int {
	score := block.Priority
	switch block.Bucket {
	case BucketEmotion:
		score += 5000
	case BucketRAG:
		score += 4000
	case BucketProfile:
		score += 3000
	case BucketSummary:
		// 历史摘要比单条 STM 更浓缩，最终 hard-cap 时应尽量后删。
		score += 900
	case BucketSTM:
		ageWeight := 1000 - minInt(getSeq(block), 1000)
		score += 1500 + ageWeight
	default:
		score += 1000
	}
	return score
}

// parseSectionItems 把 section 文本拆成 header + 列表项。
func parseSectionItems(content string) (header string, items []string) {
	lines := strings.Split(content, "\n")
	header = ""
	items = make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if header == "" {
			header = line
			continue
		}
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		items = append(items, line)
	}
	if len(items) == 0 {
		body := strings.TrimSpace(strings.TrimPrefix(content, header))
		if body != "" {
			items = splitSummaryPhrases(body, 6)
		}
	}
	if header == "" {
		header = "[Context]"
	}
	return header, dedupeStrings(items)
}

// parseRAGItems 解析并去重 RAG 条目。
func parseRAGItems(content string) (header string, entries []string) {
	header, items := parseSectionItems(content)
	if len(items) == 0 {
		return header, nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		key := normalizeRAGKey(item)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return header, out
}

// normalizeRAGKey 将 RAG 条目归一化为可比较键。
func normalizeRAGKey(item string) string {
	item = strings.TrimSpace(strings.ToLower(item))
	if item == "" {
		return ""
	}
	if strings.HasPrefix(item, "[") {
		if idx := strings.Index(item, "]"); idx >= 0 && idx+1 < len(item) {
			item = strings.TrimSpace(item[idx+1:])
		}
	}
	item = strings.ReplaceAll(item, " ", "")
	item = strings.ReplaceAll(item, "\t", "")
	return item
}

// composeSection 将 header 与条目重新拼装成 section 文本。
func composeSection(header string, items []string) string {
	if strings.TrimSpace(header) == "" {
		header = "[Context]"
	}
	lines := []string{strings.TrimSpace(header)}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		lines = append(lines, "- "+item)
	}
	return strings.Join(lines, "\n")
}

// compactItems 对每条项做句级压缩，得到更短候选。
func compactItems(items []string, perItemBudget int) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := trimTextToSentenceTokens(item, perItemBudget)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// splitSummaryPhrases 将文本按句号/分号等拆成短语片段。
func splitSummaryPhrases(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	replacer := strings.NewReplacer(
		"\n", "|",
		"。", "|",
		"！", "|",
		"？", "|",
		";", "|",
		"；", "|",
	)
	parts := strings.Split(replacer.Replace(text), "|")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

// dedupeStrings 对字符串切片做不区分大小写去重。
func dedupeStrings(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		key := strings.TrimSpace(strings.ToLower(item))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(item))
	}
	return out
}

// trimTextToSentenceTokens 以“句”为单位优先裁剪，避免半截语义。
func trimTextToSentenceTokens(text string, budget int) string {
	text = strings.TrimSpace(text)
	if text == "" || budget <= 0 {
		return ""
	}
	if estimateTextTokens(text) <= budget {
		return text
	}

	parts := splitSummaryPhrases(text, 16)
	if len(parts) == 0 {
		return trimTextToTokens(text, budget)
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		candidate := strings.Join(append(out, part), "；")
		if estimateTextTokens(candidate) > budget {
			break
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return trimTextToTokens(text, budget)
	}
	return strings.Join(out, "；")
}

// extractEmotionName 从 EMOTION_GUIDE 文本中提取情绪名称。
func extractEmotionName(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	key := "Detected emotion:"
	i := strings.Index(content, key)
	if i < 0 {
		return ""
	}
	start := i + len(key)
	if start >= len(content) {
		return ""
	}
	rest := strings.TrimSpace(content[start:])
	for idx, r := range rest {
		if r == '(' || r == '\n' {
			return strings.TrimSpace(rest[:idx])
		}
	}
	return strings.TrimSpace(rest)
}

// recordRAGDegrade 在 RAG 条数下降时记录裁剪日志。
func recordRAGDegrade(trace *BuildTrace, before PromptBlock, after PromptBlock) {
	if before.Bucket != BucketRAG {
		return
	}
	beforeK := ragBlockK(before)
	afterK := ragItemCount(after.Content)
	if beforeK > afterK {
		trace.TrimLogs = append(trace.TrimLogs, ragKLog(beforeK, afterK))
	}
}

// recordRAGDrop 记录 RAG 被完全丢弃时的 K 变化日志。
func recordRAGDrop(trace *BuildTrace, block PromptBlock) {
	if block.Bucket != BucketRAG {
		return
	}
	beforeK := ragBlockK(block)
	if beforeK > 0 {
		trace.TrimLogs = append(trace.TrimLogs, ragKLog(beforeK, 0))
	}
}

// uniquePositiveInts 对整数列表做正数去重并保序。
func uniquePositiveInts(items []int) []int {
	seen := map[int]struct{}{}
	out := make([]int, 0, len(items))
	for _, n := range items {
		if n <= 0 {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// minInt 返回两个整数中的较小值。
func minInt(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

// selectBucket 筛选指定预算桶的 block 集合。
func selectBucket(blocks []PromptBlock, bucket BudgetBucket) []PromptBlock {
	out := make([]PromptBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Bucket == bucket {
			out = append(out, block)
		}
	}
	return out
}

// countBucketTokens 统计某个预算桶的 token 总量。
func countBucketTokens(blocks []PromptBlock, bucket BudgetBucket) int {
	total := 0
	for _, block := range blocks {
		if block.Bucket == bucket {
			total += block.TokensEst
		}
	}
	return total
}

// countBlocksTokens 统计 block 列表总 token。
func countBlocksTokens(blocks []PromptBlock) int {
	total := 0
	for _, block := range blocks {
		total += block.TokensEst
	}
	return total
}

// promptTokensFromBlocks 统计最终 prompt block 的 token 总量。
func promptTokensFromBlocks(blocks []PromptBlock) int {
	total := 0
	for _, block := range blocks {
		total += block.TokensEst
	}
	return total
}

// ragBlockK 读取 RAG block 的请求 K（优先 metadata，其次内容估算）。
func ragBlockK(block PromptBlock) int {
	if block.Bucket != BucketRAG {
		return 0
	}
	if v, ok := block.Metadata["rag_requested_k"]; ok {
		if n := atoiSafe(v); n > 0 {
			if strings.Contains(block.Content, "RELEVANT_MEMORIES") {
				contentK := ragItemCount(block.Content)
				if contentK > 0 && contentK < n {
					return contentK
				}
			}
			return n
		}
	}
	return ragItemCount(block.Content)
}

// countRAGRequestedK 统计所有 RAG block 的请求 K 总数。
func countRAGRequestedK(blocks []PromptBlock) int {
	total := 0
	for _, block := range blocks {
		if block.Bucket == BucketRAG {
			total += ragBlockK(block)
		}
	}
	return total
}

// countRAGFinalK 统计最终保留内容中的 RAG 条目数。
func countRAGFinalK(blocks []PromptBlock) int {
	total := 0
	for _, block := range blocks {
		if block.Bucket != BucketRAG {
			continue
		}
		total += ragItemCount(block.Content)
	}
	return total
}

// ragKLog 生成统一的 RAG K 变化日志文本。
func ragKLog(before, after int) string {
	return "RAG K degraded from " + itoaSafe(before) + " to " + itoaSafe(after)
}

// itoaSafe 是不依赖 strconv 的轻量整数转字符串实现。
func itoaSafe(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
