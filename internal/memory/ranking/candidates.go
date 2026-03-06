package ranking

import "strings"

// generateCandidates 规范化候选并做基础去重。
func (r *Ranker) generateCandidates(req RankRequest, traces traceBook) []Candidate {
	out := make([]Candidate, 0, len(req.InitialCandidates))
	seen := map[string]struct{}{}
	for _, c := range req.InitialCandidates {
		c.ID = strings.TrimSpace(c.ID)
		if c.ID == "" {
			continue
		}
		if _, ok := seen[c.ID]; ok {
			continue
		}
		seen[c.ID] = struct{}{}

		c.Content = strings.TrimSpace(c.Content)
		c.ContentShort = strings.TrimSpace(c.ContentShort)
		if c.Content == "" && c.ContentShort == "" {
			continue
		}
		if c.ContentShort == "" {
			c.ContentShort = c.Content
		}
		if c.Content == "" {
			c.Content = c.ContentShort
		}
		if c.Importance <= 0 {
			c.Importance = 3
		}
		if c.Importance > 5 {
			c.Importance = 5
		}
		if c.Confidence <= 0 {
			c.Confidence = 0.7
		}
		if c.Confidence > 1 {
			c.Confidence = 1
		}
		if c.Topic == "" {
			c.Topic = "general"
		}
		if c.Kind == CandidateSemantic && c.Similarity <= 0 {
			c.Similarity = lexicalSimilarity(req.UserMessage, c.ContentShort)
		}
		if c.Kind == CandidateStructured && c.Similarity <= 0 {
			c.Similarity = maxFloat(0.35, lexicalSimilarity(req.UserMessage, c.ContentShort))
		}

		out = append(out, c)
		traces.ensure(c)
	}
	return out
}

func maxFloat(a, b float64) float64 {
	if a >= b {
		return a
	}
	return b
}
