package memory

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// HashEmbedder 是本地可用的确定性 embedding 兜底实现。
// 说明：它不是语义模型向量，但可在无第三方 embedding 接口时提供稳定召回能力。
type HashEmbedder struct {
	dim int
}

// NewHashEmbedder 创建 hash embedding 实现，默认维度 1536 以匹配当前 pgvector 列定义。
func NewHashEmbedder(dim int) *HashEmbedder {
	if dim <= 0 {
		dim = 1536
	}
	return &HashEmbedder{dim: dim}
}

// Embed 将文本映射为固定维度向量，并做 L2 归一化。
func (e *HashEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	_ = ctx
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("hash embed input is empty")
	}
	tokens := tokenizeForHashEmbedding(text)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("hash embed tokens is empty")
	}

	vec := make([]float32, e.dim)
	for _, token := range tokens {
		hash := sha256.Sum256([]byte(token))
		index := int(binary.BigEndian.Uint32(hash[:4]) % uint32(e.dim))
		sign := float32(1)
		if hash[4]&0x01 == 1 {
			sign = -1
		}
		weight := float32(1+int(hash[5]%7)) / 7.0
		vec[index] += sign * weight
	}

	normalizeL2(vec)
	return vec, nil
}

func tokenizeForHashEmbedding(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	replacer := strings.NewReplacer(
		"\n", " ",
		"\t", " ",
		"，", " ",
		"。", " ",
		"、", " ",
		",", " ",
		";", " ",
		"；", " ",
		":", " ",
		"：", " ",
	)
	text = replacer.Replace(text)

	parts := strings.Fields(text)
	out := make([]string, 0, len(parts)*2)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
		runes := []rune(part)
		if len(runes) < 2 {
			continue
		}
		for i := 0; i+1 < len(runes); i++ {
			out = append(out, string(runes[i:i+2]))
		}
	}
	if len(out) > 0 {
		return out
	}

	// 兜底：整句 2-gram。
	runes := []rune(strings.ReplaceAll(text, " ", ""))
	if len(runes) < 2 {
		if len(runes) == 1 {
			return []string{string(runes[0])}
		}
		return nil
	}
	for i := 0; i+1 < len(runes); i++ {
		out = append(out, string(runes[i:i+2]))
	}
	return out
}

func normalizeL2(vec []float32) {
	if len(vec) == 0 {
		return
	}
	var sum float64
	for _, v := range vec {
		sum += float64(v * v)
	}
	if sum <= 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	if norm == 0 {
		return
	}
	for i := range vec {
		vec[i] /= norm
	}
}
