package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
)

const (
	// AgnesImageAPIEndpoint Agnes Image API 默认端点
	AgnesImageAPIEndpoint = "https://apihub.agnes-ai.com/v1/images/generations"
)

// AgnesImageProvider Agnes Image 图片生成 Provider
type AgnesImageProvider struct {
	endpoint    string
	apiKey      string
	httpClient  *http.Client
}

// NewAgnesImageProvider 创建 Agnes Image Provider
// apiKey 为空时会从环境变量 AGNES_API_KEY 读取兜底。
func NewAgnesImageProvider(endpoint, apiKey string) *AgnesImageProvider {
	if endpoint == "" {
		endpoint = AgnesImageAPIEndpoint
	}
	if apiKey == "" {
		apiKey = ""
	}
	return &AgnesImageProvider{
		endpoint:   endpoint,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
}

// ProviderName 返回 Provider 名称
func (p *AgnesImageProvider) ProviderName() string {
	return string(model.VisualProviderAgnesImage)
}

// MediaType 返回媒体类型
func (p *AgnesImageProvider) MediaType() model.VisualMediaType {
	return model.VisualMediaTypeImage
}

// agnesImageCreateRequest Agnes Image 创建请求体
type agnesImageCreateRequest struct {
	Model      string                 `json:"model"`
	Prompt     string                 `json:"prompt"`
	Size       string                 `json:"size"`
	Ratio      string                 `json:"ratio,omitempty"`
	Image      []string               `json:"image,omitempty"`
	ExtraBody  map[string]interface{} `json:"extra_body,omitempty"`
}

// agnesImageCreateResponse Agnes Image 创建响应体
type agnesImageCreateResponse struct {
	Created int64 `json:"created"`
	Data    []struct {
		URL          string `json:"url"`
		B64JSON      string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
}

// Create 创建图片生成任务
// Agnes Image 是同步 API，创建后直接返回结果。
func (p *AgnesImageProvider) Create(ctx context.Context, req *VisualCreateRequest) (*VisualCreateResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Agnes Image API Key 未配置")
	}

	size := "1024x1024"
	if req.Params != nil {
		if v, ok := req.Params["size"].(string); ok && v != "" {
			size = v
		}
	}

	extraBody := make(map[string]interface{})
	responseFormat := "url"
	if req.Params != nil {
		if v, ok := req.Params["response_format"].(string); ok && v != "" {
			responseFormat = v
		}
	}
	extraBody["response_format"] = responseFormat

	// 图生图：将参考图（用户上传 + 历史结果）放入 extra_body.image
	var inputImages []string
	if req.ImageURL != "" {
		inputImages = append(inputImages, req.ImageURL)
	}
	for _, u := range req.ImageURLs {
		if u != "" {
			inputImages = append(inputImages, u)
		}
	}
	if len(inputImages) > 0 {
		extraBody["image"] = inputImages
	}

	body := agnesImageCreateRequest{
		Model:     req.Model,
		Prompt:    req.Prompt,
		Size:      size,
		ExtraBody: extraBody,
	}
	if req.Params != nil {
		if v, ok := req.Params["ratio"].(string); ok && v != "" {
			body.Ratio = v
		}
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Agnes Image 请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Agnes Image 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Agnes Image 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, UpstreamRateLimitedError
		}
		// 503 且响应体提示队列已满时，让业务层进入重试，前端保持"排队中"。
		if resp.StatusCode == http.StatusServiceUnavailable {
			bodyStr := strings.ToLower(string(respBody))
			if strings.Contains(bodyStr, "queue is full") || strings.Contains(bodyStr, "retry later") {
				return nil, UpstreamQueueFullError
			}
		}
		return nil, fmt.Errorf("Agnes Image 返回错误: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	var agnesResp agnesImageCreateResponse
	if err := json.Unmarshal(respBody, &agnesResp); err != nil {
		return nil, fmt.Errorf("解析 Agnes Image 响应失败: %w", err)
	}

	if len(agnesResp.Data) == 0 {
		return nil, fmt.Errorf("Agnes Image 返回空结果")
	}

	result := &VisualResult{
		URL:     agnesResp.Data[0].URL,
		B64JSON: agnesResp.Data[0].B64JSON,
	}
	if result.URL == "" && result.B64JSON != "" {
		// Base64 输出时也把数据带回去，前端可本地展示
		result.URL = "data:image/png;base64," + result.B64JSON
	}

	return &VisualCreateResult{
		UpstreamTaskID: "",
		Status:         model.VisualTaskStatusSucceeded,
		Result:         result,
		Usage:          &VisualUsage{TotalTokens: 0},
	}, nil
}

// Query Agnes Image 为同步 API，无异步查询需求，直接返回成功
func (p *AgnesImageProvider) Query(ctx context.Context, upstreamTaskID string) (*VisualQueryResult, error) {
	return &VisualQueryResult{
		Status: model.VisualTaskStatusSucceeded,
	}, nil
}

// Cancel Agnes Image 为同步 API，无需取消
func (p *AgnesImageProvider) Cancel(ctx context.Context, upstreamTaskID string) error {
	return nil
}
