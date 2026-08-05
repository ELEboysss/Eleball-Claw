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
	// claw 云端秘技门控：第三方云端模块拉取需 VIP1+；nil（云端 cmd/server）时不校验
	cloudAccount *service.CloudAccountService
}

// NewModuleHandler 创建模块处理器
func NewModuleHandler(moduleService *service.ModuleService, logger *zap.Logger) *ModuleHandler {
	return &ModuleHandler{moduleService: moduleService, logger: logger}
}

// SetCloudAPIBase 注入云端 API Base（claw 用：SubmitForReview 转发云端秘技审核提交）。
func (h *ModuleHandler) SetCloudAPIBase(base string) {
	h.cloudAPIBase = base
}

// SetCloudAccountService 注入云端账户缓存（claw 用：云端秘技 VIP1+ 门控）。
func (h *ModuleHandler) SetCloudAccountService(svc *service.CloudAccountService) {
	h.cloudAccount = svc
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

// StartModule 启动指定模块（claw 控制台「启动服务」按钮）。
// POST /v1/claw-console/modules/:id/start：按部署方式拉起模块进程/容器并刷新状态。
// process 同步返回；docker 异步拉起（立即返回当前状态，前端稍后刷新）。
func (h *ModuleHandler) StartModule(c *gin.Context) {
	id := c.Param("id")
	status, err := h.moduleService.Start(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error()})
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

	// 凡云端来源模块（经 /market/modules/installed 拉取，无论 official）安装均需 VIP1+。
	// claw 本地扫描/内置秘技（如 SearchWeb）不经过此接口，天然豁免。
	if !requireCloudVIP1(c, h.cloudAccount) {
		return
	}

	record, err := h.moduleService.InstallFromCloudMeta(meta)
	if err != nil {
		h.logger.Warn("模块安装失败", zap.String("module_id", meta.ModuleID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	// 安装成功后补齐本地秘技数据：upsert AgentItem + 幂等写入当前用户的购买记录，
	// 否则技能页无本地数据且 ToggleAgentActive 会因「未购买」拒绝激活。
	userIDVal, _ := c.Get("user_id")
	userID, _ := userIDVal.(string)
	if err := h.moduleService.EnsureCloudAgentProvision(meta, userID); err != nil {
		h.logger.Warn("云端秘技本地落库失败", zap.String("module_id", meta.ModuleID), zap.Error(err))
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
