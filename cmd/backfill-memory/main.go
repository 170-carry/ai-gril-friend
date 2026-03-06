package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"ai-gf/internal/config"
	"ai-gf/internal/embedding"
	"ai-gf/internal/llm"
	"ai-gf/internal/memory"
	"ai-gf/internal/repo"
	"ai-gf/internal/signals"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// backfillChatPair 表示一组历史 user/assistant 消息配对。
type backfillChatPair struct {
	userID      string
	sessionID   string
	userMsgID   int64
	assistantID int64
	userMsg     string
	assistant   string
	createdAt   time.Time
}

type chatRow struct {
	id        int64
	userID    string
	sessionID string
	role      string
	content   string
	createdAt time.Time
}

func main() {
	_ = godotenv.Load()

	var (
		doExtract = flag.Bool("extract", true, "是否从历史 chat_messages 回填 structured/semantic memory")
		doEmbed   = flag.Bool("embed", true, "是否为缺失 embedding 的 memory_chunks 回填向量")
		limit     = flag.Int("limit", 0, "仅处理前 N 条（0 表示全部）")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	ctx := context.Background()
	pool, err := repo.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database connect failed: %v", err)
	}
	defer pool.Close()

	if err := repo.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("run migrations failed: %v", err)
	}

	memoryRepo := repo.NewPGMemoryRepository(pool)
	embedder := buildEmbedder(cfg)
	memoryService := memory.NewService(memoryRepo, embedder, nil, memory.DefaultConfig())
	provider := llm.NewOpenAIProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.EmbeddingModel)
	memoryService.UseSignalAnalyzer(signals.NewLLMAnalyzer(provider))

	if *doExtract {
		if err := backfillFromChatHistory(ctx, pool, memoryService, *limit); err != nil {
			log.Fatalf("backfill from chat history failed: %v", err)
		}
	}
	if *doEmbed {
		if err := backfillMemoryChunkEmbeddings(ctx, pool, embedder, *limit); err != nil {
			log.Fatalf("backfill embeddings failed: %v", err)
		}
	}

	log.Printf("backfill completed")
}

func buildEmbedder(cfg config.Config) memory.Embedder {
	return embedding.Build(embedding.Config{
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
}

// backfillFromChatHistory 遍历历史消息，按 user->assistant 配对回放到记忆抽取器。
func backfillFromChatHistory(ctx context.Context, pool *pgxpool.Pool, svc *memory.Service, limit int) error {
	query := `SELECT id, user_id, session_id, role, content, created_at
		FROM chat_messages
		WHERE role IN ('user', 'assistant')
		ORDER BY user_id ASC, session_id ASC, created_at ASC, id ASC`
	if limit > 0 {
		query += " LIMIT " + fmt.Sprintf("%d", limit)
	}

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("query chat messages: %w", err)
	}
	defer rows.Close()

	pending := map[string]chatRow{}
	pairs := 0
	failures := 0
	for rows.Next() {
		var row chatRow
		if err := rows.Scan(&row.id, &row.userID, &row.sessionID, &row.role, &row.content, &row.createdAt); err != nil {
			return fmt.Errorf("scan chat row: %w", err)
		}
		key := row.userID + "|" + row.sessionID
		switch row.role {
		case "user":
			pending[key] = row
		case "assistant":
			u, ok := pending[key]
			if !ok {
				continue
			}
			pair := backfillChatPair{
				userID:      row.userID,
				sessionID:   row.sessionID,
				userMsgID:   u.id,
				assistantID: row.id,
				userMsg:     u.content,
				assistant:   row.content,
				createdAt:   row.createdAt,
			}
			delete(pending, key)

			if pair.createdAt.IsZero() {
				pair.createdAt = time.Now()
			}
			if err := svc.ProcessTurn(ctx, memory.TurnInput{
				UserID:              pair.userID,
				SessionID:           pair.sessionID,
				UserMessageID:       pair.userMsgID,
				AssistantMessageID:  pair.assistantID,
				UserMessage:         pair.userMsg,
				AssistantMessage:    pair.assistant,
				ConversationContext: "",
				Now:                 pair.createdAt,
			}); err != nil {
				failures++
				log.Printf("history extract failed user=%s session=%s user_msg_id=%d: %v", pair.userID, pair.sessionID, pair.userMsgID, err)
				continue
			}
			pairs++
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate chat messages: %w", err)
	}

	log.Printf("history extract done: pairs=%d failures=%d", pairs, failures)
	return nil
}

// backfillMemoryChunkEmbeddings 为缺失 embedding 的语义记忆逐条补向量。
func backfillMemoryChunkEmbeddings(ctx context.Context, pool *pgxpool.Pool, embedder memory.Embedder, limit int) error {
	query := `SELECT id, user_id, content
		FROM memory_chunks
		WHERE embedding IS NULL
		ORDER BY id ASC`
	if limit > 0 {
		query += " LIMIT " + fmt.Sprintf("%d", limit)
	}

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("query memory_chunks for embedding backfill: %w", err)
	}
	defer rows.Close()

	updated := 0
	failed := 0
	for rows.Next() {
		var (
			id      int64
			userID  string
			content string
		)
		if err := rows.Scan(&id, &userID, &content); err != nil {
			return fmt.Errorf("scan memory chunk: %w", err)
		}
		emb, err := embedder.Embed(ctx, content)
		if err != nil || len(emb) == 0 {
			failed++
			log.Printf("embed backfill failed chunk_id=%d user=%s: %v", id, userID, err)
			continue
		}
		if _, err := pool.Exec(ctx, `UPDATE memory_chunks
			SET embedding = $2::vector, updated_at = NOW()
			WHERE id = $1`, id, vectorLiteral(emb)); err != nil {
			failed++
			log.Printf("update embedding failed chunk_id=%d user=%s: %v", id, userID, err)
			continue
		}
		updated++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate memory chunk rows: %w", err)
	}

	log.Printf("embedding backfill done: updated=%d failed=%d", updated, failed)
	return nil
}

// vectorLiteral 把 float32 向量转换为 pgvector 接收的字符串。
func vectorLiteral(embedding []float32) string {
	if len(embedding) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(embedding))
	for _, v := range embedding {
		parts = append(parts, fmt.Sprintf("%g", v))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
