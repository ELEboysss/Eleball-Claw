package handler

import (
	"net/http"
	"strconv"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// AdminHandler 管理后台处理器
type AdminHandler struct {
	adminService *service.AdminService
}

// NewAdminHandler 创建处理器
func NewAdminHandler(adminService *service.AdminService) *AdminHandler {
	return &AdminHandler{adminService: adminService}
}

// ====== Dashboard ======

// GetRecentActivities 获取最近动态
func (h *AdminHandler) GetRecentActivities(c *gin.Context) {
	limit := 20
	if n, _ := strconv.Atoi(c.Query("limit")); n > 0 && n <= 100 {
		limit = n
	}
	items, err := h.adminService.ListRecentActivities(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": items})
}

// GetStats 获取仪表盘统计
func (h *AdminHandler) GetStats(c *gin.Context) {
	stats, err := h.adminService.GetDashboardStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": stats})
}

// GetDailyActive 获取日活跃用户趋势
func (h *AdminHandler) GetDailyActive(c *gin.Context) {
	days, _ := strconv.Atoi(c.Query("days"))
	if days <= 0 {
		days = 7
	}
	data, err := h.adminService.GetDailyActive(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": data})
}

// GetTokenUsage 获取 Token 使用趋势
func (h *AdminHandler) GetTokenUsage(c *gin.Context) {
	days, _ := strconv.Atoi(c.Query("days"))
	if days <= 0 {
		days = 7
	}
	data, err := h.adminService.GetTokenUsageTrend(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": data})
}

// ====== 用户管理 ======

// ListUsers 获取用户列表
func (h *AdminHandler) ListUsers(c *gin.Context) {
	var req service.UserListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	resp, err := h.adminService.ListUsers(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": resp})
}

// GetUserDetail 获取用户详情
func (h *AdminHandler) GetUserDetail(c *gin.Context) {
	userID := c.Param("id")
	user, err := h.adminService.GetUserDetail(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1004, "message": "用户不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": user})
}

// UpdateUserStatus 更新用户状态
func (h *AdminHandler) UpdateUserStatus(c *gin.Context) {
	userID := c.Param("id")
	var req struct {
		Status int `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	if err := h.adminService.UpdateUserStatus(userID, req.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// DeleteUser 删除用户
func (h *AdminHandler) DeleteUser(c *gin.Context) {
	userID := c.Param("id")
	if err := h.adminService.DeleteUser(userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// ====== 计费管理 ======

// ListTransactions 获取交易记录
func (h *AdminHandler) ListTransactions(c *gin.Context) {
	var req service.TransactionListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	resp, err := h.adminService.ListTransactions(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": resp})
}

// Recharge 给用户充值
func (h *AdminHandler) Recharge(c *gin.Context) {
	var req service.RechargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	if err := h.adminService.Recharge(req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "充值成功"})
}

// ====== 订单管理 ======

// ListOrders 获取订单列表
func (h *AdminHandler) ListOrders(c *gin.Context) {
	var req service.OrderListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	resp, err := h.adminService.ListOrders(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": resp})
}

// ====== ASR 额度管理 ======

// GetUserAsrQuota 获取用户 ASR 额度
func (h *AdminHandler) GetUserAsrQuota(c *gin.Context) {
	userID := c.Param("id")
	quota, err := h.adminService.GetUserAsrQuota(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": quota})
}

// UpdateUserAsrQuota 设置用户 ASR 额度
func (h *AdminHandler) UpdateUserAsrQuota(c *gin.Context) {
	userID := c.Param("id")
	var req service.UpdateUserAsrQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	if err := h.adminService.UpdateUserAsrQuota(userID, req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "设置成功"})
}

// RefundOrder 订单退款
func (h *AdminHandler) RefundOrder(c *gin.Context) {
	orderID := c.Param("id")
	if err := h.adminService.RefundOrder(orderID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "退款成功"})
}

// ConfirmOrder 管理员确认订单已收款
// POST /v1/admin/orders/:id/confirm
func (h *AdminHandler) ConfirmOrder(c *gin.Context) {
	orderID := c.Param("id")
	if err := h.adminService.ConfirmOrder(orderID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "确认收款成功"})
}
