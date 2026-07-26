package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// TeamHandler 对话分组处理器（Agent Team，组严格按 user_id 隔离）
type TeamHandler struct {
	teamService *service.TeamService
}

// NewTeamHandler 创建处理器
func NewTeamHandler(teamService *service.TeamService) *TeamHandler {
	return &TeamHandler{teamService: teamService}
}

// ListTeams 分组列表（含组内对话数）
func (h *TeamHandler) ListTeams(c *gin.Context) {
	userID, _ := c.Get("user_id")
	items, err := h.teamService.List(userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": items})
}

// CreateTeamRequest 创建分组请求
type CreateTeamRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// CreateTeam 创建分组
func (h *TeamHandler) CreateTeam(c *gin.Context) {
	var req CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: name 必填"})
		return
	}
	userID, _ := c.Get("user_id")
	team, err := h.teamService.Create(userID.(string), req.Name, req.Description)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": team})
}

// GetTeam 分组详情（含组内对话摘要列表）
func (h *TeamHandler) GetTeam(c *gin.Context) {
	userID, _ := c.Get("user_id")
	detail, err := h.teamService.Get(userID.(string), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": detail})
}

// UpdateTeamRequest 更新分组请求（指针字段，缺省不更新）
type UpdateTeamRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// UpdateTeam 更新分组名称/描述
func (h *TeamHandler) UpdateTeam(c *gin.Context) {
	var req UpdateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	team, err := h.teamService.Update(userID.(string), c.Param("id"), req.Name, req.Description)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": team})
}

// DeleteTeam 删除分组（组内对话 team_id 置空，不删对话）
func (h *TeamHandler) DeleteTeam(c *gin.Context) {
	userID, _ := c.Get("user_id")
	if err := h.teamService.Delete(userID.(string), c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}
