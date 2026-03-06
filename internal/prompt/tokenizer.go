package prompt

import (
	"strings"

	"ai-gf/internal/llm"
)

// splitBudgets 按配置比例拆分 system/memory/history 三个预算桶。
func splitBudgets(total int, cfg BudgetConfig) (int, int, int) {
	sum := cfg.SystemRatio + cfg.MemoryRatio + cfg.HistoryRatio
	if sum <= 0 {
		def := DefaultBudgetConfig()
		sum = def.SystemRatio + def.MemoryRatio + def.HistoryRatio
		cfg.SystemRatio = def.SystemRatio
		cfg.MemoryRatio = def.MemoryRatio
		cfg.HistoryRatio = def.HistoryRatio
	}

	systemBudget := total * cfg.SystemRatio / sum
	memoryBudget := total * cfg.MemoryRatio / sum
	historyBudget := total - systemBudget - memoryBudget
	if systemBudget < 64 {
		systemBudget = 64
	}
	if memoryBudget < 64 {
		memoryBudget = 64
	}
	if historyBudget < 0 {
		historyBudget = 0
	}
	return systemBudget, memoryBudget, historyBudget
}

// estimateTextTokens 是轻量 token 估算器：非 ASCII 按 1 token，ASCII 按 4:1 估算。
func estimateTextTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	asciiCount := 0
	nonASCII := 0
	for _, r := range text {
		if r <= 127 {
			asciiCount++
		} else {
			nonASCII++
		}
	}
	tokens := nonASCII + (asciiCount+3)/4
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// estimateMessageTokens 估算单条消息 token（含消息结构开销）。
func estimateMessageTokens(msg llm.Message) int {
	return 4 + estimateTextTokens(msg.Content)
}

// estimateMessagesTokens 估算完整消息数组总 token。
func estimateMessagesTokens(messages []llm.Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	return total
}

// trimTextToTokens 按预算截断文本并追加省略号（仅用于允许文本裁剪的场景）。
func trimTextToTokens(text string, budget int) string {
	text = strings.TrimSpace(text)
	if text == "" || budget <= 0 {
		return ""
	}
	if estimateTextTokens(text) <= budget {
		return text
	}

	budgetQ := budget * 4
	usedQ := 0
	var b strings.Builder
	for _, r := range text {
		cost := 1
		if r > 127 {
			cost = 4
		}
		if usedQ+cost > budgetQ {
			break
		}
		b.WriteRune(r)
		usedQ += cost
	}

	trimmed := strings.TrimSpace(b.String())
	if trimmed == "" {
		return ""
	}
	if !strings.HasSuffix(trimmed, "...") && budget >= 2 {
		trimmed += "..."
	}
	return trimmed
}

// maxInt 返回两个整数中的较大值。
func maxInt(a, b int) int {
	if a >= b {
		return a
	}
	return b
}
