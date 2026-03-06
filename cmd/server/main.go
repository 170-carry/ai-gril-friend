package main

import (
	"context"
	"log"
	"time"

	"ai-gf/internal/api"
	"ai-gf/internal/chat"
	"ai-gf/internal/config"
	"ai-gf/internal/embedding"
	"ai-gf/internal/llm"
	"ai-gf/internal/memory"
	"ai-gf/internal/proactive"
	"ai-gf/internal/prompt"
	"ai-gf/internal/repo"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/joho/godotenv"
)

// main 是服务启动入口：加载配置、连接数据库、初始化依赖并启动 HTTP 服务。
func main() {
	// 加载本地 .env，线上环境即使没有该文件也不影响启动。
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	ctx := context.Background()
	pool, err := repo.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database setup failed: %v", err)
	}
	defer pool.Close()

	if err := repo.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	// 装配基础依赖：消息仓库、记忆仓库、LLM Provider、聊天服务。
	chatRepo := repo.NewPGChatMessageRepository(pool)
	outboxRepo := repo.NewPGChatOutboxRepository(pool)
	memoryRepo := repo.NewPGMemoryRepository(pool)
	proactiveRepo := repo.NewPGProactiveRepository(pool)
	provider := llm.NewOpenAIProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.EmbeddingModel)

	// embedding 由统一工厂装配：支持 qwen/openai/hash，并保留 hash 兜底。
	memoryEmbedder := embedding.Build(embedding.Config{
		Provider:     cfg.EmbeddingProvider,
		BaseURL:      cfg.EmbeddingBaseURL,
		APIKey:       cfg.EmbeddingAPIKey,
		Model:        cfg.EmbeddingModel,
		Endpoint:     cfg.EmbeddingEndpoint,
		Dimensions:   cfg.EmbeddingDimensions,
		TextType:     cfg.EmbeddingTextType,
		OutputType:   cfg.EmbeddingOutputType,
		HashFallback: cfg.EmbeddingHashFallback,
		VectorDim:    cfg.EmbeddingVectorDim,
	})

	proactiveService := proactive.NewService(proactiveRepo, proactive.DefaultConfig())
	memoryService := memory.NewService(memoryRepo, memoryEmbedder, proactiveService, memory.DefaultConfig())
	var turnProcessor memory.TurnProcessor
	if cfg.MemoryAsyncEnabled {
		turnProcessor = memory.NewAsyncProcessor(memoryService, memory.AsyncProcessorConfig{
			Workers: cfg.MemoryWorkerCount,
			Queue:   cfg.MemoryQueueSize,
			Timeout: time.Duration(cfg.MemoryJobTimeoutSec) * time.Second,
		})
		defer func() {
			if err := turnProcessor.Close(); err != nil {
				log.Printf("close memory async processor failed: %v", err)
			}
		}()
	}

	chatService := chat.NewService(chatRepo, provider, memoryService, turnProcessor, cfg.LLMTemperature, cfg.MaxHistoryRounds, prompt.BudgetConfig{
		MaxPromptTokens: cfg.PromptMaxTokens,
		ReserveTokens:   cfg.PromptReserveTokens,
		SystemRatio:     cfg.PromptSystemRatio,
		MemoryRatio:     cfg.PromptMemoryRatio,
		HistoryRatio:    cfg.PromptHistoryRatio,
	})

	// 主动消息调度器：后台扫描到期提醒/回访并写入 outbox。
	dispatcher := proactive.NewDispatcher(proactiveRepo, proactive.DispatcherConfig{
		Enabled:      cfg.ProactiveEnabled,
		ScanInterval: time.Duration(cfg.ProactiveScanSec) * time.Second,
		BatchSize:    cfg.ProactiveBatchSize,
		JobTimeout:   6 * time.Second,
		MaxAttempts:  proactive.DefaultConfig().QueueMaxAttempts,
	})
	if dispatcher != nil {
		dispatcher.Start()
		defer dispatcher.Stop()
	}

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})
	// 允许本地调试页直接请求；生产环境可收敛为白名单。
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization",
	}))
	app.Use(recover.New())

	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	api.NewChatHandler(chatService, outboxRepo, proactiveRepo).Register(app)

	log.Printf("server listening on %s", cfg.ServerAddr)
	if err := app.Listen(cfg.ServerAddr); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
