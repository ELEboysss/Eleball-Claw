package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// AdminEleAgentModelHandler 管理员 Ele Agent 模型配置处理器
type AdminEleAgentModelHandler struct {
	svc *service.EleAgentModelService
}

// NewAdminEleAgentModelHandler 创建处理器
func NewAdminEleAgentModelHandler(svc *service.EleAgentModelService) *AdminEleAgentModelHandler {
	return &AdminEleAgentModelHandler{svc: svc}
}

// CreateConfigRequest 创建配置请求
type CreateConfigRequest struct {
	Provider          string `json:"provider" binding:"required"`
	Protocol          string `json:"protocol"` // 上游协议：openai_compatible / anthropic_messages，默认 openai_compatible
	ModelName         string `json:"model_name" binding:"required"`
	DisplayName       string `json:"display_name"`
	BaseURL           string `json:"base_url" binding:"required"`
	APIKey            string `json:"api_key" binding:"required"`
	Priority          int    `json:"priority"`
	InputPricePerCall int64  `json:"input_price_per_call"`
	PricePerCall      int64  `json:"price_per_call"`
	PricePerGeneration int64 `json:"price_per_generation"` // 按次附加费（弹丸/次），与 token 费用相加，适用于对话/图片/视频
	VideoMinDuration   int   `json:"video_min_duration"`   // 视频最小时长（秒），0 表示不限制
	VideoMaxDuration   int   `json:"video_max_duration"`   // 视频最大时长（秒），0 表示不限制
	VideoDurationStep  int   `json:"video_duration_step"`  // 视频时长步长（秒）
	SupportsChat              bool `json:"supports_chat"`
	SupportsVision            bool `json:"supports_vision"`
	SupportsImage             bool `json:"supports_image"`
	SupportsVideo             bool `json:"supports_video"`
	SupportsImageInput        bool `json:"supports_image_input"`
	SupportsContinuousContext bool `json:"supports_continuous_context"`
	SupportsTools             bool `json:"supports_tools"`
}

// UpdateConfigRequest 更新配置请求
type UpdateConfigRequest struct {
	Provider                  string `json:"provider"`
	Protocol                  string `json:"protocol"` // 上游协议：openai_compatible / anthropic_messages
	ModelName                 string `json:"model_name"`
	DisplayName               string `json:"display_name"`
	BaseURL                   string `json:"base_url"`
	IsEnabled                 *bool  `json:"is_enabled"`
	SupportsChat              *bool  `json:"supports_chat"`
	SupportsVision            *bool  `json:"supports_vision"`
	SupportsImage             *bool  `json:"supports_image"`
	SupportsVideo             *bool  `json:"supports_video"`
	SupportsImageInput        *bool  `json:"supports_image_input"`
	SupportsContinuousContext *bool  `json:"supports_continuous_context"`
	SupportsTools             *bool  `json:"supports_tools"`
	Priority                  *int   `json:"priority"`
	InputPricePerCall         *int64 `json:"input_price_per_call"`
	PricePerCall              *int64 `json:"price_per_call"`
	PricePerGeneration        *int64 `json:"price_per_generation"`
	VideoMinDuration          *int   `json:"video_min_duration"`
	VideoMaxDuration          *int   `json:"video_max_duration"`
	VideoDurationStep         *int   `json:"video_duration_step"`
}

// RotateEleAgentModelKeyRequest 轮换 API Key 请求
type RotateEleAgentModelKeyRequest struct {
	APIKey string `json:"api_key" binding:"required"`
}

