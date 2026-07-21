package handler

import (
	"net/http"
	"strconv"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/service"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/gin-gonic/gin"
)

// AdminKeyHandler 管理员 API Key 管理处理器
type AdminKeyHandler struct {
	keyManager *service.KeyManagerService
}

// NewAdminKeyHandler 创建处理器
func NewAdminKeyHandler(keyManager *service.KeyManagerService) *AdminKeyHandler {
	return &AdminKeyHandler{keyManager: keyManager}
}

// CreateKeyRequest 创建 Key 请求
type CreateKeyRequest struct {
	Provider   string `json:"provider" binding:"required"`
	Name       string `json:"name" binding:"required"`
	BaseURL    string `json:"base_url"`
	ApiKey     string `json:"api_key" binding:"required"`
	Priority   int    `json:"priority"`
	DailyQuota int64  `json:"daily_quota"`
}

// UpdateKeyRequest 更新 Key 请求
type UpdateKeyRequest struct {
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	IsEnabled  *bool  `json:"is_enabled"`
	Priority   *int   `json:"priority"`
	DailyQuota *int64 `json:"daily_quota"`
}

// RotateKeyRequest 轮换 Key 请求
type RotateKeyRequest struct {
	ApiKey string `json:"api_key" binding:"required"`
}

// ListProviders 列出支持的 Provider 及 Key 统计
func (h *AdminKeyHandler) ListProviders(c *gin.Context) {
	status, err := h.keyManager.ProviderStatus()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	providers := service.SupportedProviders()
	result := make([]gin.H, 0, len(providers))
	for _, p := range providers {
		item := gin.H{
			"provider":      p["provider"],
			"name":          p["name"],
			"default_base_url": p["default_base_url"],
			"total_keys":    0,
			"enabled_keys":  0,
			"available_keys": 0,
		}
		for _, s := range status {
			if s.Provider == p["provider"] {
				item["total_keys"] = s.TotalKeys
				item["enabled_keys"] = s.EnabledKeys
				item["available_keys"] = s.AvailableKeys
				break
			}
		}
		result = append(result, item)
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": result})
}

// ListKeys 列出 Key
func (h *AdminKeyHandler) ListKeys(c *gin.Context) {
	provider := c.Query("provider")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	items, total, err := h.keyManager.ListKeys(provider, page, pageSize)
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

// CreateKey 创建 Key
func (h *AdminKeyHandler) CreateKey(c *gin.Context) {
	var req CreateKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	item, err := h.keyManager.CreateKey(req.Provider, req.Name, req.BaseURL, req.ApiKey, req.Priority, req.DailyQuota)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// GetKey 获取 Key 详情
func (h *AdminKeyHandler) GetKey(c *gin.Context) {
	id := c.Param("id")
	item, err := h.keyManager.GetKey(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1000, "message": "Key 不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// UpdateKey 更新 Key
func (h *AdminKeyHandler) UpdateKey(c *gin.Context) {
	id := c.Param("id")
	var req UpdateKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	existing, err := h.keyManager.GetKey(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1000, "message": "Key 不存在"})
		return
	}

	key := &model.ProviderApiKey{
		ID:        id,
		Provider:  existing.Provider,
		Name:      existing.Name,
		BaseURL:   existing.BaseURL,
		IsEnabled: existing.IsEnabled,
		Priority:  existing.Priority,
		DailyQuota: existing.DailyQuota,
	}

	if req.Name != "" {
		key.Name = req.Name
	}
	if req.BaseURL != "" {
		key.BaseURL = req.BaseURL
	}
	if req.IsEnabled != nil {
		key.IsEnabled = *req.IsEnabled
	}
	if req.Priority != nil {
		key.Priority = *req.Priority
	}
	if req.DailyQuota != nil {
		key.DailyQuota = *req.DailyQuota
	}

	item, err := h.keyManager.UpdateKey(key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// DeleteKey 删除 Key
func (h *AdminKeyHandler) DeleteKey(c *gin.Context) {
	id := c.Param("id")
	if err := h.keyManager.DeleteKey(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// TestKey 测试 Key 是否可用
func (h *AdminKeyHandler) TestKey(c *gin.Context) {
	id := c.Param("id")
	item, err := h.keyManager.GetKey(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1000, "message": "Key 不存在"})
		return
	}

	selected, err := h.keyManager.SelectKey(item.Provider)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": err.Error()})
		return
	}
	if selected.Key.ID != id {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": "该 Key 当前不可用（可能被禁用或超配额）"})
		return
	}

	factory := service.NewClientFactory(0)
	client, err := factory.Create(item.Provider, selected.Plaintext, item.BaseURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	// 发送极简请求验证 Key
	modelName := "gpt-4o-mini"
	if item.Provider == "deepseek" {
		modelName = "deepseek-chat"
	} else if item.Provider == "custom" {
		modelName = "default"
	}

	_, err = client.Chat(c.Request.Context(), llm.ChatRequest{
		Model:    modelName,
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
		Stream:   false,
	})
	if err != nil {
		_ = h.keyManager.ReportFailure(id, err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": "Key 测试失败: " + err.Error()})
		return
	}

	_ = h.keyManager.ReportSuccess(id, 0)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// ResetQuota 重置所有 Key 日配额
func (h *AdminKeyHandler) ResetQuota(c *gin.Context) {
	if err := h.keyManager.ResetDailyQuota(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}
