package llm

import (
	"testing"
)

func TestEstimateTokenCount(t *testing.T) {
	tests := []struct {
		name  string
		input string
		min   int
		max   int
	}{
		{"empty", "", 0, 0},
		{"english short", "hello world", 2, 4},
		{"english long", "The quick brown fox jumps over the lazy dog.", 10, 15},
		{"chinese short", "你好世界", 4, 4},
		{"chinese long", "今天天气不错，我们去公园散步吧。", 14, 18},
		{"mixed", "Hello 你好 world 世界", 6, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokenCount(tt.input)
			if got < tt.min || got > tt.max {
				t.Fatalf("EstimateTokenCount(%q) = %d, want between %d and %d", tt.input, got, tt.min, tt.max)
			}
		})
	}
}

func TestEstimateUsageFromMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "你好"},
	}
	completion := "你好！很高兴为你服务。"

	usage := EstimateUsageFromMessages(msgs, completion)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens <= 0 {
		t.Fatalf("expected positive prompt tokens, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens <= 0 {
		t.Fatalf("expected positive completion tokens, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("total tokens mismatch: %d != %d + %d", usage.TotalTokens, usage.PromptTokens, usage.CompletionTokens)
	}
}

func TestEstimateUsageFromMessagesWithContentParts(t *testing.T) {
	msgs := []Message{
		{
			Role: "user",
			Content: []ContentPart{
				{Type: "text", Text: "解释一下这张图片"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAA"}},
			},
		},
	}

	usage := EstimateUsageFromMessages(msgs, "这是一只猫。")
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens <= 0 {
		t.Fatalf("expected positive prompt tokens for content parts, got %d", usage.PromptTokens)
	}
}
