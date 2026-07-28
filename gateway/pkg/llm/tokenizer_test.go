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

// TestEstimateUsageFromMessagesBuffer 验证兜底估算叠加安全垫（AR-19 P2-1）。
func TestEstimateUsageFromMessagesBuffer(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hello"}}
	usage := EstimateUsageFromMessages(msgs, "hi")
	if usage.PromptTokens < usageTokenBuffer {
		t.Fatalf("expected prompt tokens >= buffer %d, got %d", usageTokenBuffer, usage.PromptTokens)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("total mismatch: %d != %d + %d", usage.TotalTokens, usage.PromptTokens, usage.CompletionTokens)
	}
}

// TestEstimateUsageFromMessagesForModelCoefficient 验证按模型族估算系数（AR-19 P2-1）。
func TestEstimateUsageFromMessagesForModelCoefficient(t *testing.T) {
	code := "package main\nimport \"fmt\"\nfunc main() {\n\tfor i := 0; i < 100; i++ {\n\t\tfmt.Printf(\"item %d\\n\", i)\n\t\tif i%2 == 0 { continue }\n\t}\n}"
	msgs := []Message{{Role: "user", Content: code}}
	completion := "return result"
	// 代码模型系数 1.2 > 默认 1.0，prompt 估算应更高（buffer 相同，差异来自系数）
	base := EstimateUsageFromMessagesForModel(msgs, completion, "gpt-4o")
	coder := EstimateUsageFromMessagesForModel(msgs, completion, "deepseek-coder")
	if coder.PromptTokens <= base.PromptTokens {
		t.Fatalf("coder model estimate should exceed base: coder=%d base=%d", coder.PromptTokens, base.PromptTokens)
	}
}
