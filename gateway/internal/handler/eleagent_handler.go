package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// EleAgentHandler Ele Agent 处理器
type EleAgentHandler struct {
	eleAgentService      *service.EleAgentService
	eleAgentModelService *service.EleAgentModelService
}

// NewEleAgentHandler 创建处理器
func NewEleAgentHandler(eleAgentService *service.EleAgentService, eleAgentModelService *service.EleAgentModelService) *EleAgentHandler {
	return &EleAgentHandler{
		eleAgentService:      eleAgentService,
		eleAgentModelService: eleAgentModelService,
	}
}

// GetCredentials 获取 Ele Agent 调用凭证
func (h *EleAgentHandler) GetCredentials(c *gin.Context) {
	userID, _ := c.Get("user_id")
	subProvider := c.Query("subProvider")
	subModel := c.Query("subModel")

	creds, err := h.eleAgentService.GetCredentials(userID.(string), subProvider, subModel)
	if err != nil {
		// 余额不足返回 402 Payment Required
		if _, ok := err.(*service.BalanceInsufficientError); ok {
			c.JSON(http.StatusPaymentRequired, gin.H{"code": 4002, "message": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    creds,
	})
}

// ListModels 列出 Ele Agent 可用的平台-模型选项
func (h *EleAgentHandler) ListModels(c *gin.Context) {
	options := h.eleAgentModelService.ListOptions()
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    options,
	})
}
