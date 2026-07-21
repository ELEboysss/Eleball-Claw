package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// RechargePackageHandler 充值套餐处理器
type RechargePackageHandler struct {
	service *service.RechargePackageService
}

// NewRechargePackageHandler 创建处理器
func NewRechargePackageHandler(service *service.RechargePackageService) *RechargePackageHandler {
	return &RechargePackageHandler{service: service}
}

// ListUserPackages 用户端获取上架套餐列表
// GET /v1/recharge/packages
func (h *RechargePackageHandler) ListUserPackages(c *gin.Context) {
	items, err := h.service.ListForUser()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"items": items}})
}

// ListAdminPackages 管理端获取全部套餐
// GET /v1/admin/recharge/packages
func (h *RechargePackageHandler) ListAdminPackages(c *gin.Context) {
	items, err := h.service.ListAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"items": items}})
}

// CreatePackage 创建套餐
// POST /v1/admin/recharge/packages
func (h *RechargePackageHandler) CreatePackage(c *gin.Context) {
	var req service.CreatePackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	item, err := h.service.Create(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "创建成功", "data": item})
}

// UpdatePackage 更新套餐
// PATCH /v1/admin/recharge/packages/:id
func (h *RechargePackageHandler) UpdatePackage(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "缺少套餐 ID"})
		return
	}

	var req service.UpdatePackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	item, err := h.service.Update(id, &req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "更新成功", "data": item})
}

// DeletePackage 删除套餐
// DELETE /v1/admin/recharge/packages/:id
func (h *RechargePackageHandler) DeletePackage(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "缺少套餐 ID"})
		return
	}

	if err := h.service.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "删除成功"})
}
