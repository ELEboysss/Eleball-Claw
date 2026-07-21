package llm

import (
	"testing"
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
