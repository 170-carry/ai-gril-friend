package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"ai-gf/internal/config"
	"ai-gf/internal/embedding"
	"ai-gf/internal/repo"

	"github.com/joho/godotenv"
)

// main 是 embedding 连通性自检命令：
// 1) 调 embedding 接口获取向量
// 2) 可选把向量作为 pgvector 参数做一次维度/距离校验
func main() {
	_ = godotenv.Load()

	text := flag.String("text", "今天有点焦虑，想找你聊聊。", "要转换为向量的文本")
	checkDB := flag.Bool("check-db", true, "是否校验向量可被数据库 pgvector 正常接收")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	embedder := embedding.Build(embedding.Config{
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

	ctx := context.Background()
	vector, err := embedder.Embed(ctx, *text)
	if err != nil {
		log.Fatalf("embedding failed: %v", err)
	}
	log.Printf("embedding ok: dim=%d preview=%s", len(vector), preview(vector, 8))

	if !*checkDB {
		log.Printf("skip db check")
		return
	}

	pool, err := repo.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database connect failed: %v", err)
	}
	defer pool.Close()

	// 这里用“向量自比较”做最小验证：同一向量的 cosine 距离应为 0。
	var (
		dbDims       int
		selfDistance float64
	)
	if err := pool.QueryRow(ctx, `SELECT vector_dims($1::vector), ($1::vector <=> $1::vector)`, vectorLiteral(vector)).Scan(&dbDims, &selfDistance); err != nil {
		log.Fatalf("pgvector check failed: %v", err)
	}
	log.Printf("pgvector check ok: db_dims=%d self_distance=%.6f", dbDims, selfDistance)
}

// preview 仅用于日志展示，避免打印过长向量。
func preview(vec []float32, n int) string {
	if n <= 0 {
		n = 4
	}
	if len(vec) <= n {
		return fmt.Sprintf("%v", vec)
	}
	return fmt.Sprintf("%v ...", vec[:n])
}

// vectorLiteral 把 float32 向量转换为 pgvector 的文本字面量。
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
