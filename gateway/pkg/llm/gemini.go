package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// geminiToolCallSeq 为未带 id 的 Gemini functionCall 生成唯一工具调用 id（编排器回填 tool 结果时引用）
var geminiToolCallSeq uint64

// GeminiClient 适配 Google Gemini generateContent / streamGenerateContent 原生协议。
// 负责将 OpenAI 兼容请求转换为 Gemini 原生格式，并将响应/流式增量转回 OpenAI 兼容 ChatChunk。
type GeminiClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
	logger     *zap.Logger
}

// NewGeminiClient 创建 Gemini 客户端
func NewGeminiClient(apiKey, baseURL string, timeout time.Duration) *GeminiClient {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       90 * time.Second,
	}
	return &GeminiClient{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Transport: transport},
		timeout:    timeout,
		logger:     zap.NewNop(),
	}
}

// SetLogger 设置日志器
func (c *GeminiClient) SetLogger(logger *zap.Logger) {
	if logger != nil {
		c.logger = logger
	}
}

// Chat 非流式对话
func (c *GeminiClient) Chat(ctx context.Context, req ChatRequest) (*ChatChunk, error) {
	geminiReq, err := c.toGeminiRequest(req, false)
	if err != nil {
		return nil, fmt.Errorf("构造 Gemini 请求失败: %w", err)
	}

	body, err := c.doRequest(ctx, req.Model, geminiReq)
	if err != nil {
		return nil, err
	}

	var resp geminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析 Gemini 响应失败: %w", err)
	}

	content, reasoning, toolCalls := c.extractContent(resp.Candidates)
	chunk := &ChatChunk{
		Delta:            content,
		ReasoningContent: reasoning,
		ToolCalls:        toolCalls,
		Usage:            geminiUsage(resp.UsageMetadata),
	}
	chunk.FinishReason = mapGeminiFinishReason(resp.finishReason(), len(toolCalls) > 0)
	if chunk.Usage == nil {
		// 上游未返回 usage 时兜底估算，避免漏记漏扣
		chunk.Usage = EstimateUsageFromMessages(req.Messages, content)
	}
	return chunk, nil
}

