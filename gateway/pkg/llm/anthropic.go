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
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
)

// AnthropicClient 适配 Anthropic Messages API
// 负责将 OpenAI 兼容请求转换为 Anthropic 原生协议，并将响应转回 OpenAI 兼容格式。
type AnthropicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
	logger     *zap.Logger
}

// NewAnthropicClient 创建 Anthropic 客户端
func NewAnthropicClient(apiKey, baseURL string, timeout time.Duration) *AnthropicClient {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
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
	return &AnthropicClient{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Transport: transport,
		},
		timeout: timeout,
		logger:  zap.NewNop(),
	}
}

// SetLogger 设置日志器
func (c *AnthropicClient) SetLogger(logger *zap.Logger) {
	if logger != nil {
		c.logger = logger
	}
}

// Chat 非流式对话
func (c *AnthropicClient) Chat(ctx context.Context, req ChatRequest) (*ChatChunk, error) {
	anthropicReq, err := c.toAnthropicRequest(req, false)
	if err != nil {
		return nil, fmt.Errorf("构造 Anthropic 请求失败: %w", err)
	}

	body, err := c.doRequest(ctx, anthropicReq)
	if err != nil {
		return nil, err
	}

	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析 Anthropic 响应失败: %w", err)
	}

	content, reasoning, toolCalls := c.extractContent(resp.Content)
	return &ChatChunk{
		Delta:            content,
		ReasoningContent: reasoning,
		FinishReason:     resp.StopReason,
		ToolCalls:        toolCalls,
		Usage: &Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.PromptTokens + resp.Usage.CompletionTokens,
		},
	}, nil
}

