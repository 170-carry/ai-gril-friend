package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// QwenEmbeddingProvider 基于百炼“通用文本向量同步接口”实现 embedding。
// 文档：https://help.aliyun.com/zh/model-studio/text-embedding-synchronous-api
type QwenEmbeddingProvider struct {
	baseURL    string
	apiKey     string
	model      string
	endpoint   string
	dimension  int
	textType   string
	outputType string
	client     *http.Client
}

// NewQwenEmbeddingProvider 创建千问同步 embedding provider。
func NewQwenEmbeddingProvider(baseURL, apiKey, model, endpoint string, dimension int, textType, outputType string) *QwenEmbeddingProvider {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs.com"
	}

	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "/api/v1/services/embeddings/text-embedding/text-embedding"
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") && !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}

	model = strings.TrimSpace(model)
	if model == "" {
		model = "text-embedding-v4"
	}

	textType = strings.TrimSpace(textType)
	if textType == "" {
		textType = "document"
	}

	outputType = strings.TrimSpace(outputType)
	if outputType == "" {
		outputType = "dense"
	}

	return &QwenEmbeddingProvider{
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(apiKey),
		model:      model,
		endpoint:   endpoint,
		dimension:  dimension,
		textType:   textType,
		outputType: outputType,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

type qwenEmbeddingRequest struct {
	Model      string                 `json:"model"`
	Input      qwenEmbeddingInput     `json:"input"`
	Parameters qwenEmbeddingParameter `json:"parameters,omitempty"`
}

type qwenEmbeddingInput struct {
	Texts []string `json:"texts"`
}

type qwenEmbeddingParameter struct {
	TextType   string `json:"text_type,omitempty"`
	Dimension  int    `json:"dimension,omitempty"`
	OutputType string `json:"output_type,omitempty"`
}

type qwenEmbeddingResponse struct {
	StatusCode int    `json:"status_code"`
	RequestID  string `json:"request_id"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Output     struct {
		Embeddings []struct {
			TextIndex int       `json:"text_index"`
			Embedding []float32 `json:"embedding"`
		} `json:"embeddings"`
	} `json:"output"`
}

type qwenErrorResponse struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

// Embed 调用千问同步 embedding 接口，返回首条 dense 向量。
func (p *QwenEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("embed input is empty")
	}

	reqBody := qwenEmbeddingRequest{
		Model: p.model,
		Input: qwenEmbeddingInput{
			Texts: []string{text},
		},
		Parameters: qwenEmbeddingParameter{
			TextType:   p.textType,
			Dimension:  p.dimension,
			OutputType: p.outputType,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal qwen embedding request: %w", err)
	}

	url := p.endpoint
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = p.baseURL + url
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read qwen embedding response: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, p.parseHTTPError(resp.StatusCode, raw)
	}

	var out qwenEmbeddingResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode qwen embedding response: %w", err)
	}
	if out.Code != "" {
		return nil, fmt.Errorf("qwen embedding error (%s): %s", out.Code, strings.TrimSpace(out.Message))
	}
	if len(out.Output.Embeddings) == 0 {
		return nil, fmt.Errorf("empty qwen embeddings")
	}

	embedding := out.Output.Embeddings[0].Embedding
	if len(embedding) == 0 {
		return nil, fmt.Errorf("empty qwen dense embedding")
	}
	return embedding, nil
}

func (p *QwenEmbeddingProvider) parseHTTPError(statusCode int, body []byte) error {
	var qwenErr qwenErrorResponse
	if err := json.Unmarshal(body, &qwenErr); err == nil && (qwenErr.Code != "" || qwenErr.Message != "") {
		if qwenErr.RequestID != "" {
			return fmt.Errorf("qwen embedding API error (%d/%s): %s (request_id=%s)", statusCode, qwenErr.Code, strings.TrimSpace(qwenErr.Message), qwenErr.RequestID)
		}
		return fmt.Errorf("qwen embedding API error (%d/%s): %s", statusCode, qwenErr.Code, strings.TrimSpace(qwenErr.Message))
	}
	return fmt.Errorf("qwen embedding API error (%d): %s", statusCode, strings.TrimSpace(string(body)))
}
