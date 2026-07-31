package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestGeminiToGeminiRequest 验证 OpenAI 兼容请求到 Gemini 请求的基本转换
func TestGeminiToGeminiRequest(t *testing.T) {
	client := NewGeminiClient("test-key", "", 0)

	req := ChatRequest{
		Model: "gemini-1.5-flash",
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: 1024,
	}

	geminiReq, err := client.toGeminiRequest(req, false)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if geminiReq.SystemInstruction == nil || len(geminiReq.SystemInstruction.Parts) == 0 {
		t.Fatalf("systemInstruction 未抽离")
	}
	if geminiReq.SystemInstruction.Parts[0].Text != "You are a helpful assistant." {
		t.Errorf("systemInstruction 不匹配: got %s", geminiReq.SystemInstruction.Parts[0].Text)
	}
	if len(geminiReq.Contents) != 1 {
		t.Fatalf("contents 数量错误: got %d, want 1", len(geminiReq.Contents))
	}
	if geminiReq.Contents[0].Role != "user" {
		t.Errorf("角色错误: got %s, want user", geminiReq.Contents[0].Role)
	}
	if geminiReq.GenerationConfig.MaxOutputTokens != 1024 {
		t.Errorf("maxOutputTokens 错误: got %d", geminiReq.GenerationConfig.MaxOutputTokens)
	}
}

// TestGeminiToGeminiRequestWithImage 验证 data URL 图片 -> inlineData 转换
func TestGeminiToGeminiRequestWithImage(t *testing.T) {
	client := NewGeminiClient("test-key", "", 0)

	req := ChatRequest{
		Model: "gemini-1.5-flash",
		Messages: []Message{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "描述这张图片"},
					map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/png;base64,iVBORw0KGgo="}},
				},
			},
		},
	}

	geminiReq, err := client.toGeminiRequest(req, false)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(geminiReq.Contents) != 1 {
		t.Fatalf("contents 数量错误: got %d, want 1", len(geminiReq.Contents))
	}
	parts := geminiReq.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("parts 数量错误: got %d, want 2", len(parts))
	}
	if parts[0].Text != "描述这张图片" {
		t.Errorf("text part 错误: got %s", parts[0].Text)
	}
	if parts[1].InlineData == nil {
		t.Fatal("inlineData 为空")
	}
	if parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("mimeType 错误: got %s", parts[1].InlineData.MimeType)
	}
	if parts[1].InlineData.Data != "iVBORw0KGgo=" {
		t.Errorf("data 错误: got %s", parts[1].InlineData.Data)
	}
}

// TestGeminiExtractContent 验证 candidates.parts 提取（text + thought + functionCall）
func TestGeminiExtractContent(t *testing.T) {
	client := NewGeminiClient("test-key", "", 0)
	candidates := []geminiCandidate{{
		Content: geminiContent{Role: "model", Parts: []geminiPart{
			{Text: "最终答案"},
			{Text: "思考过程", Thought: true},
			{FunctionCall: &geminiFunctionCall{ID: "fc_1", Name: "search", Args: map[string]interface{}{"q": "hello"}}},
		}},
	}}
	content, reasoning, toolCalls := client.extractContent(candidates)
	if content != "最终答案" {
		t.Errorf("content 错误: got %s", content)
	}
	if reasoning != "思考过程" {
		t.Errorf("reasoning 错误: got %s", reasoning)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("toolCalls 数量错误: got %d, want 1", len(toolCalls))
	}
	if toolCalls[0].ID != "fc_1" || toolCalls[0].Function.Name != "search" {
		t.Errorf("toolCall 不匹配: %+v", toolCalls[0])
	}
	if toolCalls[0].Function.Arguments != `{"q":"hello"}` {
		t.Errorf("toolCall arguments 错误: got %s", toolCalls[0].Function.Arguments)
	}
}

// TestGeminiToolsConversion 验证 OpenAI function schema -> Gemini functionDeclarations + tool_choice
func TestGeminiToolsConversion(t *testing.T) {
	client := NewGeminiClient("test-key", "", 0)
	req := ChatRequest{
		Model:    "gemini-1.5-flash",
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
	gr, err := client.toGeminiRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(gr.Tools) != 1 || len(gr.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools 数量错误: %+v", gr.Tools)
	}
	decl := gr.Tools[0].FunctionDeclarations[0]
	if decl.Name != "search" || decl.Description != "搜索网络" {
		t.Errorf("functionDeclaration 不匹配: %+v", decl)
	}
	if decl.Parameters["type"] != "object" {
		t.Errorf("parameters 错误: %+v", decl.Parameters)
	}
	if gr.ToolConfig == nil || gr.ToolConfig.FunctionCallingConfig.Mode != "AUTO" {
		t.Errorf("toolConfig AUTO 错误: %+v", gr.ToolConfig)
	}

	// required -> ANY
	req.ToolChoice = "required"
	gr2, _ := client.toGeminiRequest(req, false)
	if gr2.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Errorf("required 应映射为 ANY: %+v", gr2.ToolConfig)
	}

	// none -> NONE
	req.ToolChoice = "none"
	gr3, _ := client.toGeminiRequest(req, false)
	if gr3.ToolConfig.FunctionCallingConfig.Mode != "NONE" {
		t.Errorf("none 应映射为 NONE: %+v", gr3.ToolConfig)
	}
}

