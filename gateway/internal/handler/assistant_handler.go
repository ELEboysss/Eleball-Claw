package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// AssistantHandler 助手处理器（助手 = 已激活秘技的命名组合，按会话应用）
type AssistantHandler struct {
	assistantService *service.AssistantService
}

// NewAssistantHandler 创建处理器
func NewAssistantHandler(assistantService *service.AssistantService) *AssistantHandler {
	return &AssistantHandler{assistantService: assistantService}
}

// ListAssistants 助手列表
func (h *AssistantHandler) ListAssistants(c *gin.Context) {
	userID, _ := c.Get("user_id")
	items, err := h.assistantService.List(userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": items})
}

// CreateAssistantRequest 创建助手请求
type CreateAssistantRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// CreateAssistant 创建助手
func (h *AssistantHandler) CreateAssistant(c *gin.Context) {
	var req CreateAssistantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: name 必填"})
		return
	}
	userID, _ := c.Get("user_id")
	view, err := h.assistantService.Create(userID.(string), req.Name, req.Description)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": view})
}

// GetAssistant 助手详情
func (h *AssistantHandler) GetAssistant(c *gin.Context) {
	userID, _ := c.Get("user_id")
	view, err := h.assistantService.Get(userID.(string), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": view})
}

// UpdateAssistantRequest 更新助手请求（指针字段，缺省不更新）
// Agent Team P3：扩展 system_prompt / shared / team_id（team_id 非空校验组归属）
type UpdateAssistantRequest struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	SystemPrompt *string `json:"system_prompt"`
	Shared       *bool   `json:"shared"`
	TeamID       *string `json:"team_id"`
}

// UpdateAssistant 更新助手名称/描述/编排协作字段
func (h *AssistantHandler) UpdateAssistant(c *gin.Context) {
	var req UpdateAssistantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	view, err := h.assistantService.Update(userID.(string), c.Param("id"), service.AssistantUpdateInput{
		Name:         req.Name,
		Description:  req.Description,
		SystemPrompt: req.SystemPrompt,
		Shared:       req.Shared,
		TeamID:       req.TeamID,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": view})
}

// DeleteAssistant 删除助手（同时清除引用它的会话绑定）
func (h *AssistantHandler) DeleteAssistant(c *gin.Context) {
	userID, _ := c.Get("user_id")
	if err := h.assistantService.Delete(userID.(string), c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// SetAssistantItemsRequest 设置助手条目请求
type SetAssistantItemsRequest struct {
	AgentIDs []string `json:"agent_ids"`
}

// SetAssistantItems 全量替换助手条目（仅允许已购买且已激活的秘技）
func (h *AssistantHandler) SetAssistantItems(c *gin.Context) {
	var req SetAssistantItemsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	view, err := h.assistantService.SetItems(userID.(string), c.Param("id"), req.AgentIDs)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": view})
}
