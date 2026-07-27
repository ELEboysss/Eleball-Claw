package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNormalizeUsage_OpenAIFields(t *testing.T) {
	usage := normalizeUsage(&Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      0,
	}, 0, 0)

	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 5 || usage.TotalTokens != 15 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestNormalizeUsage_AliasFields(t *testing.T) {
	usage := normalizeUsage(nil, 12, 8)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 8 || usage.TotalTokens != 20 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestNormalizeUsage_NoUsage(t *testing.T) {
	usage := normalizeUsage(nil, 0, 0)
	if usage != nil {
		t.Fatalf("expected nil usage, got %+v", usage)
	}
}

// TestOpenAIClient_Embed 验证 /embeddings 请求路径、请求体与响应解析（AR-09）
func TestOpenAIClient_Embed(t *testing.T) {
	var gotBody EmbeddingRequest
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EmbeddingResponse{
			Data: []EmbeddingItem{
				{Embedding: []float32{0.1, 0.2, 0.3}},
				{Embedding: []float32{0.4, 0.5, 0.6}},
			},
		})
	}))
	defer srv.Close()

	c := NewOpenAIClient("test-key", srv.URL, 5*time.Second)
	vecs, err := c.Embed(context.Background(), "text-embed-3", []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 3 || vecs[0][0] != 0.1 {
		t.Fatalf("unexpected first vector: %v", vecs[0])
	}
	if gotBody.Model != "text-embed-3" || len(gotBody.Input) != 2 {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}

	// model 为空 -> error
	if _, err := c.Embed(context.Background(), "", []string{"a"}); err == nil {
		t.Fatal("expected error for empty model")
	}
	// inputs 为空 -> nil, nil
	got, err := c.Embed(context.Background(), "m", nil)
	if err != nil || got != nil {
		t.Fatalf("expected nil/nil for empty inputs, got %v/%v", got, err)
	}
}