// TestGeminiToolMessageConversion 验证 assistant ToolCalls->functionCall、tool->functionResponse（name 反查）
func TestGeminiToolMessageConversion(t *testing.T) {
	client := NewGeminiClient("test-key", "", 0)
	req := ChatRequest{
		Model: "gemini-1.5-flash",
		Messages: []Message{
			{Role: "user", Content: "查天气"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{{
				ID:       "t1",
				Type:     "function",
				Function: ToolCallFunction{Name: "weather", Arguments: `{"city":"北京"}`},
			}}},
			{Role: "tool", ToolCallID: "t1", Content: "晴天 25度"},
		},
	}
	gr, err := client.toGeminiRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	// user, model(functionCall), user(functionResponse)
	if len(gr.Contents) != 3 {
		t.Fatalf("contents 数错误: got %d, want 3", len(gr.Contents))
	}
	model := gr.Contents[1]
	if model.Role != "model" {
		t.Errorf("assistant 角色应为 model: got %s", model.Role)
	}
	if len(model.Parts) != 1 || model.Parts[0].FunctionCall == nil {
		t.Fatalf("functionCall part 缺失: %+v", model.Parts)
	}
	if model.Parts[0].FunctionCall.Name != "weather" {
		t.Errorf("functionCall name 错误: got %s", model.Parts[0].FunctionCall.Name)
	}
	if model.Parts[0].FunctionCall.Args["city"] != "北京" {
		t.Errorf("functionCall args 错误: %+v", model.Parts[0].FunctionCall.Args)
	}
	// tool 消息 -> user + functionResponse（name 由 t1 反查为 weather）
	fr := gr.Contents[2]
	if fr.Role != "user" {
		t.Errorf("tool 结果角色应为 user: got %s", fr.Role)
	}
	if len(fr.Parts) != 1 || fr.Parts[0].FunctionResponse == nil {
		t.Fatalf("functionResponse part 缺失: %+v", fr.Parts)
	}
	if fr.Parts[0].FunctionResponse.Name != "weather" {
		t.Errorf("functionResponse name 应由 tool_call_id 反查为 weather: got %s", fr.Parts[0].FunctionResponse.Name)
	}
	if fr.Parts[0].FunctionResponse.Response["output"] != "晴天 25度" {
		t.Errorf("functionResponse response 错误: %+v", fr.Parts[0].FunctionResponse.Response)
	}
}

