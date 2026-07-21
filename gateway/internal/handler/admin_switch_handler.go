package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/middleware"
	"github.com/gin-gonic/gin"
)

// AdminSwitchHandler 管理后台运行时开关控制器。
type AdminSwitchHandler struct{}

// NewAdminSwitchHandler 创建开关控制器。
func NewAdminSwitchHandler() *AdminSwitchHandler {
	return &AdminSwitchHandler{}
}

// Toggle 处理 /_internal/admin/:action 请求。
// action 支持 on（开启）、off（关闭）、status（查询状态）。
func (h *AdminSwitchHandler) Toggle(c *gin.Context) {
	action := c.Param("action")
	switch action {
	case "on":
		middleware.SetAdminEnabled(true)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "admin enabled", "data": gin.H{"enabled": true}})
	case "off":
		middleware.SetAdminEnabled(false)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "admin disabled", "data": gin.H{"enabled": false}})
	case "status":
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"enabled": middleware.IsAdminEnabled()}})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "invalid action, expected on/off/status"})
	}
}