// ListConfigs 列出配置
func (h *AdminEleAgentModelHandler) ListConfigs(c *gin.Context) {
	provider := c.Query("provider")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	items, total, err := h.svc.ListConfigs(provider, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// CreateConfig 创建配置
func (h *AdminEleAgentModelHandler) CreateConfig(c *gin.Context) {
	var req CreateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.InputPricePerCall < 0 || req.PricePerCall < 0 || req.PricePerGeneration < 0 || req.Priority < 0 ||
		req.VideoMinDuration < 0 || req.VideoMaxDuration < 0 || req.VideoDurationStep < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "单价、时长与优先级不能为负数"})
		return
	}
	if req.VideoMaxDuration > 0 && req.VideoMinDuration > req.VideoMaxDuration {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "视频最小时长不能大于最大时长"})
		return
	}

	item, err := h.svc.CreateConfig(service.EleAgentModelConfigInput{
		Provider:                  req.Provider,
		Protocol:                  req.Protocol,
		ModelName:                 req.ModelName,
		DisplayName:               req.DisplayName,
		BaseURL:                   req.BaseURL,
		APIKey:                    req.APIKey,
		Priority:                  req.Priority,
		InputPricePerCall:         req.InputPricePerCall,
		PricePerCall:              req.PricePerCall,
		PricePerGeneration:        req.PricePerGeneration,
		VideoMinDuration:          req.VideoMinDuration,
		VideoMaxDuration:          req.VideoMaxDuration,
		VideoDurationStep:         req.VideoDurationStep,
		SupportsChat:              req.SupportsChat,
		SupportsVision:            req.SupportsVision,
		SupportsImage:             req.SupportsImage,
		SupportsVideo:             req.SupportsVideo,
		SupportsImageInput:        req.SupportsImageInput,
		SupportsContinuousContext: req.SupportsContinuousContext,
		SupportsTools:             req.SupportsTools,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// GetConfig 获取配置详情
func (h *AdminEleAgentModelHandler) GetConfig(c *gin.Context) {
	id := c.Param("id")
	item, err := h.svc.GetConfig(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1000, "message": "配置不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// UpdateConfig 更新配置
func (h *AdminEleAgentModelHandler) UpdateConfig(c *gin.Context) {
	id := c.Param("id")
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if (req.InputPricePerCall != nil && *req.InputPricePerCall < 0) ||
		(req.PricePerCall != nil && *req.PricePerCall < 0) ||
		(req.PricePerGeneration != nil && *req.PricePerGeneration < 0) ||
		(req.VideoMinDuration != nil && *req.VideoMinDuration < 0) ||
		(req.VideoMaxDuration != nil && *req.VideoMaxDuration < 0) ||
		(req.VideoDurationStep != nil && *req.VideoDurationStep < 0) ||
		(req.Priority != nil && *req.Priority < 0) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "单价、时长与优先级不能为负数"})
		return
	}

	item, err := h.svc.UpdateConfig(id, service.EleAgentModelConfigPatch{
		Provider:                  req.Provider,
		Protocol:                  req.Protocol,
		ModelName:                 req.ModelName,
		DisplayName:               req.DisplayName,
		BaseURL:                   req.BaseURL,
		IsEnabled:                 req.IsEnabled,
		SupportsChat:              req.SupportsChat,
		SupportsVision:            req.SupportsVision,
		SupportsImage:             req.SupportsImage,
		SupportsVideo:             req.SupportsVideo,
		SupportsImageInput:        req.SupportsImageInput,
		SupportsContinuousContext: req.SupportsContinuousContext,
		SupportsTools:             req.SupportsTools,
		Priority:                  req.Priority,
		InputPricePerCall:         req.InputPricePerCall,
		PricePerCall:              req.PricePerCall,
		PricePerGeneration:        req.PricePerGeneration,
		VideoMinDuration:          req.VideoMinDuration,
		VideoMaxDuration:          req.VideoMaxDuration,
		VideoDurationStep:         req.VideoDurationStep,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// RotateAPIKey 轮换 API Key
func (h *AdminEleAgentModelHandler) RotateAPIKey(c *gin.Context) {
	id := c.Param("id")
	var req RotateEleAgentModelKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	item, err := h.svc.RotateAPIKey(id, req.APIKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// DeleteConfig 删除配置
func (h *AdminEleAgentModelHandler) DeleteConfig(c *gin.Context) {
	id := c.Param("id")
	if err := h.svc.DeleteConfig(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// ExportConfigs 导出全部模型配置为 JSON 文件。
// include_keys=true 时解密导出明文 API Key（默认不导出），导出文件需妥善保管。
// 输出为缩进对齐的代码风格 JSON（非单行压缩），便于人工查看与编辑后再导入。
func (h *AdminEleAgentModelHandler) ExportConfigs(c *gin.Context) {
	includeKeys := c.Query("include_keys") == "true"

	data, err := h.svc.ExportConfigs(includeKeys)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	indented, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": "序列化导出数据失败: " + err.Error()})
		return
	}

	filename := "eleagent-models-" + time.Now().Format("20060102-150405") + ".json"
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(http.StatusOK, "application/json; charset=utf-8", indented)
}

// ImportConfigs 批量导入模型配置。
// 请求体支持两种形式：导出的完整 JSON（含 items 字段）或纯配置数组。
// 按 provider + model_name 匹配：存在则更新（未提供 api_key 时保留原 Key），不存在则创建（必须提供 api_key）。
func (h *AdminEleAgentModelHandler) ImportConfigs(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "读取请求体失败: " + err.Error()})
		return
	}

	// 先按导出文件结构解析，失败再按纯数组解析
	var items []service.EleAgentModelExportItem
	var wrapper service.EleAgentModelExportData
	if err := json.Unmarshal(body, &wrapper); err == nil && wrapper.Items != nil {
		items = wrapper.Items
	} else if err := json.Unmarshal(body, &items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "JSON 格式错误，应为导出文件（含 items 字段）或配置数组"})
		return
	}
	if len(items) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "导入内容为空"})
		return
	}
	if len(items) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "单次最多导入 500 条配置"})
		return
	}

	result, err := h.svc.ImportConfigs(items)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": result})
}
