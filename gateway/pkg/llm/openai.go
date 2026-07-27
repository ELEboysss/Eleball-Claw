package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// OpenAIClient 适配 OpenAI 兼容格式的厂商（OpenAI、DeepSeek、Moonshot 等）
type OpenAIClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
	logger     *zap.Logger
}

// NewOpenAIClient 创建客户端
// timeout 用于控制连接/响应头超时；非流式请求会用它作为整体超时，
// 流式请求不设总超时，避免慢首包或长连接被提前切断。
func NewOpenAIClient(apiKey, baseURL string, timeout time.Duration) *OpenAIClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	// 自定义 Transport：重点控制响应头返回时间，不限制整个流式读取时长
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       90 * time.Second,
	}
	return &OpenAIClient{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Transport: transport,
			// 不设置总 Timeout，流式响应由调用方 context 控制生命周期
		},
		timeout: timeout,
		logger:  zap.NewNop(),
	}
}

// SetLogger 设置日志器，便于上层注入统一日志
func (c *OpenAIClient) SetLogger(logger *zap.Logger) {
	if logger != nil {
		c.logger = logger
	}
}

// Chat 非流式对话
func (c *OpenAIClient) Chat(ctx context.Context, req ChatRequest) (*ChatChunk, error) {
	req.Stream = false
	// 非流式请求设置整体超时，防止上游无响应时一直挂起
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	body, err := c.doRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	var resp openAIResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if len(resp.Choices) == 0 {
		c.logger.Warn("上游非流式响应为空 Choices",
			zap.String("baseURL", c.baseURL),
			zap.Int("resp_body_len", len(body)),
			zap.String("resp_body_head", truncateString(string(body), 500)),
		)
		return nil, fmt.Errorf("空响应: %s", truncateString(string(body), 500))
	}

	message := resp.Choices[0].Message
	content := contentToString(message.Content)
	reasoning := contentToString(message.ReasoningContent)

	// Choices 存在但 content / reasoning_content 均为空时记录响应详情，便于排查
	if content == "" && reasoning == "" && len(message.ToolCalls) == 0 {
		c.logger.Warn("上游非流式响应 Choices 存在但内容为空",
			zap.String("baseURL", c.baseURL),
			zap.Int("resp_body_len", len(body)),
			zap.String("resp_body_head", truncateString(string(body), 500)),
		)
	}
	usage := normalizeUsage(resp.Usage, resp.InputTokens, resp.OutputTokens)
	if usage == nil {
		// 上游未返回 usage 时做兜底估算，避免漏记漏扣
		usage = EstimateUsageFromMessages(req.Messages, content)
	}
	return &ChatChunk{
		Delta:            content,
		ReasoningContent: reasoning,
		Usage:            usage,
		ToolCalls:        message.ToolCalls,
	}, nil
}

