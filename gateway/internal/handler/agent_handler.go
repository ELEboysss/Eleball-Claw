package handler

import (
	"net/http"
	"strconv"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// AgentHandler Agent 市场处理器
type AgentHandler struct {
	agentService *service.AgentMarketService
	// claw 云端秘技门控：激活云端来源秘技需 VIP1+；nil（云端 cmd/server）时不校验
	cloudAccount *service.CloudAccountService
}

// NewAgentHandler 创建处理器
func NewAgentHandler(agentService *service.AgentMarketService) *AgentHandler {
	return &AgentHandler{agentService: agentService}
}

// SetCloudAccountService 注入云端账户缓存（claw 用：云端秘技激活 VIP1+ 门控）。
func (h *AgentHandler) SetCloudAccountService(svc *service.CloudAccountService) {
	h.cloudAccount = svc
}

// ListAgents 秘技列表
func (h *AgentHandler) ListAgents(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	category := c.Query("category")
	sortBy := c.DefaultQuery("sort", "hot")
	filter := c.Query("filter")

	userIDVal, _ := c.Get("user_id")
	userID, _ := userIDVal.(string)
	items, total, err := h.agentService.ListAgents(userID, page, pageSize, category, sortBy, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"total": total,
			"items": items,
		},
	})
}

// GetAgent 秘技详情
func (h *AgentHandler) GetAgent(c *gin.Context) {
	id := c.Param("id")
	agent, err := h.agentService.GetAgent(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 4004, "message": "秘技不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": agent})
}

// CreateAgent 创建秘技
func (h *AgentHandler) CreateAgent(c *gin.Context) {
	var req service.CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	userName, _ := c.Get("user_name")
	name, _ := userName.(string)
	if name == "" {
		name = "开发者"
	}

	agent, err := h.agentService.CreateAgent(userID.(string), name, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": agent})
}

// PurchaseAgent 购买秘技
func (h *AgentHandler) PurchaseAgent(c *gin.Context) {
	var req service.PurchaseAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	userID, _ := c.Get("user_id")
	if err := h.agentService.PurchaseAgent(userID.(string), req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "购买成功"})
}

// ToggleAgentActive 切换当前用户对某秘技的激活状态
func (h *AgentHandler) ToggleAgentActive(c *gin.Context) {
	userIDVal, _ := c.Get("user_id")
	userID, _ := userIDVal.(string)
	agentID := c.Param("id")
	// 云端来源秘技（cloud-purchased，含官方模块）激活需 VIP1+；claw 本地扫描/内置秘技免门控
	if h.agentService.IsCloudPurchasedAgent(agentID) {
		if !requireCloudVIP1(c, h.cloudAccount) {
			return
		}
	}
	active, err := h.agentService.ToggleAgentActive(userID, agentID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"active": active},
	})
}

// ListReviews 评价列表
func (h *AgentHandler) ListReviews(c *gin.Context) {
	agentID := c.Param("id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	reviews, total, err := h.agentService.ListReviews(agentID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"total": total,
			"items": reviews,
		},
	})
}

// CreateReview 创建评价
func (h *AgentHandler) CreateReview(c *gin.Context) {
	var req service.CreateReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	userID, _ := c.Get("user_id")
	userName, _ := c.Get("user_name")
	name, _ := userName.(string)
	if name == "" {
		name = "用户"
	}

	if err := h.agentService.CreateReview(userID.(string), name, req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "评价成功"})
}

// ToggleFavorite 切换收藏
func (h *AgentHandler) ToggleFavorite(c *gin.Context) {
	agentID := c.Param("id")
	userID, _ := c.Get("user_id")

	favorited, err := h.agentService.ToggleFavorite(agentID, userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}

	action := "取消收藏"
	if favorited {
		action = "收藏成功"
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": action, "data": gin.H{"favorited": favorited}})
}

// GetDeveloperAccount 开发者账户
func (h *AgentHandler) GetDeveloperAccount(c *gin.Context) {
	userID, _ := c.Get("user_id")
	acc, err := h.agentService.GetDeveloperAccount(userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": acc})
}

// GetUserSpace 弹丸空间主页
func (h *AgentHandler) GetUserSpace(c *gin.Context) {
	userID, _ := c.Get("user_id")
	space, err := h.agentService.GetUserSpace(userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": space})
}

// GetCategories 分类列表（从已上架秘技中动态聚合）
func (h *AgentHandler) GetCategories(c *gin.Context) {
	categories, err := h.agentService.GetCategories()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": categories})
}

// GetCapabilities 查询当前账户能力
func (h *AgentHandler) GetCapabilities(c *gin.Context) {
	userID, _ := c.Get("user_id")
	role, _ := c.Get("role")
	roleStr, _ := role.(string)
	if roleStr == "" {
		roleStr = model.UserRoleUser
	}

	caps, err := h.agentService.GetCapabilities(userID.(string), roleStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": caps})
}

// ====== 管理员审核接口 ======

// ListAgentsForAdmin 管理员查询秘技列表（支持状态筛选）
func (h *AgentHandler) ListAgentsForAdmin(c *gin.Context) {
	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	items, total, err := h.agentService.ListAgentsForAdmin(status, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"total": total,
			"items": items,
		},
	})
}

// ReviewAgent 审核秘技（通过/拒绝）
func (h *AgentHandler) ReviewAgent(c *gin.Context) {
	id := c.Param("id")
	var req service.ReviewAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	result, err := h.agentService.ReviewAgent(id, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4000, "message": err.Error()})
		return
	}

	action := "已通过"
	if req.Status == "rejected" {
		action = "已拒绝"
	}
	resp := gin.H{"code": 0, "message": "审核" + action}
	if result != nil && result.AuthToken != "" {
		resp["data"] = gin.H{
			"driver_id":  result.DriverID,
			"auth_token": result.AuthToken,
		}
	}
	c.JSON(http.StatusOK, resp)
}

// GetAgentDependencies 获取 SKU 依赖的驱动/模块状态（管理后台审批参考）
func (h *AgentHandler) GetAgentDependencies(c *gin.Context) {
	id := c.Param("id")
	status, err := h.agentService.GetAgentDependencyStatus(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": status})
}

// DelistAgent 下架秘技
func (h *AgentHandler) DelistAgent(c *gin.Context) {
	id := c.Param("id")
	if err := h.agentService.DelistAgent(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "下架成功"})
}
