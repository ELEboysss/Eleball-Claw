package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// ConversationHandler 对话历史处理器
type ConversationHandler struct {
	conversationService *service.ConversationService
	agentService        *service.AgentService
}

// NewConversationHandler 创建处理器
func NewConversationHandler(conversationService *service.ConversationService, agentService *service.AgentService) *ConversationHandler {
	return &ConversationHandler{
		conversationService: conversationService,
		agentService:        agentService,
	}
}

// CreateConversation 创建新对话
func (h *ConversationHandler) CreateConversation(c *gin.Context) {
	var req service.CreateConversationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	userID, _ := c.Get("user_id")

	conv, err := h.conversationService.CreateConversation(c.Request.Context(), userID.(string), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": conv})
}

// ListConversations 查询对话列表（支持 team_id 按组过滤）
func (h *ConversationHandler) ListConversations(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	teamID := c.Query("team_id")

	userID, _ := c.Get("user_id")
	items, total, err := h.conversationService.List(c.Request.Context(), userID.(string), teamID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"total": total, "items": items},
	})
}

// GetConversation 查询对话详情
func (h *ConversationHandler) GetConversation(c *gin.Context) {
	id := c.Param("id")
	userID, _ := c.Get("user_id")
	conv, err := h.conversationService.GetDetail(c.Request.Context(), id, userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": conv})
}

// UpdateConversation 更新对话元数据
func (h *ConversationHandler) UpdateConversation(c *gin.Context) {
	id := c.Param("id")
	var req service.UpdateConversationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	if err := h.conversationService.Update(c.Request.Context(), id, userID.(string), req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// DeleteConversation 删除对话
func (h *ConversationHandler) DeleteConversation(c *gin.Context) {
	id := c.Param("id")
	userID, _ := c.Get("user_id")
	// 先清理该对话下的 Agent Session 资源，避免对话删除后残留过程文件
	if err := h.agentService.DeleteSessionsByConversation(c.Request.Context(), userID.(string), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	if err := h.conversationService.Delete(c.Request.Context(), id, userID.(string)); err != nil {
		if errors.Is(err, service.ErrConversationNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"code": 4004, "message": "对话不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// ListMessages 查询消息列表
func (h *ConversationHandler) ListMessages(c *gin.Context) {
	id := c.Param("id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))

	userID, _ := c.Get("user_id")
	items, total, err := h.conversationService.ListMessages(c.Request.Context(), id, userID.(string), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"total": total, "items": items},
	})
}

// CreateMessage 发送消息
func (h *ConversationHandler) CreateMessage(c *gin.Context) {
	id := c.Param("id")
	var msg model.ChatMessage
	if err := c.ShouldBindJSON(&msg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	updatedTitle, err := h.conversationService.SaveMessage(c.Request.Context(), id, userID.(string), &msg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"message": msg, "title": updatedTitle}})
}
