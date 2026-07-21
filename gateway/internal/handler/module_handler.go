package handler

import (
	"bytes"
	"encoding/json"
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
	// P4：claw 转发云端秘技审核提交的 BaseURL（https://api.eleball.cn/v1）
	cloudAPIBase string
}

// NewModuleHandler 创建模块处理器
func NewModuleHandler(moduleService *service.ModuleService, logger *zap.Logger) *ModuleHandler {
	return &ModuleHandler{moduleService: moduleService, logger: logger}
}

// SetCloudAPIBase 注入云端 API Base（claw 用：SubmitForReview 转发云端秘技审核提交）。
func (h *ModuleHandler) SetCloudAPIBase(base string) {
	h.cloudAPIBase = base
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

// InstallModule P4：把云端拉取的 ModuleInstallMeta 安装到本地。
//
// claw 控制台/技能页「安装到本地」按钮调用：
//   - official=true：直接激活本地预置模块。
//   - 第三方：拉镜像 + cosign 签名校验 + 启动容器 + 注册激活。
//
// 请求体为 ModuleInstallMeta（见 specs/api-schema.yml）。
// 路由：POST /v1/claw-console/modules/install（claw_router 注册）。
func (h *ModuleHandler) InstallModule(c *gin.Context) {
	var meta service.ModuleInstallMeta
	if err := c.ShouldBindJSON(&meta); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if meta.ModuleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "module_id 不能为空"})
		return
	}

	record, err := h.moduleService.InstallFromCloudMeta(meta)
	if err != nil {
		h.logger.Warn("模块安装失败", zap.String("module_id", meta.ModuleID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": record})
}

// SubmitForReview P4：本地秘技提交云端审核。
//
// 把本地模块/驱动信息转发到云端 POST /v1/market/modules/register（需 auth_token，
// 从请求头 X-Module-Auth-Token 或 body 取）。云端审核通过后上架为云端秘技。
// 路由：POST /v1/claw-console/modules/submit-review（claw_router 注册）。
func (h *ModuleHandler) SubmitForReview(c *gin.Context) {
	if h.cloudAPIBase == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 3001, "message": "未配置云端 API Base，无法提交审核"})
		return
	}

	var req model.ModuleRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	providedToken := c.GetHeader("X-Module-Auth-Token")
	if providedToken == "" {
		providedToken = req.AuthToken
	}

	// 转发到云端 register 接口
	body, _ := json.Marshal(req)
	cloudReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost,
		h.cloudAPIBase+"/market/modules/register", bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "构造云端请求失败: " + err.Error()})
		return
	}
	cloudReq.Header.Set("Content-Type", "application/json")
	if providedToken != "" {
		cloudReq.Header.Set("X-Module-Auth-Token", providedToken)
	}

	resp, err := http.DefaultClient.Do(cloudReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"code": 3001, "message": "转发云端失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	var cloudResp map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&cloudResp)
	c.JSON(resp.StatusCode, cloudResp)
}
