package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQwenEmbeddingProvider_Embed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/services/embeddings/text-embedding/text-embedding" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %s", got)
		}

		var req qwenEmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "text-embedding-v4" {
			t.Fatalf("unexpected model: %s", req.Model)
		}
		if len(req.Input.Texts) != 1 || req.Input.Texts[0] != "hello qwen" {
			t.Fatalf("unexpected input: %+v", req.Input.Texts)
		}
		if req.Parameters.Dimension != 1536 {
			t.Fatalf("unexpected dimension: %d", req.Parameters.Dimension)
		}
		if req.Parameters.TextType != "document" {
			t.Fatalf("unexpected text_type: %s", req.Parameters.TextType)
		}
		if req.Parameters.OutputType != "dense" {
			t.Fatalf("unexpected output_type: %s", req.Parameters.OutputType)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{
				"embeddings": []map[string]any{
					{
						"text_index": 0,
						"embedding":  []float32{0.1, 0.2, 0.3},
					},
				},
			},
		})
	}))
	defer server.Close()

	p := NewQwenEmbeddingProvider(
		server.URL,
		"test-key",
		"text-embedding-v4",
		"/api/v1/services/embeddings/text-embedding/text-embedding",
		1536,
		"document",
		"dense",
	)

	vec, err := p.Embed(context.Background(), "hello qwen")
	if err != nil {
		t.Fatalf("embed failed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("unexpected embedding length: %d", len(vec))
	}
}

func TestQwenEmbeddingProvider_EmbedHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id": "req-001",
			"code":       "InvalidApiKey",
			"message":    "api key is invalid",
		})
	}))
	defer server.Close()

	p := NewQwenEmbeddingProvider(server.URL, "bad-key", "text-embedding-v4", "/api/v1/services/embeddings/text-embedding/text-embedding", 1536, "document", "dense")
	_, err := p.Embed(context.Background(), "hello qwen")
	if err == nil {
		t.Fatalf("expect error, got nil")
	}
	if got := err.Error(); got == "" || got == "<nil>" {
		t.Fatalf("unexpected error message: %v", err)
	}
}
