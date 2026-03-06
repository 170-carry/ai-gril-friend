package embedding

import (
	"context"
	"errors"
	"testing"
)

type fakeEmbedder struct {
	vector []float32
	err    error
}

func (f fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.vector, nil
}

func TestGuardEmbedder_DimensionMismatch(t *testing.T) {
	t.Parallel()

	g := newGuardEmbedder(fakeEmbedder{vector: []float32{1, 2, 3}}, 5)
	_, err := g.Embed(context.Background(), "x")
	if err == nil {
		t.Fatalf("expect dimension mismatch error")
	}
}

func TestGuardEmbedder_PassThroughError(t *testing.T) {
	t.Parallel()

	innerErr := errors.New("boom")
	g := newGuardEmbedder(fakeEmbedder{err: innerErr}, 3)
	_, err := g.Embed(context.Background(), "x")
	if err == nil || err.Error() != innerErr.Error() {
		t.Fatalf("expect passthrough error, got: %v", err)
	}
}