// TestGeminiMultiToolResultMerge 验证连续 tool 消息合并到同一 user 消息（满足角色交替）
func TestGeminiMultiToolResultMerge(t *testing.T) {
	client := NewGeminiClient("test-key", "", 0)
	req := ChatRequest{
		Model: "gemini-1.5-flash",
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
	gr, err := client.toGeminiRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	// user, model, user(2 functionResponse parts)
	if len(gr.Contents) != 3 {
		t.Fatalf("contents 数错误: got %d, want 3（两条 tool 应合并）", len(gr.Contents))
	}
	if gr.Contents[2].Role != "user" {
		t.Errorf("角色错误: %s", gr.Contents[2].Role)
	}
	if len(gr.Contents[2].Parts) != 2 {
		t.Fatalf("functionResponse parts 数错误: got %d, want 2", len(gr.Contents[2].Parts))
	}
}

// TestGeminiChatToolUseRoundTrip 验证 Chat() 把 functionCall 响应解析为 ToolCalls（httptest）
func TestGeminiChatToolUseRoundTrip(t *testing.T) {
	var gotTools int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var greq geminiRequest
		_ = json.Unmarshal(b, &greq)
		if len(greq.Tools) > 0 {
			gotTools = len(greq.Tools[0].FunctionDeclarations)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"candidates": []map[string]interface{}{
				{"content": map[string]interface{}{"role": "model", "parts": []map[string]interface{}{
					{"text": "调用工具"},
					{"functionCall": map[string]interface{}{"name": "search", "args": map[string]interface{}{"q": "x"}}},
				}}, "finishReason": "STOP", "index": 0},
			},
			"usageMetadata": map[string]interface{}{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
		})
	}))
	defer srv.Close()

	c := NewGeminiClient("k", srv.URL, 5*time.Second)
	chunk, err := c.Chat(context.Background(), ChatRequest{
		Model:    "gemini-1.5-flash",
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
	if len(chunk.ToolCalls) != 1 || chunk.ToolCalls[0].Function.Name != "search" {
		t.Errorf("ToolCall 不匹配: %+v", chunk.ToolCalls)
	}
	if chunk.ToolCalls[0].Function.Arguments != `{"q":"x"}` {
		t.Errorf("Args 错误: got %s", chunk.ToolCalls[0].Function.Arguments)
	}
	if chunk.ToolCalls[0].ID == "" {
		t.Error("未生成 toolCall id")
	}
	if chunk.Usage == nil || chunk.Usage.PromptTokens != 10 || chunk.Usage.CompletionTokens != 5 {
		t.Errorf("usage 错误: %+v", chunk.Usage)
	}
	if chunk.FinishReason != "tool_calls" {
		t.Errorf("FinishReason 应为 tool_calls: got %s", chunk.FinishReason)
	}
}

// TestGeminiStreamTextAndToolUse 验证 ChatStream 流式发出 Delta + ToolCalls + Usage（httptest SSE）
func TestGeminiStreamTextAndToolUse(t *testing.T) {
	chunk1 := `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},"index":0}]}`
	chunk2 := `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"weather","args":{"city":"NYC"}}}]},"index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`
	sse := "data: " + chunk1 + "\n\ndata: " + chunk2 + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	c := NewGeminiClient("k", srv.URL, 5*time.Second)
	ch, err := c.ChatStream(context.Background(), ChatRequest{Model: "gemini-1.5-flash", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("ChatStream 失败: %v", err)
	}
	var deltas []string
	var toolCalls []ToolCall
	var usage *Usage
	for chunk := range ch {
		if chunk.Delta != "" {
			deltas = append(deltas, chunk.Delta)
		}
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	if strings.Join(deltas, "") != "Hello" {
		t.Errorf("Delta 错误: got %v", deltas)
	}
	if len(toolCalls) != 1 || toolCalls[0].Function.Name != "weather" {
		t.Errorf("toolCall 不匹配: %+v", toolCalls)
	}
	if toolCalls[0].Function.Arguments != `{"city":"NYC"}` {
		t.Errorf("Args 错误: got %s", toolCalls[0].Function.Arguments)
	}
	if usage == nil || usage.PromptTokens != 5 || usage.CompletionTokens != 3 {
		t.Errorf("usage 错误: %+v", usage)
	}
}

// TestGeminiFetchImageToInlineData 验证公开 URL 图片下载后内联为 base64
func TestGeminiFetchImageToInlineData(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()

	inline, err := fetchImageToInlineData(srv.URL + "/img.png")
	if err != nil {
		t.Fatalf("fetch 失败: %v", err)
	}
	if inline.MimeType != "image/png" {
		t.Errorf("mimeType 错误: got %s", inline.MimeType)
	}
	decoded, _ := base64.StdEncoding.DecodeString(inline.Data)
	if !bytes.Equal(decoded, png) {
		t.Errorf("data 不匹配")
	}
}

// TestGeminiThinkingBudget 验证 AR-19 P2-3：Type=enabled + BudgetTokens -> thinkingConfig.thinkingBudget
func TestGeminiThinkingBudget(t *testing.T) {
	client := NewGeminiClient("test-key", "", 0)

	// enabled + budget -> includeThoughts + thinkingBudget
	req := ChatRequest{
		Model:    "gemini-2.0-flash-thinking",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Thinking: &ThinkingOptions{Type: "enabled", BudgetTokens: 8192},
	}
	gr, err := client.toGeminiRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if gr.GenerationConfig == nil || gr.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("thinkingConfig 未下发")
	}
	if !gr.GenerationConfig.ThinkingConfig.IncludeThoughts {
		t.Errorf("includeThoughts 应为 true")
	}
	if gr.GenerationConfig.ThinkingConfig.ThinkingBudget != 8192 {
		t.Errorf("thinkingBudget 错误: got %d, want 8192", gr.GenerationConfig.ThinkingConfig.ThinkingBudget)
	}

	// enabled 但 BudgetTokens=0 -> includeThoughts 仍 true，thinkingBudget 不下发（0）
	req.Thinking.BudgetTokens = 0
	gr2, _ := client.toGeminiRequest(req, false)
	if !gr2.GenerationConfig.ThinkingConfig.IncludeThoughts {
		t.Errorf("includeThoughts 应为 true")
	}
	if gr2.GenerationConfig.ThinkingConfig.ThinkingBudget != 0 {
		t.Errorf("BudgetTokens=0 时不应下发 thinkingBudget: got %d", gr2.GenerationConfig.ThinkingConfig.ThinkingBudget)
	}

	// disabled -> 不下发 thinkingConfig
	req.Thinking = &ThinkingOptions{Type: "disabled", BudgetTokens: 8192}
	gr3, _ := client.toGeminiRequest(req, false)
	if gr3.GenerationConfig.ThinkingConfig != nil {
		t.Errorf("disabled 时不应下发 thinkingConfig: %+v", gr3.GenerationConfig.ThinkingConfig)
	}
}