// ChatStream 流式对话，SSE 转发
func (c *OpenAIClient) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error) {
	req.Stream = true
	// 请求上游在最终 chunk 中返回 usage，否则无法精确计费
	req.StreamOptions = &StreamOptions{IncludeUsage: true}
	body, err := marshalOpenAIBody(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		return nil, &UpstreamError{StatusCode: httpResp.StatusCode, Body: string(body)}
	}

	chunkChan := make(chan ChatChunk, 10)
	go func() {
		defer close(chunkChan)
		defer httpResp.Body.Close()

		var accumulatedDelta strings.Builder
		var accumulatedReasoning strings.Builder
		var usageEmitted bool
		var lineCount int
		var dataEventCount int
		var emptyChoiceCount int
		var usageEventCount int
		var finishReason string
		// 保留最后若干条原始 SSE 行，便于排查空响应问题
		lastLines := make([]string, 0, 10)

		scanner := bufio.NewScanner(httpResp.Body)
		// 部分厂商（如 Kimi Code）可能返回较长的 JSON 行，放宽扫描限制
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			lineCount++
			if line == "" {
				continue
			}
			lastLines = append(lastLines, line)
			if len(lastLines) > 10 {
				lastLines = lastLines[1:]
			}
			// 兼容不同厂商 SSE 格式：data: <json>（标准）与 data:<json>（如 Kimi Coding）
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			dataEventCount++
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}

			var streamResp openAIStreamResp
			if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
				continue
			}
			// 兼容最终 usage chunk：choices 为空但 usage 或 input/output tokens 不为空
			if len(streamResp.Choices) == 0 && streamResp.Usage == nil && streamResp.InputTokens == 0 && streamResp.OutputTokens == 0 {
				continue
			}

			var delta string
			var reasoning string
			if len(streamResp.Choices) > 0 {
				choice := streamResp.Choices[0]
				delta = contentToString(choice.Delta.Content)
				reasoning = contentToString(choice.Delta.ReasoningContent)
				finishReason = choice.FinishReason
				if delta == "" && reasoning == "" {
					emptyChoiceCount++
				}
			} else {
				usageEventCount++
			}
			accumulatedDelta.WriteString(delta)
			accumulatedReasoning.WriteString(reasoning)
			chunk := ChatChunk{
				Delta:            delta,
				ReasoningContent: reasoning,
				FinishReason:     finishReason,
				Usage:            normalizeUsage(streamResp.Usage, streamResp.InputTokens, streamResp.OutputTokens),
			}
			if chunk.Usage != nil {
				usageEmitted = true
			}
			select {
			case chunkChan <- chunk:
			case <-ctx.Done():
				return
			}
		}

		// 上游未返回任何有效内容时打印调试日志，便于定位“模型未返回任何内容”问题
		if accumulatedDelta.Len() == 0 && accumulatedReasoning.Len() == 0 {
			c.logger.Warn("上游流式响应未返回任何内容",
				zap.String("baseURL", c.baseURL),
				zap.Int("sse_lines", lineCount),
				zap.Int("data_events", dataEventCount),
				zap.Int("empty_choices", emptyChoiceCount),
				zap.Int("usage_events", usageEventCount),
				zap.String("finish_reason", finishReason),
				zap.Error(scanner.Err()),
				zap.Strings("last_sse_lines", lastLines),
			)
		}

		// 上游未返回 usage 且流正常结束时，补发一个带有兜底估算的 final chunk
		if !usageEmitted && scanner.Err() == nil {
			usage := EstimateUsageFromMessages(req.Messages, accumulatedDelta.String())
			select {
			case chunkChan <- ChatChunk{Usage: usage}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return chunkChan, nil
}

// marshalOpenAIBody 将 ChatRequest 序列化为 OpenAI 兼容请求体。
// AR-19 P2-3：当 Thinking.Effort 设置时，提升为顶层 reasoning_effort（OpenAI o 系列 Chat Completions
// 识别字段，参考 OmniRoute / jcode openai-runtime 约定）；原 thinking 字段保留以兼容 Kimi/Moonshot。
func marshalOpenAIBody(req ChatRequest) ([]byte, error) {
	type wrapper struct {
		ChatRequest
		ReasoningEffort string `json:"reasoning_effort,omitempty"`
	}
	w := wrapper{ChatRequest: req}
	if req.Thinking != nil && req.Thinking.Effort != "" {
		w.ReasoningEffort = req.Thinking.Effort
	}
	return json.Marshal(w)
}

func (c *OpenAIClient) doRequest(ctx context.Context, req ChatRequest) ([]byte, error) {
	body, err := marshalOpenAIBody(req)
	if err != nil {
		return nil, err
	}
	if c.logger != nil {
		c.logger.Debug("OpenAIClient 请求上游", zap.String("baseURL", c.baseURL), zap.String("body", truncateString(string(body), 2000)))
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
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

// Embed 调用 OpenAI 兼容 /embeddings 端点获取向量（AR-09 记忆检索）。
// model 为空时返回错误；inputs 为空时返回 nil；timeout 复用客户端配置作为整体超时。
// 鉴权与 baseUrl 复用 Chat 的同套配置（EleAgent 模型中心 OpenAI 兼容）。
func (c *OpenAIClient) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if model == "" {
		return nil, fmt.Errorf("embedding model 未配置")
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	body, err := json.Marshal(EmbeddingRequest{Model: model, Input: inputs})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if c.logger != nil {
		c.logger.Debug("OpenAIClient 请求 embedding",
			zap.String("baseURL", c.baseURL),
			zap.String("model", model),
			zap.Int("inputs", len(inputs)))
	}
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
	var resp EmbeddingResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("解析 embedding 响应失败: %w", err)
	}
	out := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// contentToString 将上游返回的 content（可能是 string 或数组）统一转为字符串。
// 流式场景下 delta content 通常为 string；非流式完整响应中也可能出现数组形式。
func contentToString(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	// 其他类型（如 content parts 数组）序列化为 JSON 字符串返回
	b, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprintf("%v", content)
	}
	return string(b)
}

// openAIResp 非流式响应结构
type openAIResp struct {
	Choices []struct {
		Message struct {
			Role             string      `json:"role"`
			Content          interface{} `json:"content"`           // 支持 string 或 content parts 数组
			ReasoningContent interface{} `json:"reasoning_content"` // Kimi / DeepSeek 等模型的思考过程
			ToolCalls        []ToolCall  `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage        *Usage `json:"usage"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// openAIStreamResp 流式响应结构
type openAIStreamResp struct {
	Choices []struct {
		Delta struct {
			Content          interface{} `json:"content"`           // 通常为 string
			ReasoningContent interface{} `json:"reasoning_content"` // Kimi / DeepSeek 等模型的思考过程
			ToolCalls        []ToolCall  `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage        *Usage `json:"usage"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// normalizeUsage 统一处理 OpenAI 格式与部分厂商的 input/output tokens 别名。
// 优先使用标准的 usage 字段；若不存在但存在 input_tokens/output_tokens，则构造等价的 Usage。
func normalizeUsage(usage *Usage, inputTokens, outputTokens int) *Usage {
	if usage != nil {
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		return usage
	}
	if inputTokens > 0 || outputTokens > 0 {
		return &Usage{
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      inputTokens + outputTokens,
		}
	}
	return nil
}

// truncateString 截断字符串，避免日志过长
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
