package memory

import (
	"context"
	"testing"
	"time"

	"ai-gf/internal/repo"
	"ai-gf/internal/signals"
)

func TestApplyRelationshipSignalsIncreasesScoresOnWarmTurn(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 18, 0, 0, 0, time.UTC)
	base := repo.RelationshipState{
		UserID:               "u1",
		Stage:                "familiar",
		FamiliarityScore:     0.44,
		IntimacyScore:        0.42,
		TrustScore:           0.48,
		FlirtScore:           0.16,
		BoundaryRiskScore:    0.08,
		SupportNeedScore:     0.26,
		PlayfulnessThreshold: 0.22,
		InteractionHeat:      0.25,
	}
	sig := relationshipSignals{
		Affection:      0.7,
		Vulnerability:  0.7,
		SupportSeeking: 0.4,
		SelfDisclosure: 0.5,
		Engagement:     0.6,
		Acceptance:     0.5,
		Humor:          0.4,
		RoutineWarmth:  0.4,
		Heat:           0.8,
	}

	got := applyRelationshipSignals(base, sig, now)
	if got.IntimacyScore <= base.IntimacyScore {
		t.Fatalf("expected intimacy to increase, got %.2f -> %.2f", base.IntimacyScore, got.IntimacyScore)
	}
	if got.TrustScore <= base.TrustScore {
		t.Fatalf("expected trust to increase, got %.2f -> %.2f", base.TrustScore, got.TrustScore)
	}
	if got.InteractionHeat <= base.InteractionHeat {
		t.Fatalf("expected interaction heat to increase, got %.2f -> %.2f", base.InteractionHeat, got.InteractionHeat)
	}
	if got.TotalTurns != 1 {
		t.Fatalf("expected total turns incremented to 1, got %d", got.TotalTurns)
	}
}

func TestDeriveRelationshipStage(t *testing.T) {
	t.Parallel()

	if stage := deriveRelationshipStage(repo.RelationshipState{
		FamiliarityScore:  0.86,
		IntimacyScore:     0.82,
		TrustScore:        0.80,
		FlirtScore:        0.76,
		BoundaryRiskScore: 0.10,
		SupportNeedScore:  0.24,
		InteractionHeat:   0.70,
	}); stage != "romantic" {
		t.Fatalf("expected romantic, got %s", stage)
	}

	if stage := deriveRelationshipStage(repo.RelationshipState{
		BoundaryRiskScore: 0.78,
		IntimacyScore:     0.62,
		TrustScore:        0.70,
		InteractionHeat:   0.48,
	}); stage != "companion" {
		t.Fatalf("expected companion, got %s", stage)
	}
}

func TestApplyRelationshipSignals_BoundaryRiskFreezesCloseness(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 18, 0, 0, 0, time.UTC)
	base := repo.RelationshipState{
		UserID:               "u1",
		Stage:                "light_flirt",
		FamiliarityScore:     0.68,
		IntimacyScore:        0.60,
		TrustScore:           0.70,
		FlirtScore:           0.52,
		BoundaryRiskScore:    0.10,
		SupportNeedScore:     0.22,
		PlayfulnessThreshold: 0.34,
		InteractionHeat:      0.48,
	}
	sig := relationshipSignals{
		Affection:   0.85,
		Acceptance:  0.75,
		Romantic:    0.82,
		Boundary:    0.88,
		Heat:        0.52,
		Engagement:  0.30,
		SupportNeed: 0.18,
	}

	got := applyRelationshipSignals(base, sig, now)
	if got.FlirtScore >= base.FlirtScore {
		t.Fatalf("expected flirt to stop increasing under high boundary risk, got %.2f -> %.2f", base.FlirtScore, got.FlirtScore)
	}
	if got.Stage != "companion" {
		t.Fatalf("expected boundary freeze to pull stage back to companion, got %s", got.Stage)
	}
}

func TestApplyRelationshipSignals_PrioritizesSupportOverUpgrade(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 18, 0, 0, 0, time.UTC)
	base := repo.RelationshipState{
		UserID:               "u1",
		Stage:                "trust_building",
		FamiliarityScore:     0.58,
		IntimacyScore:        0.42,
		TrustScore:           0.40,
		FlirtScore:           0.25,
		BoundaryRiskScore:    0.08,
		SupportNeedScore:     0.72,
		PlayfulnessThreshold: 0.20,
		InteractionHeat:      0.30,
	}
	sig := relationshipSignals{
		Affection:      0.80,
		SupportSeeking: 0.30,
		SelfDisclosure: 0.10,
		Acceptance:     0.80,
		Romantic:       0.80,
		DependenceRisk: 0.70,
		SupportNeed:    0.95,
		Heat:           0.40,
	}

	got := applyRelationshipSignals(base, sig, now)
	if got.SupportNeedScore <= base.SupportNeedScore {
		t.Fatalf("expected support need to increase, got %.2f -> %.2f", base.SupportNeedScore, got.SupportNeedScore)
	}
	if got.FlirtScore > base.FlirtScore+0.05 {
		t.Fatalf("expected flirt growth to stay dampened during support-priority turn, got %.2f -> %.2f", base.FlirtScore, got.FlirtScore)
	}
	if got.Stage != "trust_building" {
		t.Fatalf("expected support-priority state to remain trust_building, got %s", got.Stage)
	}
}

func TestRelationshipSignalsFromTurn_BlocksNegatedVulnerability(t *testing.T) {
	t.Parallel()

	sig := relationshipSignalsFromTurn(context.Background(), signals.NewMatcher(nil), repo.RelationshipState{}, TurnInput{
		UserMessage: "我已经不焦虑了，今天还挺轻松",
	}, time.Date(2026, 3, 6, 18, 0, 0, 0, time.UTC))

	if sig.Vulnerability != 0 {
		t.Fatalf("expected negated vulnerability to be 0, got %.2f", sig.Vulnerability)
	}
}

func TestRelationshipSignalsFromTurn_UsesEmbeddingFallback(t *testing.T) {
	t.Parallel()

	matcher := signals.NewMatcher(relationshipStubEmbedder{
		vectors: map[string][]float32{
			"心里像被抽空了一样": {1, 0},
			"心里空落落的":    {1, 0},
		},
	})
	sig := relationshipSignalsFromTurn(context.Background(), matcher, repo.RelationshipState{}, TurnInput{
		UserMessage: "心里像被抽空了一样",
	}, time.Date(2026, 3, 6, 18, 0, 0, 0, time.UTC))

	if sig.Vulnerability <= 0 {
		t.Fatalf("expected embedding fallback vulnerability > 0, got %.2f", sig.Vulnerability)
	}
}

type relationshipStubEmbedder struct {
	vectors map[string][]float32
}

func (s relationshipStubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if vec, ok := s.vectors[text]; ok {
		return vec, nil
	}
	return []float32{0, 1}, nil
}
