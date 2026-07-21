package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// CDKHandler 兑换码处理器
type CDKHandler struct {
	cdkService *service.CDKService
}

// NewCDKHandler 创建兑换码处理器
func NewCDKHandler(cdkService *service.CDKService) *CDKHandler {
	return &CDKHandler{cdkService: cdkService}
}

// BatchGenerate 批量生成兑换码（管理员）
func (h *CDKHandler) BatchGenerate(c *gin.Context) {
	var req service.BatchGenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	resp, err := h.cdkService.BatchGenerate(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "生成成功", "data": resp})
}

// ListCDKs 查询兑换码列表（管理员）
func (h *CDKHandler) ListCDKs(c *gin.Context) {
	var req service.CDKListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	resp, err := h.cdkService.ListCDKs(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": resp})
}

// DeleteCDK 删除兑换码（管理员）
func (h *CDKHandler) DeleteCDK(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "缺少兑换码 ID"})
		return
	}

	if err := h.cdkService.DeleteCDK(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "删除成功"})
}

// Redeem 用户兑换兑换码
func (h *CDKHandler) Redeem(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 1002, "message": "未登录"})
		return
	}

	var req service.RedeemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	resp, err := h.cdkService.Redeem(userID.(string), req.Code)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1003, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "兑换成功", "data": resp})
}

