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

	content, reasoning := c.extractContent(resp.Content)
	return &ChatChunk{
		Delta:            content,
		ReasoningContent: reasoning,
		FinishReason:     resp.StopReason,
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

		if accumulatedContent.Len() == 0 && accumulatedReasoning.Len() == 0 {
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

// toAnthropicRequest 将 OpenAI 兼容请求转换为 Anthropic Messages API 请求
func (c *AnthropicClient) toAnthropicRequest(req ChatRequest, stream bool) (*anthropicRequest, error) {
	var systemParts []string
	var messages []anthropicMessage

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			systemParts = append(systemParts, contentToString(msg.Content))
			continue
		}

		blocks, err := c.toAnthropicContent(msg.Content)
		if err != nil {
			return nil, err
		}
		role := msg.Role
		if role == "tool" {
			// Anthropic 没有 tool role，临时降级为 user
			role = "user"
		}
		messages = append(messages, anthropicMessage{
			Role:    role,
			Content: blocks,
		})
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

	return anthropicReq, nil
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

// extractContent 从 Anthropic 响应 content blocks 中提取文本与思考内容
func (c *AnthropicClient) extractContent(blocks []anthropicContentBlock) (string, string) {
	var contents []string
	var reasoning []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			contents = append(contents, block.Text)
		case "thinking":
			reasoning = append(reasoning, block.Thinking)
		}
	}
	return strings.Join(contents, ""), strings.Join(reasoning, "")
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
	Model     string              `json:"model"`
	Messages  []anthropicMessage  `json:"messages"`
	System    string              `json:"system,omitempty"`
	MaxTokens int                 `json:"max_tokens"`
	Stream    bool                `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string                   `json:"role"`
	Content []anthropicContentBlock  `json:"content"`
}

type anthropicContentBlock struct {
	Type   string                 `json:"type"`
	Text   string                 `json:"text,omitempty"`
	Thinking string               `json:"thinking,omitempty"`
	Source *anthropicImageSource  `json:"source,omitempty"`
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
	Type         string                  `json:"type"`
	Index        int                     `json:"index,omitempty"`
	Delta        anthropicStreamDelta    `json:"delta,omitempty"`
	Usage        anthropicUsage          `json:"usage,omitempty"`
	Message      *anthropicMessageStart  `json:"message,omitempty"`
	ContentBlock *anthropicContentBlock  `json:"content_block,omitempty"`
}

type anthropicStreamDelta struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	Thinking   string `json:"thinking,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

type anthropicMessageStart struct {
	Usage anthropicUsage `json:"usage"`
}
