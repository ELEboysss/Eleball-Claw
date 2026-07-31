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

// TestOpenAIReasoningEffort 验证 AR-19 P2-3：Thinking.Effort 提升为顶层 reasoning_effort
func TestOpenAIReasoningEffort(t *testing.T) {
	// Effort 设置 -> 顶层 reasoning_effort，thinking 字段保留（兼容 Kimi）
	body, err := marshalOpenAIBody(ChatRequest{
		Model:    "o3",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Thinking: &ThinkingOptions{Effort: "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort 未提升为顶层: got %v", got["reasoning_effort"])
	}
	if thinking, ok := got["thinking"].(map[string]interface{}); !ok || thinking["effort"] != "high" {
		t.Errorf("thinking 字段应保留 effort: %v", got["thinking"])
	}

	// 未设 Effort -> 不含 reasoning_effort
	body2, _ := marshalOpenAIBody(ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	var got2 map[string]interface{}
	_ = json.Unmarshal(body2, &got2)
	if _, ok := got2["reasoning_effort"]; ok {
		t.Errorf("不应下发 reasoning_effort: %v", got2["reasoning_effort"])
	}
}