// ChatStream 流式对话
func (c *GeminiClient) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error) {
	geminiReq, err := c.toGeminiRequest(req, true)
	if err != nil {
		return nil, fmt.Errorf("构造 Gemini 流式请求失败: %w", err)
	}

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", c.baseURL, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-goog-api-key", c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		return nil, &UpstreamError{StatusCode: httpResp.StatusCode, Body: string(respBody)}
	}

	chunkChan := make(chan ChatChunk, 10)
	go func() {
		defer close(chunkChan)
		defer httpResp.Body.Close()

		var accumulatedContent strings.Builder
		var accumulatedReasoning strings.Builder
		var lastUsage *Usage
		var usageEmitted bool
		var sawToolCall bool
		var lastFinish string
		var lastLines []string

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			lastLines = append(lastLines, line)
			if len(lastLines) > 10 {
				lastLines = lastLines[1:]
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}

			var resp geminiResponse
			if err := json.Unmarshal([]byte(data), &resp); err != nil {
				c.logger.Warn("解析 Gemini SSE 事件失败", zap.String("data", data), zap.Error(err))
				continue
			}

			for _, part := range resp.parts() {
				if part.FunctionCall != nil {
					sawToolCall = true
					tc := geminiFunctionCallToToolCall(part.FunctionCall)
					select {
					case chunkChan <- ChatChunk{ToolCalls: []ToolCall{tc}}:
					case <-ctx.Done():
						return
					}
					continue
				}
				if part.Text == "" {
					continue
				}
				if part.Thought {
					accumulatedReasoning.WriteString(part.Text)
					select {
					case chunkChan <- ChatChunk{ReasoningContent: part.Text}:
					case <-ctx.Done():
						return
					}
				} else {
					accumulatedContent.WriteString(part.Text)
					select {
					case chunkChan <- ChatChunk{Delta: part.Text}:
					case <-ctx.Done():
						return
					}
				}
			}

			if fr := resp.finishReason(); fr != "" {
				lastFinish = fr
			}
			if u := geminiUsage(resp.UsageMetadata); u != nil {
				lastUsage = u
			}
		}

		if accumulatedContent.Len() == 0 && accumulatedReasoning.Len() == 0 && !sawToolCall {
			c.logger.Warn("Gemini 上游流式响应未返回任何内容",
				zap.String("baseURL", c.baseURL),
				zap.Strings("last_lines", lastLines),
				zap.Error(scanner.Err()),
			)
		}

		if lastFinish != "" {
			select {
			case chunkChan <- ChatChunk{FinishReason: mapGeminiFinishReason(lastFinish, sawToolCall)}:
			case <-ctx.Done():
				return
			}
		}

		if lastUsage != nil {
			usageEmitted = true
			select {
			case chunkChan <- ChatChunk{Usage: lastUsage}:
			case <-ctx.Done():
				return
			}
		}
		// 上游未返回 usage 且流正常结束时，补发兜底估算 final chunk
		if !usageEmitted && scanner.Err() == nil {
			usage := EstimateUsageFromMessages(req.Messages, accumulatedContent.String())
			select {
			case chunkChan <- ChatChunk{Usage: usage}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return chunkChan, nil
}

// doRequest 非流式请求，返回完整响应体
func (c *GeminiClient) doRequest(ctx context.Context, model string, geminiReq *geminiRequest) ([]byte, error) {
	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/models/%s:generateContent", c.baseURL, model)
	httpReq, err := http.NewRequestWithContext(callCtx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-goog-api-key", c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, &UpstreamError{StatusCode: httpResp.StatusCode, Body: string(respBody)}
	}
	return respBody, nil
}

// toGeminiRequest 将 OpenAI 兼容请求转换为 Gemini generateContent 请求：
//   - system -> 顶层 systemInstruction；user->user；assistant->model；
//   - assistant 的 ToolCalls -> model 角色 functionCall parts；
//   - tool 结果 -> user 角色 functionResponse parts（连续 tool 合并到同一 user，满足角色交替）；
//   - Gemini functionResponse 按 name 匹配，而编排器构造 tool 结果消息时不带 name（仅 tool_call_id），
//     故维护 tool_call_id -> name 映射，从历史 assistant ToolCalls 反查。
func (c *GeminiClient) toGeminiRequest(req ChatRequest, stream bool) (*geminiRequest, error) {
	var systemParts []string
	var contents []geminiContent
	toolCallIDToName := map[string]string{}

	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			systemParts = append(systemParts, contentToString(msg.Content))
			continue
		case "tool":
			name := msg.Name
			if name == "" {
				name = toolCallIDToName[msg.ToolCallID]
			}
			if name == "" {
				name = "tool_result"
			}
			respPart := geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					Name:     name,
					Response: map[string]interface{}{"output": contentToString(msg.Content)},
				},
			}
			if n := len(contents); n > 0 && contents[n-1].Role == "user" &&
				len(contents[n-1].Parts) > 0 && contents[n-1].Parts[0].FunctionResponse != nil {
				contents[n-1].Parts = append(contents[n-1].Parts, respPart)
			} else {
				contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{respPart}})
			}
			continue
		case "assistant":
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					toolCallIDToName[tc.ID] = tc.Function.Name
				}
			}
			parts, err := c.toGeminiAssistantParts(msg)
			if err != nil {
				return nil, err
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, geminiContent{Role: "model", Parts: parts})
			continue
		}

		parts, err := c.toGeminiContent(msg.Content)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, geminiContent{Role: "user", Parts: parts})
	}

	geminiReq := &geminiRequest{
		Contents:         contents,
		GenerationConfig: buildGeminiGenerationConfig(req),
	}
	if len(systemParts) > 0 {
		geminiReq.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: strings.Join(systemParts, "\n\n")}}}
	}
	if tools := toGeminiTools(req.Tools); len(tools) > 0 {
		geminiReq.Tools = tools
		geminiReq.ToolConfig = toGeminiToolConfig(req.ToolChoice)
	}
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		geminiReq.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{IncludeThoughts: true}
		// AR-19 P2-3：显式推理 token 预算（BudgetTokens>0 时下发 thinkingBudget）
		if req.Thinking.BudgetTokens > 0 {
			geminiReq.GenerationConfig.ThinkingConfig.ThinkingBudget = req.Thinking.BudgetTokens
		}
	}
	return geminiReq, nil
}

