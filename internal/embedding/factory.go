package embedding

import (
	"strings"

	"ai-gf/internal/llm"
	"ai-gf/internal/memory"
)

// Config 描述 embedding provider 装配参数。
type Config struct {
	Provider     string
	BaseURL      string
	APIKey       string
	Model        string
	Endpoint     string
	Dimensions   int
	TextType     string
	OutputType   string
	HashFallback bool
	VectorDim    int
}

// Build 根据配置构建 embedding 实现。
// 设计目标：
// 1) 把 provider 选择逻辑集中在一处，后续新增本地模型时只需补一个 case。
// 2) 保留 hash 兜底，避免外部 embedding 接口偶发不可用导致主流程失败。
func Build(cfg Config) memory.Embedder {
	vectorDim := cfg.VectorDim
	if vectorDim <= 0 {
		vectorDim = 1536
	}

	hashEmbedder := newGuardEmbedder(memory.NewHashEmbedder(vectorDim), vectorDim)
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch provider {
	case "hash", "hashed", "local_hash", "local":
		return hashEmbedder
	case "qwen", "qwen_sync", "dashscope_sync":
		qwenDimension := cfg.Dimensions
		if qwenDimension <= 0 {
			qwenDimension = vectorDim
		}
		primary := newGuardEmbedder(llm.NewQwenEmbeddingProvider(
			cfg.BaseURL,
			cfg.APIKey,
			cfg.Model,
			cfg.Endpoint,
			qwenDimension,
			cfg.TextType,
			cfg.OutputType,
		), vectorDim)
		if cfg.HashFallback {
			return memory.NewFallbackEmbedder(primary, hashEmbedder)
		}
		return primary
	default:
		primary := newGuardEmbedder(llm.NewOpenAIProvider(cfg.BaseURL, cfg.APIKey, "", cfg.Model), vectorDim)
		if cfg.HashFallback {
			return memory.NewFallbackEmbedder(primary, hashEmbedder)
		}
		return primary
	}
}
