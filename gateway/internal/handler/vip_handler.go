package handler

import (
	"net/http"
	"strconv"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// VIPHandler VIP 会员处理器
type VIPHandler struct {
	vipService *service.VIPService
}

// NewVIPHandler 创建 VIP 处理器
func NewVIPHandler(vipService *service.VIPService) *VIPHandler {
	return &VIPHandler{vipService: vipService}
}

// ListPlans 用户端获取上架 VIP 套餐
// GET /v1/vip/plans
func (h *VIPHandler) ListPlans(c *gin.Context) {
	items, err := h.vipService.ListPlansForUser()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"items": items}})
}

// GetStatus 获取当前用户 VIP 状态
// GET /v1/vip/status
func (h *VIPHandler) GetStatus(c *gin.Context) {
	userID, _ := c.Get("user_id")
	status, err := h.vipService.GetVIPStatus(userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": status})
}

// SubscribeRequest 订阅请求
type SubscribeRequest struct {
	PlanID           string `json:"plan_id" binding:"required"`
	Channel          string `json:"channel" binding:"oneof=wechat alipay"`
	UseElegantBalance bool  `json:"use_elegant_balance"`
}

// Subscribe 创建 VIP 订阅订单（或直接开通）
// POST /v1/vip/subscribe
func (h *VIPHandler) Subscribe(c *gin.Context) {
	var req SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	result, err := h.vipService.Subscribe(userID.(string), req.PlanID, req.Channel, req.UseElegantBalance)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": result})
}

// ====== 管理后台 ======

// ListPlansAdmin 管理端获取全部 VIP 套餐
// GET /v1/admin/vip/plans
func (h *VIPHandler) ListPlansAdmin(c *gin.Context) {
	items, err := h.vipService.ListAllPlans()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"items": items}})
}

// CreatePlan 创建 VIP 套餐
// POST /v1/admin/vip/plans
func (h *VIPHandler) CreatePlan(c *gin.Context) {
	var req service.CreateVIPPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	plan, err := h.vipService.CreatePlan(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "创建成功", "data": plan})
}

// UpdatePlan 更新 VIP 套餐
// PATCH /v1/admin/vip/plans/:id
func (h *VIPHandler) UpdatePlan(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "缺少套餐 ID"})
		return
	}
	var req service.UpdateVIPPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	plan, err := h.vipService.UpdatePlan(id, &req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "更新成功", "data": plan})
}

// DeletePlan 删除 VIP 套餐
// DELETE /v1/admin/vip/plans/:id
func (h *VIPHandler) DeletePlan(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "缺少套餐 ID"})
		return
	}
	if err := h.vipService.DeletePlan(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "删除成功"})
}

// ListSubscriptions 查询订阅记录
// GET /v1/admin/vip/subscriptions
func (h *VIPHandler) ListSubscriptions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	userID := c.Query("user_id")

	items, total, err := h.vipService.ListSubscriptions(page, pageSize, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"items": items, "total": total}})
}

// GrantVIPRequest 管理员开通 VIP 请求
type GrantVIPRequest struct {
	PlanID string `json:"plan_id" binding:"required"`
	Months int    `json:"months" binding:"required,min=1"`
}

// GrantVIP 管理员手动开通/续期 VIP
// POST /v1/admin/users/:id/vip
func (h *VIPHandler) GrantVIP(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "缺少用户 ID"})
		return
	}
	var req GrantVIPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if err := h.vipService.GrantSubscriptionByAdmin(userID, req.PlanID, req.Months); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "开通成功"})
}
