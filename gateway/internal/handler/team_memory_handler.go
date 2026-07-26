package handler

import (
	"net/http"
	"strconv"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// TeamMemoryHandler 组共享记忆处理器（Agent Team P2，组严格按 user_id 隔离）
type TeamMemoryHandler struct {
	memoryService *service.TeamMemoryService
}

// NewTeamMemoryHandler 创建处理器
func NewTeamMemoryHandler(memoryService *service.TeamMemoryService) *TeamMemoryHandler {
	return &TeamMemoryHandler{memoryService: memoryService}
}

// ListMemories 组记忆列表（分页，按 created_at 倒序）
func (h *TeamMemoryHandler) ListMemories(c *gin.Context) {
	userID, _ := c.Get("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	items, total, err := h.memoryService.ListMemories(userID.(string), c.Param("id"), page, pageSize)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{
		"items": items,
		"total": total,
	}})
}

// CreateMemoryRequest 手动新增组记忆请求
type CreateMemoryRequest struct {
	Content string `json:"content" binding:"required"`
	Tags    string `json:"tags"`
}

// CreateMemory 手动新增组记忆
func (h *TeamMemoryHandler) CreateMemory(c *gin.Context) {
	var req CreateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: content 必填"})
		return
	}
	userID, _ := c.Get("user_id")
	m, err := h.memoryService.AddMemory(userID.(string), c.Param("id"), req.Content, req.Tags, "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": m})
}

// DeleteMemory 删除组记忆条目
func (h *TeamMemoryHandler) DeleteMemory(c *gin.Context) {
	userID, _ := c.Get("user_id")
	if err := h.memoryService.DeleteMemory(userID.(string), c.Param("id"), c.Param("memoryId")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}
