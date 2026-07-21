package service

import (
	"context"
	"errors"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/pkg/llm"
)

// UpstreamRateLimitedError 表示上游视觉生成厂商触发限流（如 429）。
// 业务层捕获后可转换为友好的"服务繁忙"提示。
var UpstreamRateLimitedError = errors.New("上游视觉服务繁忙，请稍后再试")

// UpstreamQueueFullError 表示上游视觉生成厂商队列已满（如 503 image queue is full）。
// 业务层捕获后可进入重试，并将任务状态保持为 pending，让前端显示排队中。
var UpstreamQueueFullError = errors.New("上游视觉服务队列已满，请稍后再试")

// VisualInputAsset 输入资源
type VisualInputAsset struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// VisualResult 生成结果
type VisualResult struct {
	URL      string   `json:"url,omitempty"`
	URLs     []string `json:"urls,omitempty"`
	B64JSON  string   `json:"b64_json,omitempty"`
	CoverURL string   `json:"cover_url,omitempty"`
	Width    int      `json:"width,omitempty"`
	Height   int      `json:"height,omitempty"`
	Seconds  float64  `json:"seconds,omitempty"`
	FPS      int      `json:"fps,omitempty"`
	Size     string   `json:"size,omitempty"`
}

// VisualUsage 用量信息
type VisualUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// VisualCreateRequest 创建视觉生成任务请求
type VisualCreateRequest struct {
	Provider  string
	Model     string
	Prompt    string
	ImageURL  string
	ImageURLs []string
	// Messages 用于原生多模态对话协议（openai_image / openai_video），直接透传历史对话上下文。
	Messages []llm.Message
	Params   map[string]interface{}
}

// VisualCreateResult 创建任务返回
type VisualCreateResult struct {
	UpstreamTaskID string
	Status         model.VisualTaskStatus
	Result         *VisualResult
	Usage          *VisualUsage
	ErrorMessage   string
}

// VisualQueryResult 查询任务返回
type VisualQueryResult struct {
	Status       model.VisualTaskStatus
	Progress     int
	Result       *VisualResult
	Usage        *VisualUsage
	ErrorMessage string
}

// VisualProvider 视觉生成 Provider 统一接口
// 每个 Provider 实现对应一种上游视觉生成 API（Agnes Image / Agnes Video / Seedance 等）。
// 注意：具体使用哪个 Provider 实现由 EleAgentModelConfig.Protocol 决定，而非 ProviderName()。
type VisualProvider interface {
	// ProviderName 返回 Provider 标识，仅用于日志/调试，不再作为路由 key。
	ProviderName() string
	// MediaType 支持的媒体类型：image / video
	MediaType() model.VisualMediaType
	// Create 创建生成任务
	Create(ctx context.Context, req *VisualCreateRequest) (*VisualCreateResult, error)
	// Query 查询任务状态
	Query(ctx context.Context, upstreamTaskID string) (*VisualQueryResult, error)
	// Cancel 取消任务（仅视频类 Provider 需要实现）
	Cancel(ctx context.Context, upstreamTaskID string) error
}
