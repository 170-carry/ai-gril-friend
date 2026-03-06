package llm

import "context"

// Message 是发送给 LLM 的标准消息单元。
type Message struct {
	Role    string
	Content string
}

// GenerateRequest 描述一次文本生成请求。
type GenerateRequest struct {
	Messages    []Message
	Temperature float64
}

// Provider 抽象了聊天生成与嵌入能力，便于替换不同模型供应商。
type Provider interface {
	// StreamGenerate 流式返回增量 token。
	StreamGenerate(ctx context.Context, req GenerateRequest, onDelta func(delta string) error) error
	// Generate 一次性返回完整文本。
	Generate(ctx context.Context, req GenerateRequest) (string, error)
	// Embed 返回向量表示；当前阶段可先留空实现。
	Embed(ctx context.Context, text string) ([]float32, error)
}
