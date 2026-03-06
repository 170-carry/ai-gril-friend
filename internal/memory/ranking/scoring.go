package ranking

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// scoredCandidate 是内部评分结构。
type scoredCandidate struct {
	Candidate
	Score float64
}

// scoreCandidates 执行多因子评分。
func (r *Ranker) scoreCandidates(req RankRequest, in []Candidate, traces traceBook) []scoredCandidate {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	contentFreq := buildContentFrequency(in)

	out := make([]scoredCandidate, 0, len(in))
	for _, c := range in {
		simScore := clamp01(c.Similarity)
		importanceScore := clamp01(float64(c.Importance) / 5.0)
		ageDays := now.Sub(c.CreatedAt).Hours() / 24
		tau := 30.0
		if c.Importance >= 4 {
			tau = 120.0
		}
		recencyScore := math.Exp(-maxFloat(ageDays, 0) / tau)
		usageScore := math.Log(1+float64(maxInt(c.AccessCount, 0))) / math.Log(1+20.0)
		pinnedScore := 0.0
		if c.Pinned {
			pinnedScore = 1.0
		}
		redundancyPenalty := estimateRedundancyPenalty(c, contentFreq)

		score := r.cfg.WSim*simScore +
			r.cfg.WImp*importanceScore +
			r.cfg.WRec*recencyScore +
			r.cfg.WUse*usageScore +
			r.cfg.WPin*pinnedScore -
			r.cfg.WRed*redundancyPenalty

		out = append(out, scoredCandidate{Candidate: c, Score: score})
		t := traces.ensure(c)
		t.Score = score
		t.Notes = append(t.Notes,
			"sim="+fmtFloat(simScore),
			"imp="+fmtFloat(importanceScore),
			"rec="+fmtFloat(recencyScore),
			"red="+fmtFloat(redundancyPenalty),
		)
	}
	return out
}

func buildContentFrequency(items []Candidate) map[string]int {
	freq := map[string]int{}
	for _, item := range items {
		key := normalizeContentKey(item.ContentShort)
		if key == "" {
			key = normalizeContentKey(item.Content)
		}
		if key == "" {
			continue
		}
		freq[key]++
	}
	return freq
}

func estimateRedundancyPenalty(item Candidate, freq map[string]int) float64 {
	key := normalizeContentKey(item.ContentShort)
	if key == "" {
		key = normalizeContentKey(item.Content)
	}
	if key == "" {
		return 0
	}
	duplicates := freq[key] - 1
	if duplicates <= 0 {
		return 0
	}
	// 重复越多惩罚越高，上限 1。
	return clamp01(float64(duplicates) / 3.0)
}

func normalizeContentKey(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"\n", " ",
		"\t", " ",
		"，", " ",
		"。", " ",
		"、", " ",
		",", " ",
		";", " ",
		"；", " ",
		":", " ",
		"：", " ",
	)
	text = replacer.Replace(text)
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) > 80 {
		text = string(r[:80])
	}
	return text
}

func lexicalSimilarity(query, content string) float64 {
	q := tokenize(query)
	c := tokenize(content)
	if len(q) == 0 || len(c) == 0 {
		return 0
	}
	setQ := map[string]struct{}{}
	for _, token := range q {
		setQ[token] = struct{}{}
	}
	intersect := 0
	setC := map[string]struct{}{}
	for _, token := range c {
		setC[token] = struct{}{}
		if _, ok := setQ[token]; ok {
			intersect++
		}
	}
	union := len(setQ) + len(setC) - intersect
	if union <= 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}

func tokenize(text string) []string {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return nil
	}
	replacer := strings.NewReplacer(
		"\n", " ",
		"\t", " ",
		"，", " ",
		"。", " ",
		"、", " ",
		",", " ",
		";", " ",
		"；", " ",
		":", " ",
		"：", " ",
	)
	text = replacer.Replace(text)
	parts := strings.Fields(text)
	if len(parts) > 0 {
		return parts
	}

	// 对纯中文场景回退为 2-gram，避免分词库依赖。
	runes := []rune(strings.ReplaceAll(text, " ", ""))
	if len(runes) < 2 {
		if len(runes) == 1 {
			return []string{string(runes[0])}
		}
		return nil
	}
	out := make([]string, 0, len(runes)-1)
	for i := 0; i+1 < len(runes); i++ {
		out = append(out, string(runes[i:i+2]))
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxInt(a, b int) int {
	if a >= b {
		return a
	}
	return b
}

func fmtFloat(v float64) string {
	return strconvFormat(v)
}

func strconvFormat(v float64) string {
	// 不引入 fmt/scientific 输出，统一保留三位小数。
	return strings.TrimRight(strings.TrimRight(fmtSprintf(v), "0"), ".")
}

func fmtSprintf(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}
