package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// AgentCredentialHandler SKU 凭证管理接口
type AgentCredentialHandler struct {
	credentialService *service.AgentCredentialService
}

// NewAgentCredentialHandler 创建凭证处理器
func NewAgentCredentialHandler(credentialService *service.AgentCredentialService) *AgentCredentialHandler {
	return &AgentCredentialHandler{credentialService: credentialService}
}

// List 查询某 SKU 的凭证 schema 与当前用户已填写的值
// GET /v1/agents/:id/credentials
func (h *AgentCredentialHandler) List(c *gin.Context) {
	userID, _ := c.Get("user_id")
	agentID := c.Param("id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "agent_id 不能为空"})
		return
	}

	data, err := h.credentialService.ListForUserAgent(userID.(string), agentID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": data})
}

// Save 保存用户为某 SKU 填入的凭证
// POST /v1/agents/:id/credentials
func (h *AgentCredentialHandler) Save(c *gin.Context) {
	userID, _ := c.Get("user_id")
	agentID := c.Param("id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "agent_id 不能为空"})
		return
	}

	var req struct {
		Values map[string]string `json:"values" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	if err := h.credentialService.SaveForUserAgent(userID.(string), agentID, req.Values); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "凭证已保存"})
}
