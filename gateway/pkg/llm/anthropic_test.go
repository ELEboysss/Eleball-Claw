package llm

import (
	"strings"
	"testing"
)

// TestAnthropicToAnthropicRequest 验证 OpenAI 兼容请求到 Anthropic 请求的基本转换
func TestAnthropicToAnthropicRequest(t *testing.T) {
	client := NewAnthropicClient("test-key", "", 0)

	req := ChatRequest{
		Model: "claude-3-7-sonnet-20250219",
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: 1024,
	}

	anthropicReq, err := client.toAnthropicRequest(req, false)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if anthropicReq.Model != req.Model {
		t.Errorf("模型名不匹配: got %s, want %s", anthropicReq.Model, req.Model)
	}
	if anthropicReq.System != "You are a helpful assistant." {
		t.Errorf("system 字段不匹配: got %s", anthropicReq.System)
	}
	if len(anthropicReq.Messages) != 1 {
		t.Fatalf("消息数量错误: got %d, want 1", len(anthropicReq.Messages))
	}
	if anthropicReq.Messages[0].Role != "user" {
		t.Errorf("消息角色错误: got %s", anthropicReq.Messages[0].Role)
	}
	if anthropicReq.MaxTokens != 1024 {
		t.Errorf("max_tokens 错误: got %d, want 1024", anthropicReq.MaxTokens)
	}
}

// TestAnthropicToAnthropicRequestWithImage 验证图片 content part 转换
func TestAnthropicToAnthropicRequestWithImage(t *testing.T) {
	client := NewAnthropicClient("test-key", "", 0)

	req := ChatRequest{
		Model: "claude-3-7-sonnet-20250219",
		Messages: []Message{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "描述这张图片",
					},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "data:image/png;base64,iVBORw0KGgo=",
						},
					},
				},
			},
		},
	}

	anthropicReq, err := client.toAnthropicRequest(req, false)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(anthropicReq.Messages) != 1 {
		t.Fatalf("消息数量错误: got %d, want 1", len(anthropicReq.Messages))
	}
	blocks := anthropicReq.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("content block 数量错误: got %d, want 2", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("第一个 block 类型错误: got %s", blocks[0].Type)
	}
	if blocks[1].Type != "image" {
		t.Errorf("第二个 block 类型错误: got %s", blocks[1].Type)
	}
	if blocks[1].Source == nil {
		t.Fatal("image source 为空")
	}
	if blocks[1].Source.MediaType != "image/png" {
		t.Errorf("media type 错误: got %s", blocks[1].Source.MediaType)
	}
}

// TestAnthropicExtractContent 验证响应 content blocks 提取
func TestAnthropicExtractContent(t *testing.T) {
	client := NewAnthropicClient("test-key", "", 0)
	blocks := []anthropicContentBlock{
		{Type: "text", Text: "最终答案"},
		{Type: "thinking", Thinking: "思考过程"},
	}
	content, reasoning := client.extractContent(blocks)
	if content != "最终答案" {
		t.Errorf("content 错误: got %s", content)
	}
	if reasoning != "思考过程" {
		t.Errorf("reasoning 错误: got %s", reasoning)
	}
}

// TestParseDataURL 验证 data URL 解析
func TestParseDataURL(t *testing.T) {
	mediaType, data, err := parseDataURL("data:image/jpeg;base64,SGVsbG8=")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if mediaType != "image/jpeg" {
		t.Errorf("media type 错误: got %s", mediaType)
	}
	if data != "SGVsbG8=" {
		t.Errorf("data 错误: got %s", data)
	}

	_, _, err = parseDataURL("invalid")
	if err == nil {
		t.Error("非法 URL 应返回错误")
	}

	_, _, err = parseDataURL("data:image/png;base64,!!!")
	if err == nil {
		t.Error("非法 base64 应返回错误")
	}
}

// TestAnthropicParseImageURL 验证公开 URL 被拒绝（当前仅支持 base64）
func TestAnthropicParseImageURL(t *testing.T) {
	client := NewAnthropicClient("test-key", "", 0)
	_, err := client.parseImageURL("https://example.com/image.png")
	if err == nil {
		t.Error("公开 URL 应返回错误")
	}
	if !strings.Contains(err.Error(), "暂不支持") {
		t.Errorf("错误信息不匹配: %v", err)
	}
}