// ChatStream 流式对话
func (c *AnthropicClient) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error) {
	anthropicReq, err := c.toAnthropicRequest(req, true)
	if err != nil {
		return nil, fmt.Errorf("构造 Anthropic 流式请求失败: %w", err)
	}

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
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
		var lastUsage Usage
		var usageEmitted bool
		var lastLines []string
		// AR-10：流式 tool_use 累积（index -> {id, name, args}）
		type streamToolUse struct {
			id, name string
			args     strings.Builder
		}
		toolUses := map[int]*streamToolUse{}
		var toolUseEmitted bool

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

			// Anthropic SSE 格式：event: xxx\ndata: {...}
			if strings.HasPrefix(line, "event:") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}

			var event anthropicStreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				c.logger.Warn("解析 Anthropic SSE 事件失败", zap.String("data", data), zap.Error(err))
				continue
			}

			switch event.Type {
			case "content_block_start":
				// AR-10：tool_use 块起始，记录 index -> {id, name}
				if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
					toolUses[event.Index] = &streamToolUse{id: event.ContentBlock.ID, name: event.ContentBlock.Name}
				}
			case "content_block_delta":
				delta := event.Delta
				if delta.Type == "text_delta" && delta.Text != "" {
					accumulatedContent.WriteString(delta.Text)
					chunk := ChatChunk{Delta: delta.Text}
					select {
					case chunkChan <- chunk:
					case <-ctx.Done():
						return
					}
				} else if delta.Type == "thinking_delta" && delta.Thinking != "" {
					accumulatedReasoning.WriteString(delta.Thinking)
					chunk := ChatChunk{ReasoningContent: delta.Thinking}
					select {
					case chunkChan <- chunk:
					case <-ctx.Done():
						return
					}
				} else if delta.Type == "input_json_delta" && delta.PartialJSON != "" {
					// AR-10：tool_use 参数增量累积
					if tu := toolUses[event.Index]; tu != nil {
						tu.args.WriteString(delta.PartialJSON)
					}
				}
			case "content_block_stop":
				// AR-10：tool_use 块结束，发出完整 ToolCall
				if tu := toolUses[event.Index]; tu != nil {
					toolUseEmitted = true
					chunk := ChatChunk{ToolCalls: []ToolCall{{
						ID:       tu.id,
						Type:     "function",
						Function: ToolCallFunction{Name: tu.name, Arguments: tu.args.String()},
					}}}
					select {
					case chunkChan <- chunk:
					case <-ctx.Done():
						return
					}
					delete(toolUses, event.Index)
				}
			case "message_delta":
				if event.Usage.CompletionTokens > 0 {
					lastUsage.CompletionTokens = event.Usage.CompletionTokens
				}
			case "message_start":
				if event.Message != nil && event.Message.Usage.PromptTokens > 0 {
					lastUsage.PromptTokens = event.Message.Usage.PromptTokens
				}
			case "message_stop":
				// 流结束，可补发 usage
			}
		}

		if accumulatedContent.Len() == 0 && accumulatedReasoning.Len() == 0 && !toolUseEmitted {
			c.logger.Warn("Anthropic 上游流式响应未返回任何内容",
				zap.String("baseURL", c.baseURL),
				zap.Strings("last_lines", lastLines),
				zap.Error(scanner.Err()),
			)
		}

		if !usageEmitted && (lastUsage.PromptTokens > 0 || lastUsage.CompletionTokens > 0) {
			lastUsage.TotalTokens = lastUsage.PromptTokens + lastUsage.CompletionTokens
			select {
			case chunkChan <- ChatChunk{Usage: &lastUsage}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return chunkChan, nil
}

func (c *AnthropicClient) doRequest(ctx context.Context, anthropicReq interface{}) ([]byte, error) {
	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, err
	}

	// 非流式请求设置整体超时
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(callCtx, "POST", c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
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

// toAnthropicRequest 将 OpenAI 兼容请求转换为 Anthropic Messages API 请求（AR-10 补齐工具调用透传）：
//   - req.Tools（OpenAI function schema）-> Anthropic tools（input_schema）；
//   - assistant 消息的 ToolCalls -> tool_use content 块；
//   - tool 角色消息 -> user 消息的 tool_result 块（连续 tool 消息合并到同一 user，满足角色交替约束）。
func (c *AnthropicClient) toAnthropicRequest(req ChatRequest, stream bool) (*anthropicRequest, error) {
	var systemParts []string
	var messages []anthropicMessage

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			systemParts = append(systemParts, contentToString(msg.Content))
			continue
		}

		// AR-10：工具结果消息 -> user + tool_result 块（连续 tool 合并到同一 user 消息）
		if msg.Role == "tool" {
			resultBlock := anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   contentToString(msg.Content),
			}
			if n := len(messages); n > 0 && messages[n-1].Role == "user" &&
				len(messages[n-1].Content) > 0 && messages[n-1].Content[0].Type == "tool_result" {
				messages[n-1].Content = append(messages[n-1].Content, resultBlock)
			} else {
				messages = append(messages, anthropicMessage{Role: "user", Content: []anthropicContentBlock{resultBlock}})
			}
			continue
		}

		// AR-10：assistant 消息带 ToolCalls -> text 块 + tool_use 块
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			blocks, err := c.toAnthropicContent(msg.Content)
			if err != nil {
				return nil, err
			}
			for _, tc := range msg.ToolCalls {
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: parseToolCallInput(tc.Function.Arguments),
				})
			}
			messages = append(messages, anthropicMessage{Role: "assistant", Content: blocks})
			continue
		}

		blocks, err := c.toAnthropicContent(msg.Content)
		if err != nil {
			return nil, err
		}
		messages = append(messages, anthropicMessage{Role: msg.Role, Content: blocks})
	}

	anthropicReq := &anthropicRequest{
		Model:     req.Model,
		Messages:  messages,
		Stream:    stream,
		MaxTokens: 4096,
	}
	if req.MaxTokens > 0 {
		anthropicReq.MaxTokens = req.MaxTokens
	}
	if len(systemParts) > 0 {
		anthropicReq.System = strings.Join(systemParts, "\n\n")
	}
	// AR-10：工具定义透传
	anthropicReq.Tools = toAnthropicTools(req.Tools)
	// AR-10：tool_choice 映射（auto/required/none -> auto/any/none）
	switch req.ToolChoice {
	case "auto":
		anthropicReq.ToolChoice = &anthropicToolChoice{Type: "auto"}
	case "required":
		anthropicReq.ToolChoice = &anthropicToolChoice{Type: "any"}
	case "none":
		anthropicReq.ToolChoice = &anthropicToolChoice{Type: "none"}
	}

	return anthropicReq, nil
}

// toAnthropicTools 将 OpenAI 工具定义（{type:function, function:{name,description,parameters}}）
// 转换为 Anthropic 工具（{name, description, input_schema}）。parameters 即 JSON Schema，结构互通。
func toAnthropicTools(tools []map[string]interface{}) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropicTool, 0, len(tools))
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
		out = append(out, anthropicTool{Name: name, Description: desc, InputSchema: params})
	}
	return out
}

// parseToolCallInput 将工具调用参数 JSON 字符串解析为 map；空串或解析失败返回空 map。
func parseToolCallInput(arguments string) map[string]interface{} {
	if arguments == "" {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}

// toAnthropicContent 将 OpenAI 兼容 content 转换为 Anthropic content blocks
func (c *AnthropicClient) toAnthropicContent(content interface{}) ([]anthropicContentBlock, error) {
	switch v := content.(type) {
	case string:
		if v == "" {
			return []anthropicContentBlock{}, nil
		}
		return []anthropicContentBlock{{Type: "text", Text: v}}, nil
	case []interface{}:
		var blocks []anthropicContentBlock
		for _, item := range v {
			part, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			partType, _ := part["type"].(string)
			switch partType {
			case "text":
				if text, ok := part["text"].(string); ok && text != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: text})
				}
			case "image_url":
				imageURLPart, ok := part["image_url"].(map[string]interface{})
				if !ok {
					continue
				}
				url, _ := imageURLPart["url"].(string)
				block, err := c.parseImageURL(url)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, block)
			case "file":
				filePart, ok := part["file"].(map[string]interface{})
				if !ok {
					continue
				}
				// 文本文件直接提取 text 拼接到内容中
				if text, ok := filePart["text"].(string); ok && text != "" {
					name, _ := filePart["name"].(string)
					if name != "" {
						blocks = append(blocks, anthropicContentBlock{Type: "text", Text: fmt.Sprintf("【文件：%s】\n%s", name, text)})
					} else {
						blocks = append(blocks, anthropicContentBlock{Type: "text", Text: text})
					}
				}
			default:
				// 未知类型忽略，避免阻塞
			}
		}
		return blocks, nil
	default:
		// 其他类型统一序列化为文本
		s := contentToString(content)
		if s == "" {
			return []anthropicContentBlock{}, nil
		}
		return []anthropicContentBlock{{Type: "text", Text: s}}, nil
	}
}

