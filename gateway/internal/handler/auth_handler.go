package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// AuthHandler 认证处理器
type AuthHandler struct {
	authService *service.AuthService
	vipService  *service.VIPService
}

// NewAuthHandler 创建处理器
func NewAuthHandler(authService *service.AuthService, vipService *service.VIPService) *AuthHandler {
	return &AuthHandler{authService: authService, vipService: vipService}
}

// Register 注册
func (h *AuthHandler) Register(c *gin.Context) {
	var req service.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	tokens, err := h.authService.Register(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": tokens})
}

// Login 登录
func (h *AuthHandler) Login(c *gin.Context) {
	var req service.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	tokens, err := h.authService.Login(req)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": tokens})
}

// Me 获取当前登录用户信息
func (h *AuthHandler) Me(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	user, err := h.authService.UserProfile(userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": "获取用户信息失败"})
		return
	}

	vipStatus, _ := h.vipService.GetVIPStatus(userID.(string))
	isVIP := vipStatus != nil && vipStatus.IsVIP

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"user_id":       user.ID,
			"username":      user.Username,
			"nickname":      user.Nickname,
			"avatar_url":    user.AvatarURL,
			"role":          user.Role,
			"is_vip":        isVIP,
			"vip_level":     user.VIPLevel,
			"vip_expire_at": user.VIPExpireAt,
			"vip_plan_id":   user.VIPPlanID,
		},
	})
}

// Refresh 刷新 Token
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	tokens, err := h.authService.Refresh(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": tokens})
}

// SendEmailOTP 发送邮箱验证码
func (h *AuthHandler) SendEmailOTP(c *gin.Context) {
	var req service.EmailOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	if err := h.authService.SendEmailOTP(req.Email, c.ClientIP()); err != nil {
		// 限流/未开通/发送失败统一返回 429 或 400，不暴露邮箱是否存在
		c.JSON(http.StatusTooManyRequests, gin.H{"code": 2002, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// EmailLogin 邮箱验证码登录（登录或自动注册）
func (h *AuthHandler) EmailLogin(c *gin.Context) {
	var req service.EmailLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	tokens, err := h.authService.EmailLogin(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 2001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": tokens})
}
