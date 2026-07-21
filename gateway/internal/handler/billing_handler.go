package handler

import (
	"net/http"
	"strconv"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// BillingHandler 计费处理器
type BillingHandler struct {
	billingService *service.BillingService
}

// NewBillingHandler 创建处理器
func NewBillingHandler(billingService *service.BillingService) *BillingHandler {
	return &BillingHandler{billingService: billingService}
}

// GetBalance 查询当前双货币余额
func (h *BillingHandler) GetBalance(c *gin.Context) {
	userID, _ := c.Get("user_id")
	balance, err := h.billingService.GetBalance(userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"danwan":  balance.Danwan,
			"elegant": balance.Elegant,
			"unit":    "cent",
		},
	})
}

// GetRechargeHistory 查询当前用户充值记录
func (h *BillingHandler) GetRechargeHistory(c *gin.Context) {
	userID, _ := c.Get("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	history, err := h.billingService.GetRechargeHistory(userID.(string), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    history,
	})
}
