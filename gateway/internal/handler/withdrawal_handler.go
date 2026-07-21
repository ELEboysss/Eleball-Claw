package handler

import (
	"net/http"
	"strconv"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// WithdrawalHandler 提现处理器
type WithdrawalHandler struct {
	withdrawalService *service.WithdrawalService
}

// NewWithdrawalHandler 创建处理器
func NewWithdrawalHandler(withdrawalService *service.WithdrawalService) *WithdrawalHandler {
	return &WithdrawalHandler{withdrawalService: withdrawalService}
}

// ApplyWithdrawal 申请提现
func (h *WithdrawalHandler) ApplyWithdrawal(c *gin.Context) {
	var req service.ApplyWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	userName, _ := c.Get("user_name")
	name, _ := userName.(string)
	if name == "" {
		name = "用户"
	}

	record, err := h.withdrawalService.ApplyWithdrawal(userID.(string), name, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "申请已提交，等待审核", "data": record})
}

// ListMyWithdrawals 查询我的提现记录
func (h *WithdrawalHandler) ListMyWithdrawals(c *gin.Context) {
	userID, _ := c.Get("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	items, total, err := h.withdrawalService.ListMyWithdrawals(userID.(string), page, pageSize)
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

// ListAllWithdrawals 管理员查询所有提现记录
func (h *WithdrawalHandler) ListAllWithdrawals(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	status := c.Query("status")

	items, total, err := h.withdrawalService.ListAllWithdrawals(page, pageSize, status)
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

// ApproveWithdrawal 审核通过
func (h *WithdrawalHandler) ApproveWithdrawal(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		AdminNote string `json:"admin_note"`
	}
	c.ShouldBindJSON(&req)

	adminID, _ := c.Get("user_id")
	if err := h.withdrawalService.ApproveWithdrawal(adminID.(string), id, req.AdminNote); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "审核通过并已付款"})
}

// RejectWithdrawal 审核拒绝
func (h *WithdrawalHandler) RejectWithdrawal(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		AdminNote string `json:"admin_note"`
	}
	c.ShouldBindJSON(&req)

	adminID, _ := c.Get("user_id")
	if err := h.withdrawalService.RejectWithdrawal(adminID.(string), id, req.AdminNote); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "已拒绝，余额已退回"})
}
