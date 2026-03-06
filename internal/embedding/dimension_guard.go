package embedding

import (
	"context"
	"fmt"

	"ai-gf/internal/memory"
)

// guardEmbedder 用于确保向量维度与数据库 pgvector 列一致。
type guardEmbedder struct {
	next     memory.Embedder
	expected int
}

// newGuardEmbedder 创建维度校验包装器。
func newGuardEmbedder(next memory.Embedder, expected int) memory.Embedder {
	if next == nil || expected <= 0 {
		return next
	}
	return &guardEmbedder{
		next:     next,
		expected: expected,
	}
}

// Embed 调用下游并校验向量维度，避免把错误维度写入 pgvector。
func (g *guardEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	emb, err := g.next.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(emb) != g.expected {
		return nil, fmt.Errorf("embedding dimension mismatch: expected %d, got %d", g.expected, len(emb))
	}
	return emb, nil
}
