package ranking

import (
	"strings"
	"time"
)

// filterCandidates 先执行策略过滤，再进入评分阶段。
func (r *Ranker) filterCandidates(req RankRequest, in []Candidate, traces traceBook) []Candidate {
	out := make([]Candidate, 0, len(in))
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}

	for _, c := range in {
		t := traces.ensure(c)
		if c.Superseded {
			t.Filtered = true
			t.FilterReason = "superseded"
			continue
		}
		if c.Kind == CandidateSemantic && c.Similarity < r.cfg.SimilarityThreshold {
			t.Filtered = true
			t.FilterReason = "below_similarity_threshold"
			continue
		}
		if hitBoundary(c, req.BoundaryKeywords) {
			t.Filtered = true
			t.FilterReason = "boundary_conflict"
			continue
		}

		ageDays := now.Sub(c.CreatedAt).Hours() / 24
		if c.Confidence < 0.6 && c.Importance <= 1 && c.AccessCount == 0 && ageDays > 30 {
			t.Filtered = true
			t.FilterReason = "low_confidence_low_value_old"
			continue
		}
		out = append(out, c)
	}
	return out
}

func hitBoundary(c Candidate, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	text := strings.ToLower(c.Content + " " + c.ContentShort + " " + c.Topic)
	for _, kw := range keywords {
		kw = strings.TrimSpace(strings.ToLower(kw))
		if kw == "" {
			continue
		}
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}
