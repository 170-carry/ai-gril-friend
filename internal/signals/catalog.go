package signals

import (
	"context"
	"math"
	"strings"
	"sync"
)

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type Analyzer interface {
	Analyze(ctx context.Context, text string, catalog Catalog) (map[string]float64, error)
}

type ScoreOptions struct {
	AllowEmbedding bool
	AllowLLM       bool
}

var DefaultScoreOptions = ScoreOptions{
	AllowEmbedding: true,
	AllowLLM:       true,
}

type Matcher struct {
	catalog   Catalog
	embedder  Embedder
	analyzer  Analyzer
	mu        sync.RWMutex
	seedCache map[string][]float32
}

func NewMatcher(embedder Embedder) *Matcher {
	return &Matcher{
		catalog:   DefaultCatalog(),
		embedder:  embedder,
		seedCache: make(map[string][]float32),
	}
}

func (m *Matcher) SetAnalyzer(analyzer Analyzer) {
	m.analyzer = analyzer
}

func (m *Matcher) ScoreSignal(ctx context.Context, text, key string) float64 {
	scores := m.ScoreSignalsWithOptions(ctx, text, DefaultScoreOptions, key)
	return scores[key]
}

func (m *Matcher) ScoreSignals(ctx context.Context, text string, keys ...string) map[string]float64 {
	return m.ScoreSignalsWithOptions(ctx, text, DefaultScoreOptions, keys...)
}

func (m *Matcher) ScoreSignalWithOptions(ctx context.Context, text, key string, opts ScoreOptions) float64 {
	scores := m.ScoreSignalsWithOptions(ctx, text, opts, key)
	return scores[key]
}

func (m *Matcher) ScoreSignalsWithOptions(ctx context.Context, text string, opts ScoreOptions, keys ...string) map[string]float64 {
	text = normalize(text)
	out := make(map[string]float64)
	if text == "" {
		for _, key := range keys {
			out[key] = 0
		}
		return out
	}

	selected := keys
	if len(selected) == 0 {
		selected = make([]string, 0, len(m.catalog.Signals))
		for key := range m.catalog.Signals {
			selected = append(selected, key)
		}
	}

	selected = uniqueStrings(selected)
	needEmbedding := false
	maxLexical := 0.0
	for _, key := range selected {
		rule, ok := m.catalog.Signals[key]
		if !ok {
			out[key] = 0
			continue
		}
		score := lexicalScore(text, rule)
		out[key] = score
		maxLexical = maxFloat(maxLexical, score)
		if opts.AllowEmbedding && m.embedder != nil && len(rule.EmbeddingSeeds) > 0 && score < rule.EmbeddingMinLexical {
			needEmbedding = true
		}
	}

	// embedding 只作为弱命中场景的兜底，避免强规则命中时再走额外语义开销。
	if needEmbedding && opts.AllowEmbedding && m.embedder != nil && maxLexical < 0.40 {
		textVec, err := m.embedder.Embed(ctx, text)
		if err == nil && len(textVec) > 0 {
			for _, key := range selected {
				rule, ok := m.catalog.Signals[key]
				if !ok || len(rule.EmbeddingSeeds) == 0 || out[key] >= rule.EmbeddingMinLexical {
					continue
				}
				semantic := m.semanticScore(ctx, textVec, rule)
				if semantic > out[key] {
					out[key] = semantic
				}
			}
		}
	}

	maxScore := maxScore(out)
	if !opts.AllowLLM || m.analyzer == nil || maxScore >= 0.35 {
		return out
	}

	llmScores, err := m.analyzer.Analyze(ctx, text, m.catalog)
	if err != nil {
		return out
	}
	for _, key := range selected {
		score := clamp01(llmScores[key])
		if score > out[key] {
			out[key] = score
		}
	}
	return out
}

func (m *Matcher) ScoreIntents(ctx context.Context, text string) map[string]float64 {
	signalSet := make([]string, 0, len(m.catalog.Signals))
	for key := range m.catalog.Signals {
		signalSet = append(signalSet, key)
	}
	signalScores := m.ScoreSignals(ctx, text, signalSet...)

	out := make(map[string]float64, len(m.catalog.Intents))
	for key, rule := range m.catalog.Intents {
		var score float64
		for _, blend := range rule.Signals {
			score += signalScores[blend.Signal] * blend.Weight
		}
		out[key] = clamp01(score)
	}
	return out
}

func (m *Matcher) semanticScore(ctx context.Context, textVec []float32, rule SignalRule) float64 {
	if len(textVec) == 0 || len(rule.EmbeddingSeeds) == 0 || m.embedder == nil {
		return 0
	}

	best := 0.0
	for _, seed := range rule.EmbeddingSeeds {
		seedVec, err := m.seedEmbedding(ctx, seed)
		if err != nil || len(seedVec) == 0 {
			continue
		}
		best = maxFloat(best, cosineSimilarity(textVec, seedVec))
	}
	if best < rule.EmbeddingThreshold || rule.EmbeddingThreshold >= 1 {
		return 0
	}

	normalized := (best - rule.EmbeddingThreshold) / (1 - rule.EmbeddingThreshold)
	return clamp01(normalized * rule.EmbeddingMaxScore)
}

