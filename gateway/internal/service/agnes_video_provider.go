package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/eleball/gateway/internal/model"
)

const (
	// AgnesVideoAPIEndpoint Agnes Video 创建任务默认端点
	AgnesVideoAPIEndpoint = "https://apihub.agnes-ai.com/v1/videos"
	// AgnesVideoQueryEndpoint Agnes Video 查询结果默认端点
	AgnesVideoQueryEndpoint = "https://apihub.agnes-ai.com/agnesapi"
)

// normalizeAgnesNumFrames Agnes Video 要求 num_frames 满足 8*n+1（如 81/121），
// 将由 duration×fps 得到的任意帧数规整到最近的合法值；下限 9 帧（1 帧不成视频）。
func normalizeAgnesNumFrames(frames int) int {
	if frames <= 9 {
		return 9
	}
	return 8*((frames-1+4)/8) + 1
}

// AgnesVideoProvider Agnes Video 视频生成 Provider
type AgnesVideoProvider struct {
	createEndpoint string
	queryEndpoint  string
	apiKey         string
	httpClient     *http.Client
}

// NewAgnesVideoProvider 创建 Agnes Video Provider
func NewAgnesVideoProvider(createEndpoint, queryEndpoint, apiKey string) *AgnesVideoProvider {
	if createEndpoint == "" {
		createEndpoint = AgnesVideoAPIEndpoint
	}
	if queryEndpoint == "" {
		queryEndpoint = AgnesVideoQueryEndpoint
	}
	return &AgnesVideoProvider{
		createEndpoint: createEndpoint,
		queryEndpoint:  queryEndpoint,
		apiKey:         apiKey,
		httpClient:     &http.Client{Timeout: 60 * time.Second},
	}
}

// ProviderName 返回 Provider 名称
func (p *AgnesVideoProvider) ProviderName() string {
	return string(model.VisualProviderAgnesVideo)
}

// MediaType 返回媒体类型
func (p *AgnesVideoProvider) MediaType() model.VisualMediaType {
	return model.VisualMediaTypeVideo
}

// agnesVideoCreateRequest Agnes Video 创建请求体
type agnesVideoCreateRequest struct {
	Model           string                 `json:"model"`
	Prompt          string                 `json:"prompt"`
	Image           string                 `json:"image,omitempty"`
	Width           int                    `json:"width,omitempty"`
	Height          int                    `json:"height,omitempty"`
	NumFrames       int                    `json:"num_frames,omitempty"`
	FrameRate       float64                `json:"frame_rate,omitempty"`
	Seed            int                    `json:"seed,omitempty"`
	NegativePrompt  string                 `json:"negative_prompt,omitempty"`
	ExtraBody       map[string]interface{} `json:"extra_body,omitempty"`
}

// agnesVideoCreateResponse Agnes Video 创建响应体
type agnesVideoCreateResponse struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	VideoID   string `json:"video_id"`
	Object    string `json:"object"`
	Model     string `json:"model"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"`
	CreatedAt int64  `json:"created_at"`
	Seconds   string `json:"seconds"`
	Size      string `json:"size"`
}

// agnesVideoQueryResponse Agnes Video 查询响应体
type agnesVideoQueryResponse struct {
	ID        string `json:"id"`
	VideoID   string `json:"video_id"`
	Model     string `json:"model"`
	Object    string `json:"object"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"`
	Seconds   string `json:"seconds"`
	Size      string `json:"size"`
	URL       string `json:"url"`
	Error     interface{} `json:"error"`
}

