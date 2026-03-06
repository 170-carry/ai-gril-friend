package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ai-gf/internal/llm"
)

type Generator interface {
	Generate(ctx context.Context, req llm.GenerateRequest) (string, error)
}

type LLMAnalyzer struct {
	generator Generator
	mu        sync.RWMutex
	cache     map[string]map[string]float64
}

func NewLLMAnalyzer(generator Generator) *LLMAnalyzer {
	if generator == nil {
		return nil
	}
	return &LLMAnalyzer{
		generator: generator,
		cache:     make(map[string]map[string]float64),
	}
}

func (a *LLMAnalyzer) Analyze(ctx context.Context, text string, catalog Catalog) (map[string]float64, error) {
	if a == nil || a.generator == nil {
		return nil, fmt.Errorf("llm analyzer is not configured")
	}

	text = normalize(text)
	if text == "" {
		return nil, fmt.Errorf("llm analyzer input is empty")
	}

	a.mu.RLock()
	if cached, ok := a.cache[text]; ok {
		a.mu.RUnlock()
		return cloneScores(cached), nil
	}
	a.mu.RUnlock()

	raw, err := a.generator.Generate(ctx, llm.GenerateRequest{
		Messages: []llm.Message{
			{
				Role: "system",
				Content: "You are a careful Chinese NLP signal classifier. " +
					"Return only valid JSON. Be conservative. Handle negation, reversal, and boundary language correctly.",
			},
			{
				Role:    "user",
				Content: buildLLMSignalPrompt(text, catalog),
			},
		},
		Temperature: 0,
	})
	if err != nil {
		return nil, err
	}

	scores, err := parseLLMSignalScores(raw, catalog)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.cache[text] = cloneScores(scores)
	a.mu.Unlock()
	return scores, nil
}

func buildLLMSignalPrompt(text string, catalog Catalog) string {
	keys := make([]string, 0, len(catalog.Signals))
	for key := range catalog.Signals {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("Analyze this Chinese chat message and score each signal from 0.0 to 1.0.\n")
	b.WriteString("Scoring rules:\n")
	b.WriteString("- 0.0 means absent, negated, or clearly not intended.\n")
	b.WriteString("- 0.2-0.4 means weak hint.\n")
	b.WriteString("- 0.5-0.7 means clear presence.\n")
	b.WriteString("- 0.8-1.0 means explicit and strong.\n")
	b.WriteString("- If the user explicitly negates a feeling or request, score it near 0.\n")
	b.WriteString("- Be conservative. If unsure, stay low.\n")
	b.WriteString("Examples:\n")
	b.WriteString("- \"我已经不焦虑了\" => vulnerability should be 0.\n")
	b.WriteString("- \"不用陪我\" => support_seeking should be 0.\n")
	b.WriteString("- \"别问了\" => boundary should be high.\n")
	b.WriteString("Signals:\n")
	for _, key := range keys {
		rule := catalog.Signals[key]
		b.WriteString("- ")
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(rule.Description))
		examples := signalExamples(rule)
		if examples != "" {
			b.WriteString(" Examples: ")
			b.WriteString(examples)
		}
		b.WriteString("\n")
	}
	b.WriteString("Return JSON only using this shape:\n")
	b.WriteString("{")
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fmt.Sprintf("%q: 0", key))
	}
	b.WriteString("}\n")
	b.WriteString("Message:\n")
	b.WriteString(text)
	return b.String()
}

func signalExamples(rule SignalRule) string {
	samples := make([]string, 0, 4)
	for _, phrase := range rule.Phrases {
		if phrase.Text == "" {
			continue
		}
		samples = append(samples, phrase.Text)
		if len(samples) >= 2 {
			break
		}
	}
	for _, seed := range rule.EmbeddingSeeds {
		seed = strings.TrimSpace(seed)
		if seed == "" {
			continue
		}
		samples = append(samples, seed)
		if len(samples) >= 4 {
			break
		}
	}
	if len(samples) == 0 {
		return ""
	}
	return strings.Join(uniqueStrings(samples), " / ")
}

func parseLLMSignalScores(raw string, catalog Catalog) (map[string]float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty llm analyzer response")
	}

	cleaned := extractJSONObject(raw)
	var direct map[string]float64
	if err := json.Unmarshal([]byte(cleaned), &direct); err == nil {
		return normalizeLLMScores(direct, catalog), nil
	}

	var envelope struct {
		Signals map[string]float64 `json:"signals"`
	}
	if err := json.Unmarshal([]byte(cleaned), &envelope); err == nil && len(envelope.Signals) > 0 {
		return normalizeLLMScores(envelope.Signals, catalog), nil
	}
	return nil, fmt.Errorf("decode llm analyzer response failed")
}

func extractJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end >= start {
		return raw[start : end+1]
	}
	return raw
}

func normalizeLLMScores(scores map[string]float64, catalog Catalog) map[string]float64 {
	out := make(map[string]float64, len(catalog.Signals))
	for key := range catalog.Signals {
		out[key] = clamp01(scores[key])
	}
	return out
}

func cloneScores(scores map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(scores))
	for key, value := range scores {
		out[key] = value
	}
	return out
}