func (m *Matcher) seedEmbedding(ctx context.Context, seed string) ([]float32, error) {
	m.mu.RLock()
	if vec, ok := m.seedCache[seed]; ok {
		m.mu.RUnlock()
		return vec, nil
	}
	m.mu.RUnlock()

	vec, err := m.embedder.Embed(ctx, seed)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.seedCache[seed] = vec
	m.mu.Unlock()
	return vec, nil
}

func lexicalScore(text string, rule SignalRule) float64 {
	if text == "" {
		return 0
	}

	score := 0.0
	for _, phrase := range rule.Phrases {
		if hasPositivePhrase(text, phrase.Text, rule) {
			score += phrase.Weight
		}
	}
	for _, re := range rule.Regexes {
		if hasPositiveRegexp(text, re, rule) {
			score += re.Weight
		}
	}
	for _, tpl := range rule.Templates {
		if hasPositiveTemplate(text, tpl, rule) {
			score += tpl.Weight
		}
	}
	return clamp01(score)
}

func hasPositivePhrase(text, phrase string, rule SignalRule) bool {
	phrase = normalize(phrase)
	if phrase == "" {
		return false
	}

	offset := 0
	for offset <= len(text) {
		idx := strings.Index(text[offset:], phrase)
		if idx < 0 {
			return false
		}
		abs := offset + idx
		if !isNegatedByByteIndex(text, abs, rule) {
			return true
		}
		offset = abs + len(phrase)
	}
	return false
}

func hasPositiveRegexp(text string, re RegexRule, rule SignalRule) bool {
	if re.re == nil {
		return false
	}
	for _, loc := range re.re.FindAllStringIndex(text, -1) {
		if len(loc) != 2 {
			continue
		}
		if !isNegatedByByteIndex(text, loc[0], rule) {
			return true
		}
	}
	return false
}

func hasPositiveTemplate(text string, tpl TemplateRule, rule SignalRule) bool {
	start, ok := findTemplateStart(text, tpl)
	if !ok {
		return false
	}
	return !isNegatedByRuneIndex(text, start, rule)
}

func findTemplateStart(text string, tpl TemplateRule) (int, bool) {
	if len(tpl.Parts) == 0 {
		return 0, false
	}
	runes := []rune(text)
	parts := make([][]rune, 0, len(tpl.Parts))
	for _, part := range tpl.Parts {
		part = normalize(part)
		if part == "" {
			continue
		}
		parts = append(parts, []rune(part))
	}
	if len(parts) == 0 {
		return 0, false
	}

	for start := 0; start < len(runes); start++ {
		if !hasRunePrefix(runes[start:], parts[0]) {
			continue
		}
		cursor := start + len(parts[0])
		matched := true
		for _, part := range parts[1:] {
			next := -1
			limit := minInt(len(runes)-len(part), cursor+tpl.MaxGap)
			for i := cursor; i <= limit; i++ {
				if hasRunePrefix(runes[i:], part) {
					next = i
					break
				}
			}
			if next < 0 {
				matched = false
				break
			}
			cursor = next + len(part)
		}
		if matched {
			return start, true
		}
	}
	return 0, false
}

func hasRunePrefix(src, prefix []rune) bool {
	if len(prefix) == 0 || len(src) < len(prefix) {
		return false
	}
	for i := range prefix {
		if src[i] != prefix[i] {
			return false
		}
	}
	return true
}

func isNegatedByByteIndex(text string, byteIdx int, rule SignalRule) bool {
	if !rule.NegationSensitive || byteIdx <= 0 {
		return false
	}
	return hasNegation(prefixByByte(text, byteIdx, rule.NegationWindow), rule)
}

func isNegatedByRuneIndex(text string, runeIdx int, rule SignalRule) bool {
	if !rule.NegationSensitive || runeIdx <= 0 {
		return false
	}
	runes := []rune(text)
	window := rule.NegationWindow
	if window <= 0 {
		window = 4
	}
	start := maxInt(runeIdx-window, 0)
	return hasNegation(string(runes[start:runeIdx]), rule)
}

func hasNegation(prefix string, rule SignalRule) bool {
	prefix = normalize(prefix)
	if prefix == "" {
		return false
	}
	for _, word := range rule.NegationWords {
		word = normalize(word)
		if word != "" && strings.Contains(prefix, word) {
			return true
		}
	}
	return false
}

func prefixByByte(text string, byteIdx, window int) string {
	if byteIdx <= 0 {
		return ""
	}
	if byteIdx > len(text) {
		byteIdx = len(text)
	}
	runes := []rune(text[:byteIdx])
	if len(runes) == 0 {
		return ""
	}
	if window <= 0 {
		window = 4
	}
	start := maxInt(len(runes)-window, 0)
	return string(runes[start:])
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aNorm, bNorm float64
	for i := range a {
		dot += float64(a[i] * b[i])
		aNorm += float64(a[i] * a[i])
		bNorm += float64(b[i] * b[i])
	}
	if aNorm == 0 || bNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(aNorm) * math.Sqrt(bNorm))
}

func normalize(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
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

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a >= b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a >= b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

func maxScore(scores map[string]float64) float64 {
	best := 0.0
	for _, score := range scores {
		best = maxFloat(best, score)
	}
	return best
}