// parseImageURL 将 data URL 解析为 Anthropic image source
func (c *AnthropicClient) parseImageURL(url string) (anthropicContentBlock, error) {
	if url == "" {
		return anthropicContentBlock{}, fmt.Errorf("图片 URL 为空")
	}

	// 支持公开 URL 或 base64 data URL；当前附件一般为 base64 data URL
	if strings.HasPrefix(url, "data:") {
		mediaType, data, err := parseDataURL(url)
		if err != nil {
			return anthropicContentBlock{}, fmt.Errorf("解析图片 data URL 失败: %w", err)
		}
		return anthropicContentBlock{
			Type: "image",
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: mediaType,
				Data:      data,
			},
		}, nil
	}

	// 公开 URL 暂不支持，需要额外下载；当前场景下附件都是 base64
	return anthropicContentBlock{}, fmt.Errorf("Anthropic 暂不支持非 base64 图片 URL")
}

// extractContent 从 Anthropic 响应 content blocks 中提取文本、思考内容与工具调用（AR-10 补 tool_use）
func (c *AnthropicClient) extractContent(blocks []anthropicContentBlock) (string, string, []ToolCall) {
	var contents []string
	var reasoning []string
	var toolCalls []ToolCall
	for _, block := range blocks {
		switch block.Type {
		case "text":
			contents = append(contents, block.Text)
		case "thinking":
			reasoning = append(reasoning, block.Thinking)
		case "tool_use":
			inputJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				ID:       block.ID,
				Type:     "function",
				Function: ToolCallFunction{Name: block.Name, Arguments: string(inputJSON)},
			})
		}
	}
	return strings.Join(contents, ""), strings.Join(reasoning, ""), toolCalls
}

// parseDataURL 解析 data:image/png;base64,xxx 格式，返回 media_type 与 base64 数据字符串
func parseDataURL(dataURL string) (mediaType, data string, err error) {
	re := regexp.MustCompile(`^data:([^;]+);base64,(.*)$`)
	matches := re.FindStringSubmatch(dataURL)
	if len(matches) != 3 {
		return "", "", fmt.Errorf("非法 data URL 格式")
	}
	mediaType = matches[1]
	data = matches[2]
	// 校验 base64 是否合法
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return "", "", fmt.Errorf("非法 base64 数据: %w", err)
	}
	return mediaType, data, nil
}

// anthropicRequest Anthropic Messages API 请求体
type anthropicRequest struct {
	Model      string               `json:"model"`
	Messages   []anthropicMessage   `json:"messages"`
	System     string               `json:"system,omitempty"`
	MaxTokens  int                  `json:"max_tokens"`
	Stream     bool                 `json:"stream,omitempty"`
	Tools      []anthropicTool      `json:"tools,omitempty"`
	ToolChoice *anthropicToolChoice `json:"tool_choice,omitempty"`
}

// anthropicTool Anthropic 工具定义（由 OpenAI function schema 转换而来，AR-10）
type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// anthropicToolChoice 工具选择策略（OpenAI auto/required/none -> Anthropic auto/any/none）
type anthropicToolChoice struct {
	Type string `json:"type"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type     string                `json:"type"`
	Text     string                `json:"text,omitempty"`
	Thinking string                `json:"thinking,omitempty"`
	Source   *anthropicImageSource `json:"source,omitempty"`
	// AR-10：工具调用块（type=tool_use，assistant 消息/响应）
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
	// AR-10：工具结果块（type=tool_result，user 消息）
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// anthropicResponse 非流式响应
type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicUsage struct {
	PromptTokens     int `json:"input_tokens"`
	CompletionTokens int `json:"output_tokens"`
}

// anthropicStreamEvent 流式 SSE 事件
type anthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index,omitempty"`
	Delta        anthropicStreamDelta   `json:"delta,omitempty"`
	Usage        anthropicUsage         `json:"usage,omitempty"`
	Message      *anthropicMessageStart `json:"message,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
}

type anthropicStreamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"` // AR-10：tool_use 参数增量
}

type anthropicMessageStart struct {
	Usage anthropicUsage `json:"usage"`
}