// Create 创建视频生成任务
func (p *AgnesVideoProvider) Create(ctx context.Context, req *VisualCreateRequest) (*VisualCreateResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Agnes Video API Key 未配置")
	}

	// 合并参考图：用户上传 + 历史结果
	var inputImages []string
	if req.ImageURL != "" {
		inputImages = append(inputImages, req.ImageURL)
	}
	for _, u := range req.ImageURLs {
		if u != "" {
			inputImages = append(inputImages, u)
		}
	}

	body := agnesVideoCreateRequest{
		Model:  req.Model,
		Prompt: req.Prompt,
		Image:  req.ImageURL,
	}
	if len(inputImages) > 0 {
		body.Image = inputImages[0]
	}

	if req.Params != nil {
		if v, ok := req.Params["width"].(float64); ok {
			body.Width = int(v)
		}
		if v, ok := req.Params["height"].(float64); ok {
			body.Height = int(v)
		}
		// duration 参数：秒数，转换为 num_frames（默认 24fps）
		if duration, ok := req.Params["duration"].(float64); ok && body.NumFrames == 0 {
			frameRate := 24.0
			if fr, ok := req.Params["frame_rate"].(float64); ok {
				frameRate = fr
			}
			body.NumFrames = int(duration * frameRate)
			body.FrameRate = frameRate
		}
		if v, ok := req.Params["num_frames"].(float64); ok {
			body.NumFrames = int(v)
		}
		// Agnes Video 要求 num_frames 满足 8*n+1，规整到最近的合法值
		if body.NumFrames > 0 {
			body.NumFrames = normalizeAgnesNumFrames(body.NumFrames)
		}
		if v, ok := req.Params["frame_rate"].(float64); ok {
			body.FrameRate = v
		}
		if v, ok := req.Params["seed"].(float64); ok {
			body.Seed = int(v)
		}
		if v, ok := req.Params["negative_prompt"].(string); ok {
			body.NegativePrompt = v
		}
		// 关键帧动画：extra_body.image + mode
		if mode, ok := req.Params["mode"].(string); ok && mode == "keyframes" {
			extraBody := make(map[string]interface{})
			extraBody["mode"] = mode
			if len(inputImages) > 0 {
				extraBody["image"] = inputImages
			}
			body.ExtraBody = extraBody
			body.Image = "" // 关键帧模式不使用顶层 image
		}
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Agnes Video 请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.createEndpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Agnes Video 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Agnes Video 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, UpstreamRateLimitedError
		}
		return nil, fmt.Errorf("Agnes Video 返回错误: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	var agnesResp agnesVideoCreateResponse
	if err := json.Unmarshal(respBody, &agnesResp); err != nil {
		return nil, fmt.Errorf("解析 Agnes Video 响应失败: %w", err)
	}

	upstreamID := agnesResp.VideoID
	if upstreamID == "" {
		upstreamID = agnesResp.TaskID
	}
	if upstreamID == "" {
		upstreamID = agnesResp.ID
	}

	return &VisualCreateResult{
		UpstreamTaskID: upstreamID,
		Status:         mapAgnesVideoStatus(agnesResp.Status),
		Usage:          &VisualUsage{TotalTokens: 0},
	}, nil
}

// Query 查询视频生成任务状态
func (p *AgnesVideoProvider) Query(ctx context.Context, upstreamTaskID string) (*VisualQueryResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Agnes Video API Key 未配置")
	}

	url := p.queryEndpoint + "?video_id=" + upstreamTaskID
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Agnes Video 查询失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Agnes Video 查询响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, UpstreamRateLimitedError
		}
		return nil, fmt.Errorf("Agnes Video 查询返回错误: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	var agnesResp agnesVideoQueryResponse
	if err := json.Unmarshal(respBody, &agnesResp); err != nil {
		return nil, fmt.Errorf("解析 Agnes Video 查询响应失败: %w", err)
	}

	result := &VisualQueryResult{
		Status:   mapAgnesVideoStatus(agnesResp.Status),
		Progress: agnesResp.Progress,
	}

	if agnesResp.URL != "" {
		var seconds float64
		if s, err := strconv.ParseFloat(agnesResp.Seconds, 64); err == nil {
			seconds = s
		}
		result.Result = &VisualResult{
			URL:     agnesResp.URL,
			Seconds: seconds,
			Size:    agnesResp.Size,
		}
	}

	if agnesResp.Error != nil {
		result.ErrorMessage = fmt.Sprintf("%v", agnesResp.Error)
	}

	return result, nil
}

// Cancel 取消视频生成任务
func (p *AgnesVideoProvider) Cancel(ctx context.Context, upstreamTaskID string) error {
	// Agnes Video 当前文档未提供取消接口，返回 nil 表示已尽力
	return nil
}

// mapAgnesVideoStatus 映射 Agnes Video 状态到统一状态
func mapAgnesVideoStatus(status string) model.VisualTaskStatus {
	switch status {
	case "queued":
		return model.VisualTaskStatusPending
	case "in_progress":
		return model.VisualTaskStatusRunning
	case "completed":
		return model.VisualTaskStatusSucceeded
	case "failed":
		return model.VisualTaskStatusFailed
	default:
		return model.VisualTaskStatusPending
	}
}
