package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// TestAnthropicExtractContent 验证响应 content blocks 提取（含 tool_use，AR-10）
func TestAnthropicExtractContent(t *testing.T) {
	client := NewAnthropicClient("test-key", "", 0)
	blocks := []anthropicContentBlock{
		{Type: "text", Text: "最终答案"},
		{Type: "thinking", Thinking: "思考过程"},
		{Type: "tool_use", ID: "toolu_1", Name: "search", Input: map[string]interface{}{"q": "hello"}},
	}
	content, reasoning, toolCalls := client.extractContent(blocks)
	if content != "最终答案" {
		t.Errorf("content 错误: got %s", content)
	}
	if reasoning != "思考过程" {
		t.Errorf("reasoning 错误: got %s", reasoning)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("toolCalls 数量错误: got %d, want 1", len(toolCalls))
	}
	if toolCalls[0].ID != "toolu_1" || toolCalls[0].Function.Name != "search" {
		t.Errorf("toolCall 不匹配: %+v", toolCalls[0])
	}
	if toolCalls[0].Function.Arguments != `{"q":"hello"}` {
		t.Errorf("toolCall arguments 错误: got %s", toolCalls[0].Function.Arguments)
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

// TestAnthropicToolsConversion 验证 OpenAI function schema -> Anthropic tools + tool_choice（AR-10）
func TestAnthropicToolsConversion(t *testing.T) {
	client := NewAnthropicClient("test-key", "", 0)
	req := ChatRequest{
		Model:    "claude",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: []map[string]interface{}{
			{"type": "function", "function": map[string]interface{}{
				"name":        "search",
				"description": "搜索网络",
				"parameters":  map[string]interface{}{"type": "object", "properties": map[string]interface{}{"q": map[string]interface{}{"type": "string"}}},
			}},
		},
		ToolChoice: "auto",
	}
	ar, err := client.toAnthropicRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ar.Tools) != 1 {
		t.Fatalf("tools 数量错误: got %d", len(ar.Tools))
	}
	if ar.Tools[0].Name != "search" || ar.Tools[0].Description != "搜索网络" {
		t.Errorf("tool 不匹配: %+v", ar.Tools[0])
	}
	if ar.Tools[0].InputSchema["type"] != "object" {
		t.Errorf("input_schema 错误: %+v", ar.Tools[0].InputSchema)
	}
	if ar.ToolChoice == nil || ar.ToolChoice.Type != "auto" {
		t.Errorf("tool_choice 错误: %+v", ar.ToolChoice)
	}

	// required -> any
	req.ToolChoice = "required"
	ar2, _ := client.toAnthropicRequest(req, false)
	if ar2.ToolChoice.Type != "any" {
		t.Errorf("required 应映射为 any: %+v", ar2.ToolChoice)
	}
}

// TestAnthropicToolMessageConversion 验证 assistant ToolCalls->tool_use 块、tool 角色->tool_result 块（AR-10）
func TestAnthropicToolMessageConversion(t *testing.T) {
	client := NewAnthropicClient("test-key", "", 0)
	req := ChatRequest{
		Model: "claude",
		Messages: []Message{
			{Role: "user", Content: "查天气"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{{
				ID:       "toolu_1",
				Type:     "function",
				Function: ToolCallFunction{Name: "weather", Arguments: `{"city":"北京"}`},
			}}},
			{Role: "tool", ToolCallID: "toolu_1", Content: "晴天 25度"},
		},
	}
	ar, err := client.toAnthropicRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ar.Messages) != 3 {
		t.Fatalf("消息数错误: got %d, want 3", len(ar.Messages))
	}
	// assistant 消息含 tool_use 块
	asst := ar.Messages[1]
	if asst.Role != "assistant" {
		t.Errorf("assistant 角色错误: %s", asst.Role)
	}
	var foundToolUse bool
	for _, b := range asst.Content {
		if b.Type == "tool_use" {
			foundToolUse = true
			if b.ID != "toolu_1" || b.Name != "weather" {
				t.Errorf("tool_use 块不匹配: %+v", b)
			}
			if b.Input["city"] != "北京" {
				t.Errorf("input 不匹配: %+v", b.Input)
			}
		}
	}
	if !foundToolUse {
		t.Error("未找到 tool_use 块")
	}
	// tool 消息 -> user + tool_result 块
	tr := ar.Messages[2]
	if tr.Role != "user" {
		t.Errorf("tool 结果角色应为 user: got %s", tr.Role)
	}
	if len(tr.Content) != 1 || tr.Content[0].Type != "tool_result" {
		t.Errorf("tool_result 块错误: %+v", tr.Content)
	}
	if tr.Content[0].ToolUseID != "toolu_1" {
		t.Errorf("tool_use_id 错误: got %s", tr.Content[0].ToolUseID)
	}
	if tr.Content[0].Content != "晴天 25度" {
		t.Errorf("tool_result content 错误: got %v", tr.Content[0].Content)
	}
}

