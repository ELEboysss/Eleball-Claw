package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/pkg/llm"
)

const (
	// SeedreamDefaultBaseURL 火山方舟 API 默认 BaseURL（即梦/Seedream 图片模型以 doubao-seedream-* 提供）
	SeedreamDefaultBaseURL = "https://ark.cn-beijing.volces.com/api/v3"
	// seedreamPath 图片生成 API 路径
	seedreamPath = "/images/generations"
)

// SeedreamProvider 火山方舟 Seedream（即梦）图片生成 Provider。
// 同步 API：POST /api/v3/images/generations 直接返回结果，无异步查询。
// 官方文档：https://www.volcengine.com/docs/82379/1541523
type SeedreamProvider struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewSeedreamProvider 创建 Seedream Provider
// baseURL 为空时使用火山方舟默认地址；apiKey 为火山方舟 API Key。
func NewSeedreamProvider(baseURL, apiKey string) *SeedreamProvider {
	if baseURL == "" {
		baseURL = SeedreamDefaultBaseURL
	}
	return &SeedreamProvider{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
}

// ProviderName 返回 Provider 名称
func (p *SeedreamProvider) ProviderName() string {
	return string(model.EleAgentUpstreamSeedream)
}

// MediaType 返回媒体类型
func (p *SeedreamProvider) MediaType() model.VisualMediaType {
	return model.VisualMediaTypeImage
}

// seedreamCreateRequest 火山方舟图片生成请求体
type seedreamCreateRequest struct {
	Model          string      `json:"model"`
	Prompt         string      `json:"prompt"`
	Image          interface{} `json:"image,omitempty"`           // 图生图：单 URL 字符串或多 URL 数组
	Size           string      `json:"size,omitempty"`            // "1K"/"2K"/"4K" 或 "宽x高"（如 2048x2048）
	ResponseFormat string      `json:"response_format,omitempty"` // "url" / "b64_json"
	Watermark      *bool       `json:"watermark,omitempty"`       // false 不加水印
}

// seedreamCreateResponse 火山方舟图片生成响应体
type seedreamCreateResponse struct {
	Created int64 `json:"created"`
	Data    []struct {
		URL      string `json:"url"`
		B64JSON  string `json:"b64_json"`
		Size     string `json:"size"`
	} `json:"data"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// seedreamErrorBody 火山方舟错误响应体
type seedreamErrorBody struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// seedreamRatioToSize 将常见宽高比映射为 Seedream 像素尺寸（均在 4.0+ 允许的 [1280x720, 4096x4096] 范围内）。
// 仅在未显式指定 size 时按 ratio 换算。
var seedreamRatioToSize = map[string]string{
	"1:1":  "2048x2048",
	"4:3":  "2304x1728",
	"3:4":  "1728x2304",
	"3:2":  "2496x1664",
	"2:3":  "1664x2496",
	"16:9": "2560x1440",
	"9:16": "1440x2560",
	"21:9": "3024x1296",
}

// Create 创建图片生成任务（同步返回结果）
func (p *SeedreamProvider) Create(ctx context.Context, req *VisualCreateRequest) (*VisualCreateResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Seedream API Key 未配置（火山方舟 API Key）")
	}

	// size 解析：显式 size > ratio 换算 > 默认 2K
	size := ""
	responseFormat := "url"
	watermark := false
	if req.Params != nil {
		if v, ok := req.Params["size"].(string); ok {
			size = v
		}
		if v, ok := req.Params["response_format"].(string); ok && v != "" {
			responseFormat = v
		}
		switch v := req.Params["watermark"].(type) {
		case bool:
			watermark = v
		case string:
			watermark = v == "true"
		}
	}
	if size == "" {
		ratio := ""
		if req.Params != nil {
			ratio, _ = req.Params["ratio"].(string)
		}
		if mapped, ok := seedreamRatioToSize[ratio]; ok {
			size = mapped
		} else {
			size = "2K"
		}
	}

	body := seedreamCreateRequest{
		Model:          req.Model,
		Prompt:         req.Prompt,
		Size:           size,
		ResponseFormat: responseFormat,
		Watermark:      &watermark,
	}

	// 图生图/多图编辑：单图传字符串，多图传数组
	var inputImages []string
	if req.ImageURL != "" {
		inputImages = append(inputImages, req.ImageURL)
	}
	for _, u := range req.ImageURLs {
		if u != "" {
			inputImages = append(inputImages, u)
		}
	}
	switch len(inputImages) {
	case 0:
	case 1:
		body.Image = inputImages[0]
	default:
		body.Image = inputImages
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Seedream 请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+seedreamPath, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Seedream 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Seedream 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, UpstreamRateLimitedError
		}
		// 返回类型化上游错误，使 5xx 可进入业务层自动重试与友好文案
		var errBody seedreamErrorBody
		if jsonErr := json.Unmarshal(respBody, &errBody); jsonErr == nil && errBody.Error != nil && errBody.Error.Message != "" {
			return nil, &llm.UpstreamError{StatusCode: resp.StatusCode, Body: errBody.Error.Code + ": " + errBody.Error.Message}
		}
		return nil, &llm.UpstreamError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var seedreamResp seedreamCreateResponse
	if err := json.Unmarshal(respBody, &seedreamResp); err != nil {
		return nil, fmt.Errorf("解析 Seedream 响应失败: %w", err)
	}
	if len(seedreamResp.Data) == 0 {
		return nil, fmt.Errorf("Seedream 返回空结果")
	}

	result := &VisualResult{
		URL:     seedreamResp.Data[0].URL,
		B64JSON: seedreamResp.Data[0].B64JSON,
		Size:    seedreamResp.Data[0].Size,
	}
	// 多图结果（组图/多输出）全部带出
	for _, d := range seedreamResp.Data {
		if d.URL != "" {
			result.URLs = append(result.URLs, d.URL)
		}
	}
	if result.URL == "" && result.B64JSON != "" {
		// 按魔数识别真实格式（方舟 b64 输出可能是 jpeg），避免 data URL 类型与实际不符
		mime := "image/png"
		if raw, decErr := base64.StdEncoding.DecodeString(result.B64JSON); decErr == nil && len(raw) > 2 && raw[0] == 0xFF && raw[1] == 0xD8 {
			mime = "image/jpeg"
		}
		result.URL = "data:" + mime + ";base64," + result.B64JSON
	}

	usage := &VisualUsage{}
	if seedreamResp.Usage != nil {
		usage.PromptTokens = seedreamResp.Usage.PromptTokens
		usage.CompletionTokens = seedreamResp.Usage.CompletionTokens
		usage.TotalTokens = seedreamResp.Usage.TotalTokens
	}

	return &VisualCreateResult{
		UpstreamTaskID: "",
		Status:         model.VisualTaskStatusSucceeded,
		Result:         result,
		Usage:          usage,
	}, nil
}

// Query Seedream 为同步 API，无异步查询需求，直接返回成功
func (p *SeedreamProvider) Query(ctx context.Context, upstreamTaskID string) (*VisualQueryResult, error) {
	return &VisualQueryResult{Status: model.VisualTaskStatusSucceeded}, nil
}

// Cancel Seedream 为同步 API，无需取消
func (p *SeedreamProvider) Cancel(ctx context.Context, upstreamTaskID string) error {
	return nil
}
