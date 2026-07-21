package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/eleball/gateway/internal/model"
)

const (
	// SeedanceDefaultBaseURL 火山方舟 Seedance 默认 BaseURL
	SeedanceDefaultBaseURL = "https://ark.cn-beijing.volces.com/api/v3"
)

// SeedanceProvider 火山引擎 Seedance 视频生成 Provider
type SeedanceProvider struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewSeedanceProvider 创建 Seedance Provider
func NewSeedanceProvider(baseURL, apiKey string) *SeedanceProvider {
	if baseURL == "" {
		baseURL = SeedanceDefaultBaseURL
	}
	return &SeedanceProvider{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// ProviderName 返回 Provider 名称
func (p *SeedanceProvider) ProviderName() string {
	return string(model.VisualProviderSeedance)
}

// MediaType 返回媒体类型
func (p *SeedanceProvider) MediaType() model.VisualMediaType {
	return model.VisualMediaTypeVideo
}

// seedanceContentPart Seedance content part
type seedanceContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

// seedanceCreateRequest Seedance 创建请求体
type seedanceCreateRequest struct {
	Model         string                `json:"model"`
	Content       []seedanceContentPart `json:"content"`
	Ratio         string                `json:"ratio,omitempty"`
	Duration      int                   `json:"duration,omitempty"`
	Resolution    string                `json:"resolution,omitempty"`
	GenerateAudio bool                  `json:"generate_audio,omitempty"`
	// Watermark 使用指针显式传递 false：上游默认 true，omitempty 会把 false 丢掉导致视频被加水印
	Watermark     *bool                 `json:"watermark,omitempty"`
}

// seedanceCreateResponse Seedance 创建响应体
type seedanceCreateResponse struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
}

// seedanceTaskResponse Seedance 任务查询响应体
// 注意：上游成功时 content 为对象（{"video_url": "..."}），不是数组。
type seedanceTaskResponse struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Content  *struct {
		Type     string `json:"type"`
		VideoURL string `json:"video_url,omitempty"`
		ImageURL string `json:"image_url,omitempty"`
	} `json:"content"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Create 创建视频生成任务
func (p *SeedanceProvider) Create(ctx context.Context, req *VisualCreateRequest) (*VisualCreateResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Seedance API Key 未配置")
	}

	content := []seedanceContentPart{
		{Type: "text", Text: req.Prompt},
	}
	// 追加参考图：用户上传 + 历史结果
	var inputImages []string
	if req.ImageURL != "" {
		inputImages = append(inputImages, req.ImageURL)
	}
	for _, u := range req.ImageURLs {
		if u != "" {
			inputImages = append(inputImages, u)
		}
	}
	for _, u := range inputImages {
		content = append(content, seedanceContentPart{
			Type: "image_url",
			ImageURL: &struct {
				URL string `json:"url"`
			}{URL: u},
		})
	}

	body := seedanceCreateRequest{
		Model:   req.Model,
		Content: content,
	}

	// 水印默认关闭并显式传递（上游默认 true），用户勾选时才置 true
	watermark := false
	if req.Params != nil {
		if v, ok := req.Params["ratio"].(string); ok {
			body.Ratio = v
		}
		if v, ok := req.Params["duration"].(float64); ok {
			body.Duration = int(v)
		}
		if v, ok := req.Params["resolution"].(string); ok {
			body.Resolution = v
		}
		if v, ok := req.Params["generate_audio"].(bool); ok {
			body.GenerateAudio = v
		}
		if v, ok := req.Params["watermark"].(bool); ok {
			watermark = v
		}
	}
	body.Watermark = &watermark

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Seedance 请求失败: %w", err)
	}

	url := p.baseURL + "/contents/generations/tasks"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Seedance 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Seedance 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, UpstreamRateLimitedError
		}
		return nil, fmt.Errorf("Seedance 返回错误: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	var seedanceResp seedanceCreateResponse
	if err := json.Unmarshal(respBody, &seedanceResp); err != nil {
		return nil, fmt.Errorf("解析 Seedance 响应失败: %w", err)
	}

	return &VisualCreateResult{
		UpstreamTaskID: seedanceResp.ID,
		Status:         mapSeedanceStatus(seedanceResp.Status),
		Usage:          &VisualUsage{TotalTokens: 0},
	}, nil
}

// Query 查询视频生成任务状态
func (p *SeedanceProvider) Query(ctx context.Context, upstreamTaskID string) (*VisualQueryResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Seedance API Key 未配置")
	}

	url := p.baseURL + "/contents/generations/tasks/" + upstreamTaskID
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Seedance 查询失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Seedance 查询响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, UpstreamRateLimitedError
		}
		return nil, fmt.Errorf("Seedance 查询返回错误: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	var taskResp seedanceTaskResponse
	if err := json.Unmarshal(respBody, &taskResp); err != nil {
		return nil, fmt.Errorf("解析 Seedance 查询响应失败: %w", err)
	}

	result := &VisualQueryResult{
		Status: mapSeedanceStatus(taskResp.Status),
		Usage: &VisualUsage{
			PromptTokens:     taskResp.Usage.PromptTokens,
			CompletionTokens: taskResp.Usage.CompletionTokens,
			TotalTokens:      taskResp.Usage.TotalTokens,
		},
	}

	if taskResp.Content != nil && taskResp.Content.VideoURL != "" {
		result.Result = &VisualResult{URL: taskResp.Content.VideoURL}
	}

	if taskResp.Error.Message != "" {
		result.ErrorMessage = fmt.Sprintf("[%s] %s", taskResp.Error.Code, taskResp.Error.Message)
		result.Status = model.VisualTaskStatusFailed
	}

	return result, nil
}

// Cancel 取消视频生成任务
func (p *SeedanceProvider) Cancel(ctx context.Context, upstreamTaskID string) error {
	// Seedance 文档中未明确提供取消接口，这里返回 nil 占位
	return nil
}

// mapSeedanceStatus 映射 Seedance 状态到统一状态
func mapSeedanceStatus(status string) model.VisualTaskStatus {
	switch status {
	case "queued":
		return model.VisualTaskStatusPending
	case "running":
		return model.VisualTaskStatusRunning
	case "succeeded":
		return model.VisualTaskStatusSucceeded
	case "failed":
		return model.VisualTaskStatusFailed
	case "cancelled":
		return model.VisualTaskStatusCancelled
	default:
		return model.VisualTaskStatusPending
	}
}
