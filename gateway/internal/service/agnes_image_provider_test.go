package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgnesImageProviderCreate_QueueFull(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":{"message":"image queue is full, please retry later"}}`))
	}))
	defer server.Close()

	provider := NewAgnesImageProvider(server.URL, "test-key")
	_, err := provider.Create(context.Background(), &VisualCreateRequest{
		Model:  "agnes-image-test",
		Prompt: "test prompt",
	})
	if !errors.Is(err, UpstreamQueueFullError) {
		t.Fatalf("期望 UpstreamQueueFullError，实际 %v", err)
	}
}

func TestAgnesImageProviderCreate_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	provider := NewAgnesImageProvider(server.URL, "test-key")
	_, err := provider.Create(context.Background(), &VisualCreateRequest{
		Model:  "agnes-image-test",
		Prompt: "test prompt",
	})
	if !errors.Is(err, UpstreamRateLimitedError) {
		t.Fatalf("期望 UpstreamRateLimitedError，实际 %v", err)
	}
}

func TestAgnesImageProviderCreate_Other503(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":{"message":"upstream unavailable"}}`))
	}))
	defer server.Close()

	provider := NewAgnesImageProvider(server.URL, "test-key")
	_, err := provider.Create(context.Background(), &VisualCreateRequest{
		Model:  "agnes-image-test",
		Prompt: "test prompt",
	})
	if errors.Is(err, UpstreamQueueFullError) {
		t.Fatal("非队列满 503 不应返回 UpstreamQueueFullError")
	}
	if err == nil {
		t.Fatal("应返回错误")
	}
}
