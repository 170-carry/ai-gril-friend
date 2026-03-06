package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config 聚合了服务运行所需的全部环境配置。
type Config struct {
	ServerAddr            string
	DatabaseURL           string
	LLMBaseURL            string
	LLMAPIKey             string
	LLMModel              string
	EmbeddingProvider     string
	EmbeddingBaseURL      string
	EmbeddingAPIKey       string
	EmbeddingModel        string
	EmbeddingEndpoint     string
	EmbeddingDimensions   int
	EmbeddingTextType     string
	EmbeddingOutputType   string
	EmbeddingVectorDim    int
	EmbeddingHashFallback bool
	LLMTemperature        float64
	MaxHistoryRounds      int
	PromptMaxTokens       int
	PromptReserveTokens   int
	PromptSystemRatio     int
	PromptMemoryRatio     int
	PromptHistoryRatio    int
	MemoryAsyncEnabled    bool
	MemoryWorkerCount     int
	MemoryQueueSize       int
	MemoryJobTimeoutSec   int
	ProactiveEnabled      bool
	ProactiveScanSec      int
	ProactiveBatchSize    int
}

// Load 从环境变量读取配置，并校验关键字段是否齐全。
func Load() (Config, error) {
	llmBaseURL := getenv("LLM_BASE_URL", "https://api.openai.com/v1")
	llmAPIKey := os.Getenv("LLM_API_KEY")
	embeddingAPIKey := getenv("EMBEDDING_API_KEY", getenv("EMBEDDING_KEY", llmAPIKey))

	cfg := Config{
		ServerAddr:            getenv("SERVER_ADDR", ":9901"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		LLMBaseURL:            llmBaseURL,
		LLMAPIKey:             llmAPIKey,
		LLMModel:              getenv("LLM_MODEL", "gpt-4o-mini"),
		EmbeddingProvider:     getenv("EMBEDDING_PROVIDER", "openai_compatible"),
		EmbeddingBaseURL:      getenv("EMBEDDING_BASE_URL", llmBaseURL),
		EmbeddingAPIKey:       embeddingAPIKey,
		EmbeddingModel:        getenv("EMBEDDING_MODEL", "text-embedding-3-small"),
		EmbeddingEndpoint:     getenv("EMBEDDING_ENDPOINT", ""),
		EmbeddingDimensions:   getInt("EMBEDDING_DIMENSIONS", 1536),
		EmbeddingTextType:     getenv("EMBEDDING_TEXT_TYPE", "document"),
		EmbeddingOutputType:   getenv("EMBEDDING_OUTPUT_TYPE", "dense"),
		EmbeddingVectorDim:    getInt("EMBEDDING_VECTOR_DIM", 1536),
		EmbeddingHashFallback: getBool("EMBEDDING_HASH_FALLBACK", true),
		LLMTemperature:        getFloat("LLM_TEMPERATURE", 0.8),
		MaxHistoryRounds:      getInt("MAX_HISTORY_ROUNDS", 20),
		PromptMaxTokens:       getInt("PROMPT_MAX_TOKENS", 3200),
		PromptReserveTokens:   getInt("PROMPT_RESERVE_TOKENS", 800),
		PromptSystemRatio:     getInt("PROMPT_BUDGET_SYSTEM_RATIO", 35),
		PromptMemoryRatio:     getInt("PROMPT_BUDGET_MEMORY_RATIO", 25),
		PromptHistoryRatio:    getInt("PROMPT_BUDGET_HISTORY_RATIO", 40),
		MemoryAsyncEnabled:    getBool("MEMORY_ASYNC_ENABLED", true),
		MemoryWorkerCount:     getInt("MEMORY_WORKER_COUNT", 2),
		MemoryQueueSize:       getInt("MEMORY_QUEUE_SIZE", 256),
		MemoryJobTimeoutSec:   getInt("MEMORY_JOB_TIMEOUT_SEC", 8),
		ProactiveEnabled:      getBool("PROACTIVE_ENABLED", true),
		ProactiveScanSec:      getInt("PROACTIVE_SCAN_SEC", 10),
		ProactiveBatchSize:    getInt("PROACTIVE_BATCH_SIZE", 20),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.LLMAPIKey == "" {
		return Config{}, fmt.Errorf("LLM_API_KEY is required")
	}

	return cfg, nil
}

// getenv 读取字符串环境变量，不存在时返回默认值。
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getInt 读取整型环境变量，解析失败时回退到默认值。
func getInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// getFloat 读取浮点环境变量，解析失败时回退到默认值。
func getFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return n
}

// getBool 读取布尔环境变量，解析失败时回退默认值。
func getBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
