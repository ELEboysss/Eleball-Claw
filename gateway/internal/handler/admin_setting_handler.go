package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// AdminSettingHandler 系统设置处理器
type AdminSettingHandler struct {
	settingService *service.SettingService
}

// NewAdminSettingHandler 创建处理器
func NewAdminSettingHandler(settingService *service.SettingService) *AdminSettingHandler {
	return &AdminSettingHandler{settingService: settingService}
}

// GetSettings 获取系统设置
func (h *AdminSettingHandler) GetSettings(c *gin.Context) {
	settings, err := h.settingService.GetSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": settings})
}

// UpdateSettings 更新系统设置
func (h *AdminSettingHandler) UpdateSettings(c *gin.Context) {
	var req service.Settings
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	if err := h.settingService.UpdateSettings(&req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "保存成功"})
}
