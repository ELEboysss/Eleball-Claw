package handler

import (
	"fmt"
	"net/http"
	"strconv"

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

	// 凭证 BaseURL 指向网关自身（按请求实际 scheme://host），前端回调本网关 /chat/completions，
	// 由网关本地解析模型配置并代理上游，使本地配置的 Ele Agent 模型在非 agent 对话也能命中
	// （与 agent 模式一致），不再直接指向云端导致本地缺配的模型找不到。
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if xfp := c.GetHeader("X-Forwarded-Proto"); xfp != "" {
		scheme = xfp
	}
	gatewayBase := fmt.Sprintf("%s://%s/v1", scheme, c.Request.Host)

	creds, err := h.eleAgentService.GetCredentials(userID.(string), subProvider, subModel, gatewayBase)
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
// 不带分页参数时返回完整数组（对话页模型选择器依赖该行为）；
// 带 page/page_size 时返回 {items,total,page,page_size} 分页结构（claw-console 模型配置页使用）。
func (h *EleAgentHandler) ListModels(c *gin.Context) {
	options := h.eleAgentModelService.ListOptions()

	// 可选分页：仅在显式传 page_size 时启用，保持旧调用方拿到完整数组
	if pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", "0")); err == nil && pageSize > 0 {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		if page < 1 {
			page = 1
		}
		total := len(options)
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "success",
			"data": gin.H{
				"items":     options[start:end],
				"total":     total,
				"page":      page,
				"page_size": pageSize,
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    options,
	})
}
