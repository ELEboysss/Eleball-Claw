package llm

import (
	"context"
)

// ContentPart 表示一条消息中的多模态内容片段，兼容 OpenAI / Kimi 的 content parts 格式
type ContentPart struct {
	Type     string    `json:"type"`                // text / image_url / file
	Text     string    `json:"text,omitempty"`      // type=text 时使用
	ImageURL *ImageURL `json:"image_url,omitempty"` // type=image_url 时使用
	File     *FilePart `json:"file,omitempty"`      // type=file 时使用
}

// ImageURL 图片内容
type ImageURL struct {
	URL    string `json:"url"`              // 支持公开 URL 或 data:image/...;base64,...
	Detail string `json:"detail,omitempty"` // auto / low / high
}

// FilePart 文件内容（文本类文件直接传 text，二进制文件可传 base64 data URL）
type FilePart struct {
	Name     string `json:"name,omitempty"`      // 文件名
	MimeType string `json:"mimeType,omitempty"`  // MIME 类型
	Text     string `json:"text,omitempty"`      // 已提取的文本内容
	Data     string `json:"data,omitempty"`      // data:application/...;base64,... 形式
}

// Message 对话消息
// Content 支持 string 或 []ContentPart 两种形式：
//   - 纯文本对话保持 string，兼容旧客户端
//   - 多模态对话使用 []ContentPart，可包含 text / image_url / file
type Message struct {
	Role         string        `json:"role"`
	Content      interface{}   `json:"content,omitempty"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
	Name         string        `json:"name,omitempty"`
}

// ToolCall 模型生成的工具调用
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction 工具调用函数定义
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamOptions 流式选项
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ThinkingOptions Kimi / Moonshot 思考模式选项（OpenAI 兼容扩展字段）
type ThinkingOptions struct {
	Type string `json:"type"`          // enabled / disabled
	Keep string `json:"keep,omitempty"` // 多轮对话中保留 reasoning_content，如 "all"
}

// ChatRequest 对话请求
// 字段覆盖标准 OpenAI Chat Completions 与 Kimi Code 兼容扩展：
//   - max_completion_tokens / thinking / prompt_cache_key / safety_identifier 为 Kimi 系列模型特有
//   - stop / top_p 为通用 OpenAI 兼容字段
// 所有扩展字段均为可选，未设置时不参与序列化，避免影响其他厂商。
type ChatRequest struct {
	Model               string                 `json:"model"`
	Messages            []Message              `json:"messages"`
	Temperature         float64                `json:"temperature,omitempty"`
	TopP                float64                `json:"top_p,omitempty"`
	MaxTokens           int                    `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                    `json:"max_completion_tokens,omitempty"`
	Stream              bool                   `json:"stream,omitempty"`
	StreamOptions       *StreamOptions         `json:"stream_options,omitempty"`
	Thinking            *ThinkingOptions       `json:"thinking,omitempty"`
	PromptCacheKey      string                 `json:"prompt_cache_key,omitempty"`
	SafetyIdentifier    string                 `json:"safety_identifier,omitempty"`
	Stop                []string               `json:"stop,omitempty"`
	Tools               []map[string]interface{} `json:"tools,omitempty"`
	ToolChoice          string                 `json:"tool_choice,omitempty"`
}

// ChatChunk 流式/非流式响应块
// Delta 为模型最终输出内容；ReasoningContent 为 Kimi / DeepSeek 等模型的思考过程（可选）
type ChatChunk struct {
	Delta             string     `json:"delta"`
	ReasoningContent  string     `json:"reasoning_content,omitempty"`
	FinishReason      string     `json:"finish_reason,omitempty"`
	Usage             *Usage     `json:"usage,omitempty"`
	ToolCalls         []ToolCall `json:"tool_calls,omitempty"`
}

// Usage Token 用量
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens,omitempty"` // Kimi 系列返回的缓存命中 token 数
}

// Client LLM 客户端统一接口
type Client interface {
	// Chat 非流式对话，返回完整响应块（含内容、思考过程与用量）
	Chat(ctx context.Context, req ChatRequest) (*ChatChunk, error)
	// ChatStream 流式对话，返回逐字通道
	ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error)
}

// Provider 模型厂商标识
type Provider string

const (
	ProviderOpenAI   Provider = "openai"
	ProviderClaude   Provider = "claude"
	ProviderDeepSeek Provider = "deepseek"
	ProviderEleAgent Provider = "eleagent"
)
