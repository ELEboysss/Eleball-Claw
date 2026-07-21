package handler

import (
	"errors"
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// PaymentHandler 支付处理器
type PaymentHandler struct {
	paymentService *service.PaymentService
}

// NewPaymentHandler 创建处理器
func NewPaymentHandler(paymentService *service.PaymentService) *PaymentHandler {
	return &PaymentHandler{paymentService: paymentService}
}

// WechatPrepay 微信支付预订单（预留骨架，未接入真实渠道）
func (h *PaymentHandler) WechatPrepay(c *gin.Context) {
	var req service.WechatPrepayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	resp, err := h.paymentService.WechatPrepay(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": resp})
}

// AlipayOrder 支付宝订单（预留：Android APP 支付 orderString 场景，本期未启用）
func (h *PaymentHandler) AlipayOrder(c *gin.Context) {
	var req service.AlipayOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	resp, err := h.paymentService.AlipayOrder(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": resp})
}

// AlipayPrecreate 支付宝扫码预下单（收银台二维码）
// user_id 从 JWT context 注入，不信任客户端传入。
func (h *PaymentHandler) AlipayPrecreate(c *gin.Context) {
	var req service.AlipayPrecreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	req.UserID = c.GetString("user_id")
	if req.UserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 4001, "message": "未登录"})
		return
	}

	resp, err := h.paymentService.AlipayPrecreate(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrAlipayDisabled) {
			c.JSON(http.StatusOK, gin.H{"code": 5001, "message": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": resp})
}

// GetOrderStatus 查询订单支付状态（收银台轮询，纯展示不触发权益变更）
func (h *PaymentHandler) GetOrderStatus(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 4001, "message": "未登录"})
		return
	}

	result, err := h.paymentService.GetOrderStatus(userID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": result})
}

// WechatNotify 微信支付异步通知（预留骨架，未接入）
func (h *PaymentHandler) WechatNotify(c *gin.Context) {
	body, _ := c.GetRawData()
	if h.paymentService.VerifyWechatNotify(body) {
		c.String(http.StatusOK, "<xml><return_code><![CDATA[SUCCESS]]></return_code></xml>")
	} else {
		c.String(http.StatusBadRequest, "<xml><return_code><![CDATA[FAIL]]></return_code></xml>")
	}
}

// AlipayNotify 支付宝支付异步通知
// 验签 + 金额校验 + 幂等发放权益；处理成功应答 success，失败应答 fail 由支付宝重试。
func (h *PaymentHandler) AlipayNotify(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		c.String(http.StatusBadRequest, "fail")
		return
	}
	if err := h.paymentService.HandleAlipayNotify(c.Request.Context(), c.Request.PostForm); err != nil {
		// 记录错误便于排查，但不向支付宝暴露内部细节
		_ = c.Error(err)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	c.String(http.StatusOK, "success")
}
