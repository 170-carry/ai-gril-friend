package memory

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
)

// FallbackEmbedder 支持主 embedding 与兜底 embedding 的自动切换。
type FallbackEmbedder struct {
	primary     Embedder
	secondary   Embedder
	disableMain atomic.Bool
}

// NewFallbackEmbedder 创建主备 embedding 组合。
func NewFallbackEmbedder(primary Embedder, secondary Embedder) *FallbackEmbedder {
	return &FallbackEmbedder{
		primary:   primary,
		secondary: secondary,
	}
}

// Embed 先尝试主 embedding；若失败且命中“永久错误”特征，则熔断主通道并切换兜底。
func (e *FallbackEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	var primaryErr error
	if e.primary != nil && !e.disableMain.Load() {
		emb, err := e.primary.Embed(ctx, text)
		if err == nil && len(emb) > 0 {
			return emb, nil
		}
		primaryErr = err
		if isPermanentEmbeddingError(err) {
			e.disableMain.Store(true)
		}
	}

	if e.secondary != nil {
		emb, err := e.secondary.Embed(ctx, text)
		if err == nil && len(emb) > 0 {
			return emb, nil
		}
		if primaryErr != nil {
			return nil, fmt.Errorf("primary embed failed: %v; secondary embed failed: %w", primaryErr, err)
		}
		return nil, err
	}

	if primaryErr != nil {
		return nil, primaryErr
	}
	return nil, fmt.Errorf("no embedding provider available")
}

func isPermanentEmbeddingError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	// 这类错误通常不会通过重试恢复，直接熔断主通道更稳。
	permanentHints := []string{
		"404",
		"not found",
		"not implemented",
		"unsupported",
		"does not exist",
		"invalid model",
		"unknown model",
		"model",
		"embeddings",
	}
	hit := 0
	for _, hint := range permanentHints {
		if strings.Contains(text, hint) {
			hit++
		}
	}
	return hit >= 2
}
