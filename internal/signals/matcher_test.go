package signals

import (
	"context"
	"testing"

	"ai-gf/internal/llm"
)

type stubEmbedder struct {
	vectors map[string][]float32
}

func (s stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if vec, ok := s.vectors[text]; ok {
		return vec, nil
	}
	return []float32{0, 1}, nil
}

type stubGenerator struct {
	response string
	calls    int
}

func (s *stubGenerator) Generate(ctx context.Context, req llm.GenerateRequest) (string, error) {
	s.calls++
	return s.response, nil
}

func TestMatcherBlocksNegatedVulnerability(t *testing.T) {
	t.Parallel()

	matcher := NewMatcher(nil)
	got := matcher.ScoreSignal(context.Background(), "我已经不焦虑了，现在好多了", SignalVulnerability)
	if got != 0 {
		t.Fatalf("expected negated vulnerability to score 0, got %.2f", got)
	}
}

func TestMatcherMatchesSupportTemplate(t *testing.T) {
	t.Parallel()

	matcher := NewMatcher(nil)
	got := matcher.ScoreSignal(context.Background(), "可以陪我待一会吗", SignalSupportSeeking)
	if got < 0.4 {
		t.Fatalf("expected support template to score >= 0.4, got %.2f", got)
	}
}

func TestMatcherUsesEmbeddingFallback(t *testing.T) {
	t.Parallel()

	matcher := NewMatcher(stubEmbedder{
		vectors: map[string][]float32{
			"心里像被抽空了一样": {1, 0},
			"心里空落落的":    {1, 0},
		},
	})
	got := matcher.ScoreSignal(context.Background(), "心里像被抽空了一样", SignalVulnerability)
	if got <= 0 {
		t.Fatalf("expected embedding fallback to produce a score, got %.2f", got)
	}
}

func TestMatcherUsesLLMFallbackWhenRuleAndEmbeddingAreWeak(t *testing.T) {
	t.Parallel()

	generator := &stubGenerator{
		response: `{"vulnerability":0.78}`,
	}
	matcher := NewMatcher(nil)
	matcher.SetAnalyzer(NewLLMAnalyzer(generator))

	got := matcher.ScoreSignal(context.Background(), "心里像被掏空了一块", SignalVulnerability)
	if got < 0.78 {
		t.Fatalf("expected llm fallback score >= 0.78, got %.2f", got)
	}
	if generator.calls != 1 {
		t.Fatalf("expected llm analyzer to be called once, got %d", generator.calls)
	}
}

func TestMatcherSkipsLLMWhenLexicalAlreadyClear(t *testing.T) {
	t.Parallel()

	generator := &stubGenerator{
		response: `{"vulnerability":0.90}`,
	}
	matcher := NewMatcher(nil)
	matcher.SetAnalyzer(NewLLMAnalyzer(generator))

	got := matcher.ScoreSignal(context.Background(), "我今天真的好焦虑", SignalVulnerability)
	if got < 0.4 || got >= 0.9 {
		t.Fatalf("expected lexical score without llm override, got %.2f", got)
	}
	if generator.calls != 0 {
		t.Fatalf("expected llm analyzer to be skipped, got %d calls", generator.calls)
	}
}

func TestMatcherSkipsLLMWhenEmbeddingAlreadySolved(t *testing.T) {
	t.Parallel()

	generator := &stubGenerator{
		response: `{"vulnerability":0.82}`,
	}
	matcher := NewMatcher(stubEmbedder{
		vectors: map[string][]float32{
			"心里像被抽空了一样": {1, 0},
			"心里空落落的":    {1, 0},
		},
	})
	matcher.SetAnalyzer(NewLLMAnalyzer(generator))

	got := matcher.ScoreSignal(context.Background(), "心里像被抽空了一样", SignalVulnerability)
	if got <= 0 {
		t.Fatalf("expected embedding fallback score > 0, got %.2f", got)
	}
	if generator.calls != 0 {
		t.Fatalf("expected llm analyzer to be skipped after embedding fallback, got %d calls", generator.calls)
	}
}
