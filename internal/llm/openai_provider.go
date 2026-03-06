package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider 基于 OpenAI 兼容接口实现 Provider。
type OpenAIProvider struct {
	baseURL        string
	apiKey         string
	model          string
	embeddingModel string
	client         *http.Client
}

// NewOpenAIProvider 创建一个可复用的 Provider 实例。
func NewOpenAIProvider(baseURL, apiKey, model, embeddingModel string) *OpenAIProvider {
	embeddingModel = strings.TrimSpace(embeddingModel)
	if embeddingModel == "" {
		embeddingModel = "text-embedding-3-small"
	}
	return &OpenAIProvider{
		baseURL:        strings.TrimRight(baseURL, "/"),
		apiKey:         apiKey,
		model:          model,
		embeddingModel: embeddingModel,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// chatCompletionRequest 是 /chat/completions 接口请求体。
type chatCompletionRequest struct {
	Model       string               `json:"model"`
	Messages    []chatMessagePayload `json:"messages"`
	Temperature float64              `json:"temperature,omitempty"`
	Stream      bool                 `json:"stream,omitempty"`
}

// chatMessagePayload 是请求中的单条消息结构。
type chatMessagePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletionResponse 是非流式响应结构。
type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// chatCompletionChunk 是流式 SSE 每块数据的结构。
type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// apiErrorResponse 统一承接上游 API 的错误返回。
type apiErrorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// embeddingRequest 是 /embeddings 接口请求体。
type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embeddingResponse 是 /embeddings 接口响应体。
type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// StreamGenerate 调用流式 completions，并将增量内容回调给调用方。
func (p *OpenAIProvider) StreamGenerate(ctx context.Context, req GenerateRequest, onDelta func(delta string) error) error {
	payload := chatCompletionRequest{
		Model:       p.model,
		Messages:    toPayloadMessages(req.Messages),
		Temperature: req.Temperature,
		Stream:      true,
	}

	httpReq, err := p.newChatCompletionRequest(ctx, payload)
	if err != nil {
		return err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return p.parseHTTPError(resp)
	}

	// 逐行读取 SSE，提取 data: 前缀内容并解析 JSON chunk。
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode stream chunk: %w", err)
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			if err := onDelta(choice.Delta.Content); err != nil {
				return err
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}

	return nil
}

// Generate 调用非流式 completions，返回完整文本。
func (p *OpenAIProvider) Generate(ctx context.Context, req GenerateRequest) (string, error) {
	payload := chatCompletionRequest{
		Model:       p.model,
		Messages:    toPayloadMessages(req.Messages),
		Temperature: req.Temperature,
		Stream:      false,
	}

	httpReq, err := p.newChatCompletionRequest(ctx, payload)
	if err != nil {
		return "", err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", p.parseHTTPError(resp)
	}

	var completion chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return "", fmt.Errorf("decode completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("empty completion choices")
	}

	return completion.Choices[0].Message.Content, nil
}

// Embed 调用 OpenAI 兼容 embeddings 接口并返回首条向量。
func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("embed input is empty")
	}

	body, err := json.Marshal(embeddingRequest{
		Model: p.embeddingModel,
		Input: []string{text},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	httpReq, err := p.newJSONRequest(ctx, http.MethodPost, p.baseURL+"/embeddings", body)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, p.parseHTTPError(resp)
	}

	var out embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding result")
	}
	return out.Data[0].Embedding, nil
}

// newChatCompletionRequest 负责构建并签名 HTTP 请求。
func (p *OpenAIProvider) newChatCompletionRequest(ctx context.Context, payload chatCompletionRequest) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return p.newJSONRequest(ctx, http.MethodPost, p.baseURL+"/chat/completions", body)
}

// newJSONRequest 构建带鉴权头的 JSON HTTP 请求。
func (p *OpenAIProvider) newJSONRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	return httpReq, nil
}

// parseHTTPError 尝试解析上游标准错误体，否则回退为原始文本。
func (p *OpenAIProvider) parseHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	var apiErr apiErrorResponse
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error.Message != "" {
		return fmt.Errorf("llm API error (%d): %s", resp.StatusCode, apiErr.Error.Message)
	}

	return fmt.Errorf("llm API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// toPayloadMessages 将内部消息结构转换为 API 请求结构。
func toPayloadMessages(messages []Message) []chatMessagePayload {
	out := make([]chatMessagePayload, 0, len(messages))
	for _, m := range messages {
		out = append(out, chatMessagePayload{Role: m.Role, Content: m.Content})
	}
	return out
}