// TestAnthropicMultiToolResultMerge 验证连续 tool 消息合并到同一 user 消息（AR-10，满足角色交替）
func TestAnthropicMultiToolResultMerge(t *testing.T) {
	client := NewAnthropicClient("test-key", "", 0)
	req := ChatRequest{
		Model: "claude",
		Messages: []Message{
			{Role: "user", Content: "查两个"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{
				{ID: "t1", Type: "function", Function: ToolCallFunction{Name: "a", Arguments: "{}"}},
				{ID: "t2", Type: "function", Function: ToolCallFunction{Name: "b", Arguments: "{}"}},
			}},
			{Role: "tool", ToolCallID: "t1", Content: "r1"},
			{Role: "tool", ToolCallID: "t2", Content: "r2"},
		},
	}
	ar, err := client.toAnthropicRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	// user, assistant, user(2 tool_result 块) -- 两条 tool 合并为一条 user
	if len(ar.Messages) != 3 {
		t.Fatalf("消息数错误: got %d, want 3（两条 tool 应合并）", len(ar.Messages))
	}
	if ar.Messages[2].Role != "user" {
		t.Errorf("角色错误: %s", ar.Messages[2].Role)
	}
	if len(ar.Messages[2].Content) != 2 {
		t.Fatalf("tool_result 块数错误: got %d, want 2", len(ar.Messages[2].Content))
	}
}

// TestAnthropicChatToolUseRoundTrip 验证 Chat() 把 tool_use 响应解析为 ToolCalls（AR-10，httptest）
func TestAnthropicChatToolUseRoundTrip(t *testing.T) {
	var gotTools int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var areq anthropicRequest
		_ = json.Unmarshal(b, &areq)
		gotTools = len(areq.Tools)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": "调用工具"},
				{"type": "tool_use", "id": "toolu_9", "name": "search", "input": map[string]interface{}{"q": "x"}},
			},
			"stop_reason": "tool_use",
			"usage":       map[string]interface{}{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer srv.Close()

	c := NewAnthropicClient("k", srv.URL, 5*time.Second)
	chunk, err := c.Chat(context.Background(), ChatRequest{
		Model:    "claude",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: []map[string]interface{}{
			{"type": "function", "function": map[string]interface{}{"name": "search", "parameters": map[string]interface{}{"type": "object"}}},
		},
	})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if gotTools != 1 {
		t.Errorf("请求未透传 tools: got %d", gotTools)
	}
	if chunk.Delta != "调用工具" {
		t.Errorf("Delta 错误: got %s", chunk.Delta)
	}
	if len(chunk.ToolCalls) != 1 {
		t.Fatalf("ToolCalls 数量错误: got %d", len(chunk.ToolCalls))
	}
	if chunk.ToolCalls[0].ID != "toolu_9" || chunk.ToolCalls[0].Function.Name != "search" {
		t.Errorf("ToolCall 不匹配: %+v", chunk.ToolCalls[0])
	}
	if chunk.ToolCalls[0].Function.Arguments != `{"q":"x"}` {
		t.Errorf("Args 错误: got %s", chunk.ToolCalls[0].Function.Arguments)
	}
}

// TestAnthropicStreamToolUse 验证 ChatStream 流式累积 tool_use 参数并发出 ToolCalls（AR-10）
func TestAnthropicStreamToolUse(t *testing.T) {
	evt := func(typ string, payload map[string]interface{}) string {
		payload["type"] = typ
		b, _ := json.Marshal(payload)
		return "event: " + typ + "\ndata: " + string(b) + "\n\n"
	}
	sse := evt("message_start", map[string]interface{}{"message": map[string]interface{}{"usage": map[string]interface{}{"input_tokens": 5}}}) +
		evt("content_block_start", map[string]interface{}{"index": 0, "content_block": map[string]interface{}{"type": "tool_use", "id": "toolu_7", "name": "weather", "input": map[string]interface{}{}}}) +
		evt("content_block_delta", map[string]interface{}{"index": 0, "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": `{"city":"`}}) +
		evt("content_block_delta", map[string]interface{}{"index": 0, "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": `NYC"}`}}) +
		evt("content_block_stop", map[string]interface{}{"index": 0}) +
		evt("message_stop", map[string]interface{}{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	c := NewAnthropicClient("k", srv.URL, 5*time.Second)
	ch, err := c.ChatStream(context.Background(), ChatRequest{Model: "claude", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("ChatStream 失败: %v", err)
	}
	var toolCalls []ToolCall
	for chunk := range ch {
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}
	}
	if len(toolCalls) != 1 {
		t.Fatalf("toolCalls 数量错误: got %d, want 1", len(toolCalls))
	}
	if toolCalls[0].ID != "toolu_7" || toolCalls[0].Function.Name != "weather" {
		t.Errorf("toolCall 不匹配: %+v", toolCalls[0])
	}
	if toolCalls[0].Function.Arguments != `{"city":"NYC"}` {
		t.Errorf("Args 错误: got %s", toolCalls[0].Function.Arguments)
	}
}
