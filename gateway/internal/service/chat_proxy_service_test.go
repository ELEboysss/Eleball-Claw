package service

import (
	"context"
	"testing"

	"github.com/eleball/gateway/pkg/llm"
	"github.com/stretchr/testify/assert"
)

// mockLLMClient 模拟 LLM 客户端
type mockLLMClient struct {
	response string
	usage    *llm.Usage
	err      error
}

func (m *mockLLMClient) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
	return &llm.ChatChunk{Delta: m.response, Usage: m.usage}, m.err
}

func (m *mockLLMClient) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	return nil, nil
}

func TestChatProxyService_RegisterAndChat(t *testing.T) {
	svc := NewChatProxyService(nil, nil, NewNoOpEleAgentModelService(), nil)

	mock := &mockLLMClient{
		response: "你好，我是 AI 助手",
		usage: &llm.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	svc.RegisterFallbackClient(llm.Provider("mock"), mock)

	chunk, err := svc.Chat(context.Background(), &ChatRequest{
		Provider: "mock",
		Model:    "mock-model",
		Messages: []llm.Message{
			{Role: "user", Content: "你好"},
		},
	})

	assert.NoError(t, err)
	assert.NotNil(t, chunk)
	assert.Equal(t, "你好，我是 AI 助手", chunk.Delta)
	assert.NotNil(t, chunk.Usage)
	assert.Equal(t, 15, chunk.Usage.TotalTokens)
}

func TestChatProxyService_UnsupportedProvider(t *testing.T) {
	svc := NewChatProxyService(nil, nil, NewNoOpEleAgentModelService(), nil)

	_, err := svc.Chat(context.Background(), &ChatRequest{
		Provider: "nonexist",
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不支持的模型厂商")
}

// TestNormalizeMessageContents_FiltersEmptyAssistant 验证空 assistant 消息被过滤，
// 避免 Kimi 等厂商因 assistant content 为空而返回 400。
func TestNormalizeMessageContents_FiltersEmptyAssistant(t *testing.T) {
	messages := []llm.Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "   "},
		{Role: "assistant", Content: "你好，有什么可以帮你的？"},
	}

	result, err := normalizeMessageContents(messages, false)

	assert.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Equal(t, "system", result[0].Role)
	assert.Equal(t, "user", result[1].Role)
	assert.Equal(t, "assistant", result[2].Role)
	assert.Equal(t, "你好，有什么可以帮你的？", result[2].Content)
}

// TestNormalizeMessageContents_ImageVision 验证图片内容仅在视觉模型下保留；
// 非视觉模型应直接拒绝，而不是将提示文本透传进对话内容。
func TestNormalizeMessageContents_ImageVision(t *testing.T) {
	messages := []llm.Message{
		{
			Role: "user",
			Content: []interface{}{
				map[string]interface{}{
					"type": "image_url",
					"image_url": map[string]interface{}{
						"url": "data:image/png;base64,abc123",
					},
				},
			},
		},
	}

	// 非视觉模型应拒绝图片
	_, err := normalizeMessageContents(messages, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "当前模型不支持图片理解")

	// 视觉模型应保留图片 part
	result, err := normalizeMessageContents(messages, true)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	parts, ok := result[0].Content.([]interface{})
	assert.True(t, ok)
	assert.Len(t, parts, 1)
}

// TestNormalizeEleAgentTemperature 验证 Kimi coding / K3 系列模型被强制设置为 temperature=1，
// 其他模型保持原值，避免 upstream 400 错误。
func TestNormalizeEleAgentTemperature(t *testing.T) {
	// kimi-for-coding 必须强制为 1
	assert.Equal(t, 1.0, normalizeEleAgentTemperature("Kimi", "kimi-for-coding", 0.7))
	assert.Equal(t, 1.0, normalizeEleAgentTemperature("kimi", "kimi-for-coding-latest", 0.5))
	assert.Equal(t, 1.0, normalizeEleAgentTemperature("KIMI", "moonshot-v1-kimi-for-coding", 1.5))

	// K3 系列同样强制为 1（实测 temperature=0.2 返回 400）
	assert.Equal(t, 1.0, normalizeEleAgentTemperature("kimi", "k3", 0.2))
	assert.Equal(t, 1.0, normalizeEleAgentTemperature("kimi", "kimi-k3", 0.0))
	assert.Equal(t, 1.0, normalizeEleAgentTemperature("Kimi", "kimi-k3-0716", 0.7))

	// 其他 Kimi 模型保持原值
	assert.Equal(t, 0.7, normalizeEleAgentTemperature("Kimi", "kimi-k2.6", 0.7))

	// 非 Kimi 平台保持原值
	assert.Equal(t, 0.7, normalizeEleAgentTemperature("qwen", "Qwen/Qwen3-8B", 0.7))
	assert.Equal(t, 0.0, normalizeEleAgentTemperature("openai", "gpt-4o", 0.0))
}
