package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// PublicSettingHandler 面向用户的公开设置处理器
// 仅返回允许前端展示的安全配置项。
type PublicSettingHandler struct {
	settingService *service.SettingService
}

// NewPublicSettingHandler 创建公开设置处理器
func NewPublicSettingHandler(settingService *service.SettingService) *PublicSettingHandler {
	return &PublicSettingHandler{settingService: settingService}
}

// PublicSettings 对外暴露的公开设置子集
type PublicSettings struct {
	XianyuProductURL  string `json:"xianyu_product_url"`
	TaobaoProductURL  string `json:"taobao_product_url"`
	PromptFusionModel string `json:"prompt_fusion_model"`
}

// GetPublicSettings 获取公开设置
func (h *PublicSettingHandler) GetPublicSettings(c *gin.Context) {
	settings, err := h.settingService.GetSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": PublicSettings{
			XianyuProductURL:  settings.XianyuProductURL,
			TaobaoProductURL:  settings.TaobaoProductURL,
			PromptFusionModel: settings.PromptFusionModel,
		},
	})
}