// toGeminiAssistantParts 将 assistant 消息转为 Gemini model 角色.parts（文本 + functionCall）
func (c *GeminiClient) toGeminiAssistantParts(msg Message) ([]geminiPart, error) {
	parts, err := c.toGeminiContent(msg.Content)
	if err != nil {
		return nil, err
	}
	for _, tc := range msg.ToolCalls {
		fc := &geminiFunctionCall{
			Name: tc.Function.Name,
			Args: parseToolCallInput(tc.Function.Arguments),
		}
		if tc.ID != "" {
			fc.ID = tc.ID
		}
		parts = append(parts, geminiPart{FunctionCall: fc})
	}
	return parts, nil
}

// toGeminiContent 将 OpenAI 兼容 content（string / []ContentPart）转为 Gemini parts
func (c *GeminiClient) toGeminiContent(content interface{}) ([]geminiPart, error) {
	switch v := content.(type) {
	case string:
		if v == "" {
			return nil, nil
		}
		return []geminiPart{{Text: v}}, nil
	case []interface{}:
		var parts []geminiPart
		for _, item := range v {
			part, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			switch part["type"] {
			case "text":
				if text, ok := part["text"].(string); ok && text != "" {
					parts = append(parts, geminiPart{Text: text})
				}
			case "image_url":
				imageURLPart, ok := part["image_url"].(map[string]interface{})
				if !ok {
					continue
				}
				url, _ := imageURLPart["url"].(string)
				inline, err := c.toGeminiInline(url)
				if err != nil {
					return nil, err
				}
				parts = append(parts, geminiPart{InlineData: inline})
			case "file":
				filePart, ok := part["file"].(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := filePart["text"].(string); ok && text != "" {
					name, _ := filePart["name"].(string)
					if name != "" {
						parts = append(parts, geminiPart{Text: fmt.Sprintf("【文件：%s】\n%s", name, text)})
					} else {
						parts = append(parts, geminiPart{Text: text})
					}
				}
			}
		}
		return parts, nil
	default:
		s := contentToString(content)
		if s == "" {
			return nil, nil
		}
		return []geminiPart{{Text: s}}, nil
	}
}

// toGeminiInline 将 data URL 或公开 URL 转为 Gemini inlineData（base64）。
// Gemini 不支持直传公开 URL，故公开 URL 下载后内联。
func (c *GeminiClient) toGeminiInline(url string) (*geminiInlineData, error) {
	if url == "" {
		return nil, fmt.Errorf("图片 URL 为空")
	}
	if strings.HasPrefix(url, "data:") {
		mediaType, data, err := parseDataURL(url)
		if err != nil {
			return nil, fmt.Errorf("解析图片 data URL 失败: %w", err)
		}
		return &geminiInlineData{MimeType: mediaType, Data: data}, nil
	}
	return fetchImageToInlineData(url)
}

// fetchImageToInlineData 下载公开图片并以 base64 内联（15s 超时、20MB 上限）
func fetchImageToInlineData(url string) (*geminiInlineData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("构造图片下载请求失败: %w", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("下载图片失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载图片失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("读取图片数据失败: %w", err)
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	if i := strings.Index(mimeType, ";"); i >= 0 {
		mimeType = strings.TrimSpace(mimeType[:i])
	}
	return &geminiInlineData{MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(data)}, nil
}

// toGeminiTools 将 OpenAI function schema 转为 Gemini functionDeclarations
func toGeminiTools(tools []map[string]interface{}) []geminiToolDeclaration {
	if len(tools) == 0 {
		return nil
	}
	var decls []geminiFunctionDeclaration
	for _, t := range tools {
		fn, _ := t["function"].(map[string]interface{})
		if fn == nil {
			continue
		}
		name, _ := fn["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := fn["description"].(string)
		params, _ := fn["parameters"].(map[string]interface{})
		if params == nil {
			params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		decls = append(decls, geminiFunctionDeclaration{Name: name, Description: desc, Parameters: params})
	}
	if len(decls) == 0 {
		return nil
	}
	return []geminiToolDeclaration{{FunctionDeclarations: decls}}
}

// toGeminiToolConfig 映射 tool_choice：auto/空->AUTO、required->ANY、none->NONE
func toGeminiToolConfig(toolChoice string) *geminiToolConfig {
	mode := "AUTO"
	switch toolChoice {
	case "required":
		mode = "ANY"
	case "none":
		mode = "NONE"
	}
	return &geminiToolConfig{FunctionCallingConfig: geminiFunctionCallingConfig{Mode: mode}}
}

// buildGeminiGenerationConfig 构造 generationConfig（temperature/topP/maxOutputTokens/stopSequences）
func buildGeminiGenerationConfig(req ChatRequest) *geminiGenerationConfig {
	cfg := &geminiGenerationConfig{}
	if req.Temperature != 0 {
		cfg.Temperature = req.Temperature
	}
	if req.TopP != 0 {
		cfg.TopP = req.TopP
	}
	maxTokens := req.MaxTokens
	if req.MaxCompletionTokens > 0 {
		maxTokens = req.MaxCompletionTokens
	}
	if maxTokens > 0 {
		cfg.MaxOutputTokens = maxTokens
	}
	if len(req.Stop) > 0 {
		cfg.StopSequences = req.Stop
	}
	return cfg
}

// extractContent 从 Gemini candidates.parts 中提取文本、思考内容与工具调用
func (c *GeminiClient) extractContent(candidates []geminiCandidate) (string, string, []ToolCall) {
	var contents []string
	var reasoning []string
	var toolCalls []ToolCall
	for _, cand := range candidates {
		for _, part := range cand.Content.Parts {
			if part.FunctionCall != nil {
				toolCalls = append(toolCalls, geminiFunctionCallToToolCall(part.FunctionCall))
				continue
			}
			if part.Text == "" {
				continue
			}
			if part.Thought {
				reasoning = append(reasoning, part.Text)
			} else {
				contents = append(contents, part.Text)
			}
		}
	}
	return strings.Join(contents, ""), strings.Join(reasoning, ""), toolCalls
}

// geminiFunctionCallToToolCall 将 Gemini functionCall 转为 ToolCall；id 缺失时生成唯一 id
func geminiFunctionCallToToolCall(fc *geminiFunctionCall) ToolCall {
	id := fc.ID
	if id == "" {
		id = fmt.Sprintf("gemini-%d", atomic.AddUint64(&geminiToolCallSeq, 1))
	}
	args := fc.Args
	if args == nil {
		args = map[string]interface{}{}
	}
	argsJSON, _ := json.Marshal(args)
	return ToolCall{
		ID:       id,
		Type:     "function",
		Function: ToolCallFunction{Name: fc.Name, Arguments: string(argsJSON)},
	}
}

// geminiUsage 将 Gemini usageMetadata 转为 Usage（thoughtsTokenCount 计入输出）
func geminiUsage(meta *geminiUsageMetadata) *Usage {
	if meta == nil {
		return nil
	}
	prompt := meta.PromptTokenCount
	completion := meta.CandidatesTokenCount + meta.ThoughtsTokenCount
	total := meta.TotalTokenCount
	if total == 0 {
		total = prompt + completion
	}
	return &Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		CachedTokens:     meta.CachedContentTokenCount,
	}
}

// mapGeminiFinishReason 映射 Gemini finishReason 到 OpenAI 兼容 finish_reason
func mapGeminiFinishReason(reason string, hasToolCall bool) string {
	if hasToolCall {
		return "tool_calls"
	}
	switch strings.ToUpper(reason) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "OTHER":
		return "content_filter"
	default:
		if reason == "" {
			return ""
		}
		return "stop"
	}
}

// ---- Gemini 原生协议 DTO ----

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDeclaration `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
	ID   string                 `json:"id,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
	ID       string                 `json:"id,omitempty"`
}

type geminiToolDeclaration struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig geminiFunctionCallingConfig `json:"functionCallingConfig"`
}

type geminiFunctionCallingConfig struct {
	Mode string `json:"mode,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     float64               `json:"temperature,omitempty"`
	TopP            float64               `json:"topP,omitempty"`
	MaxOutputTokens int                   `json:"maxOutputTokens,omitempty"`
	StopSequences   []string              `json:"stopSequences,omitempty"`
	ThinkingConfig  *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiThinkingConfig struct {
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
	ThinkingBudget  int  `json:"thinkingBudget,omitempty"` // AR-19 P2-3：推理 token 预算（0 不下发，交由上游默认）
}

type geminiResponse struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
}

func (r geminiResponse) finishReason() string {
	if len(r.Candidates) == 0 {
		return ""
	}
	return r.Candidates[0].FinishReason
}

func (r geminiResponse) parts() []geminiPart {
	if len(r.Candidates) == 0 {
		return nil
	}
	return r.Candidates[0].Content.Parts
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
	Index        int           `json:"index,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}
