package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ModuleHandler 集市模块与动态驱动注册处理器
type ModuleHandler struct {
	moduleService *service.ModuleService
	logger        *zap.Logger
}

// NewModuleHandler 创建模块处理器
func NewModuleHandler(moduleService *service.ModuleService, logger *zap.Logger) *ModuleHandler {
	return &ModuleHandler{moduleService: moduleService, logger: logger}
}

// RescanMarketplace 运行时重新扫描 marketplace/ 目录并补齐内置模块与驱动别名
func (h *ModuleHandler) RescanMarketplace(c *gin.Context) {
	if err := h.moduleService.RescanMarketplace(h.logger); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// ListModules 列出所有已注册模块（管理后台）
// 响应结构与 specs/api-schema.yml、E2E 服务器保持一致：data 为 { total, items }
func (h *ModuleHandler) ListModules(c *gin.Context) {
	items, err := h.moduleService.ListModules()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"total": len(items), "items": items}})
}

// GetModule 获取单个模块详情（管理后台）
func (h *ModuleHandler) GetModule(c *gin.Context) {
	id := c.Param("id")
	item, err := h.moduleService.GetModule(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 4004, "message": "模块不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// RegisterModule 管理后台注册/更新模块
// 若请求未提供 module_id，后端会根据 name 自动生成并返回。
func (h *ModuleHandler) RegisterModule(c *gin.Context) {
	var req model.ModuleRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	record := &model.ModuleRecord{
		ID:            req.ModuleID,
		Name:          req.Name,
		Description:   req.Description,
		URL:           req.URL,
		TransportType: model.ModuleTransportType(req.TransportType),
		Version:       req.Version,
	}
	record.SetCapabilities(req.Capabilities)

	if err := h.moduleService.RegisterModule(record); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"module_id": record.ID}})
}

// UnregisterModule 注销模块
func (h *ModuleHandler) UnregisterModule(c *gin.Context) {
	id := c.Param("id")
	if err := h.moduleService.UnregisterModule(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// RefreshModule 强制探测模块健康状态
func (h *ModuleHandler) RefreshModule(c *gin.Context) {
	id := c.Param("id")
	status := h.moduleService.RefreshModule(id)
	if status == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 4004, "message": "模块未注册"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": status})
}

// RegisterModuleFromPlugin 插件自助注册
// 插件调用此接口上报自身信息，无需登录，但需要提供正确的 auth_token。
func (h *ModuleHandler) RegisterModuleFromPlugin(c *gin.Context) {
	var req model.ModuleRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	// 优先从请求头取令牌，其次从 body
	providedToken := c.GetHeader("X-Module-Auth-Token")
	if providedToken == "" {
		providedToken = req.AuthToken
	}

	moduleID, err := h.moduleService.RegisterModuleFromPlugin(&req, providedToken)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"module_id": moduleID}})
}

// ListDrivers 列出所有动态驱动映射（管理后台）
// 响应结构与 specs/api-schema.yml、E2E 服务器保持一致：data 为 { total, items }
func (h *ModuleHandler) ListDrivers(c *gin.Context) {
	items, err := h.moduleService.ListDrivers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"total": len(items), "items": items}})
}

// RegisterDriver 管理后台注册/更新驱动映射
func (h *ModuleHandler) RegisterDriver(c *gin.Context) {
	var req model.DriverRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	if err := h.moduleService.RegisterDriver(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// UnregisterDriver 注销驱动映射
func (h *ModuleHandler) UnregisterDriver(c *gin.Context) {
	id := c.Param("id")
	if err := h.moduleService.UnregisterDriver(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}
