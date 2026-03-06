package ranking

import "strings"

// compressMemories 把候选压缩为简洁、可注入 prompt 的记忆句。
func (r *Ranker) compressMemories(selected []scoredCandidate) []MemoryItem {
	out := make([]MemoryItem, 0, len(selected))
	for _, item := range selected {
		line := strings.TrimSpace(item.ContentShort)
		if line == "" {
			line = strings.TrimSpace(item.Content)
		}
		line = compressSentence(line)
		if line == "" {
			continue
		}
		out = append(out, MemoryItem{
			ID:         item.ID,
			SourceID:   item.SourceID,
			Kind:       item.Kind,
			Topic:      item.Topic,
			Content:    line,
			Score:      item.Score,
			Similarity: item.Similarity,
			Importance: item.Importance,
			Confidence: item.Confidence,
		})
	}
	return out
}

func compressSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// 记忆句只保留第一句，避免冗长段落占预算。
	marks := []string{"。", "!", "！", "?", "？", "\n"}
	for _, mark := range marks {
		if idx := strings.Index(s, mark); idx >= 0 {
			s = strings.TrimSpace(s[:idx])
			break
		}
	}
	if len([]rune(s)) > 80 {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:80]))
	}
	return s
}
