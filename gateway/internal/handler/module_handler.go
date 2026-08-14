package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
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

// RescanMarketplace 运行时重新扫描 marketplace/ 目录并补齐模块与 SKU（T2.2 RescanPackage）
func (h *ModuleHandler) RescanMarketplace(c *gin.Context) {
	if err := h.moduleService.RescanPackage("claw", h.logger); err != nil {
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

// RegisterModule 管理后台注册/更新模块（统一落 skill_runtimes）
// 若请求未提供 module_id，后端会根据 name 自动生成并返回。
func (h *ModuleHandler) RegisterModule(c *gin.Context) {
	var req model.PluginRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	rt := &model.SkillRuntime{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		Source:      model.SkillRuntimeSourceMarketplace,
		Endpoint:    req.URL,
		Version:     req.Version,
		Status:      model.SkillRuntimeStatusInstalled,
	}
	switch req.TransportType {
	case "mcp":
		rt.Transport = model.SkillRuntimeTransportMCPHTTP
		rt.Deployment = model.SkillRuntimeDeploymentDocker
	case "remote_url":
		rt.Transport = model.SkillRuntimeTransportRawHTTP
		rt.Deployment = model.SkillRuntimeDeploymentNone
	default:
		rt.Transport = model.SkillRuntimeTransportExecute
		rt.Deployment = model.SkillRuntimeDeploymentDocker
	}
	rt.SetCapabilities(req.Capabilities)

	if err := h.moduleService.RegisterModule(rt); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 3001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"module_id": rt.ID}})
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

// UninstallModule 真·卸载非官方模块（claw-only）：停进程/容器 + 删本地文件 + 下架 SKU + 注销运行时。
// DELETE /v1/claw-console/modules/:id/uninstall（官方模块拒绝，更新走云端模块页 T4.4）。
func (h *ModuleHandler) UninstallModule(c *gin.Context) {
	id := c.Param("id")
	if err := h.moduleService.UninstallModule(id); err != nil {
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
	var req model.PluginRegisterRequest
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

// SubmitForReview T8：本地秘技分享到云端审核。
//
// UI 仅传 module_id；handler 查本地记录取权威元数据 + 调 PackageModule（T7）打 tarball，
// 以 multipart（metadata JSON + tarball 文件）转发到云端暂存端点
// POST /market/modules/submissions（T9 实现，免登录，admin 审批闸门）。
// 云端审核通过后由 T11 解压发布到 marketplace/。去掉旧 auth_token 鸡生蛋流程
// （旧流程转发 /market/modules/register 需先有审批后下发的 auth_token，逻辑死锁）。
// 路由：POST /v1/claw-console/modules/submit-review（claw_router 注册，复用）。
func (h *ModuleHandler) SubmitForReview(c *gin.Context) {
	if h.cloudAPIBase == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 3001, "message": "未配置云端 API Base，无法分享到云端"})
		return
	}

	var req model.ModuleShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	// 元数据（供云端审核列表展示，无需解压 tarball；T3.2 对齐 package 布局）。
	// 先取元数据校验模块存在（package.json 目录或运行时记录，缺失 404），再打包。
	meta, err := h.moduleService.ModuleSubmissionMetaFor(req.ModuleID)
	if err != nil || meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 4004, "message": "本地模块不存在: " + req.ModuleID})
		return
	}

	// 打包 tarball（T7：脚本模块递归 tar 磁盘目录 / MCP 安装模块 DB 物化 package.json；
	// 权威信息以 tarball 内 package.json + .origin 为准）
	pkg, err := h.moduleService.PackageModule(req.ModuleID)
	if err != nil {
		h.logger.Warn("模块打包失败", zap.String("module_id", req.ModuleID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "模块打包失败: " + err.Error()})
		return
	}
	metaJSON, _ := json.Marshal(meta)

	// 拼 multipart：metadata(JSON 字段) + tarball(文件)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("metadata", string(metaJSON)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "构造元数据失败: " + err.Error()})
		return
	}
	part, err := writer.CreateFormFile("tarball", pkg.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "构造上传表单失败: " + err.Error()})
		return
	}
	if _, err := part.Write(pkg.Data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "写入 tarball 失败: " + err.Error()})
		return
	}
	if err := writer.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "关闭表单失败: " + err.Error()})
		return
	}

	cloudReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost,
		h.cloudAPIBase+"/market/modules/submissions", &body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "构造云端请求失败: " + err.Error()})
		return
	}
	cloudReq.Header.Set("Content-Type", writer.FormDataContentType())

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

// ListCloudCatalog 拉取云端秘技包目录（转发云端 GET /v1/market/modules/catalog）。
// claw web 据此展示「云端模块」tab + 比对本地（package_id + version）决定下载/更新。
// 透传用户 Authorization（云端 catalog 为 auth 端点）。
func (h *ModuleHandler) ListCloudCatalog(c *gin.Context) {
	if h.cloudAPIBase == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 3001, "message": "未配置云端 API Base"})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
		h.cloudAPIBase+"/market/modules/catalog", nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "构造云端请求失败: " + err.Error()})
		return
	}
	req.Header.Set("Authorization", c.GetHeader("Authorization"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"code": 3001, "message": "拉取云端目录失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	var cloudResp struct {
		Code int `json:"code"`
		Data struct {
			Items []model.PackageCatalogItem `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cloudResp); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"code": 3001, "message": "解析云端响应失败: " + err.Error()})
		return
	}
	// T4.4：比对本地安装状态（installed/local_version/has_update/local_status），
	// 供 web 展示「下载/更新/已最新」。更新复用 DownloadCloudModule（ApplyPackage 覆盖同名文件）。
	enriched := h.moduleService.EnrichCloudCatalog(cloudResp.Data.Items)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"items": enriched}})
}

// DownloadCloudModule 从云端下载完整秘技包并落盘（云端 GET /v1/market/modules/:id/package
// -> moduleService.ApplyPackage）。手动触发（D4：不自动拉取，用户主动点「下载到本地」）。
// 透传用户 Authorization。返回落盘后的本地模块记录。
func (h *ModuleHandler) DownloadCloudModule(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "缺少模块 id"})
		return
	}
	if h.cloudAPIBase == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 3001, "message": "未配置云端 API Base"})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
		h.cloudAPIBase+"/market/modules/"+id+"/package", nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "构造云端请求失败: " + err.Error()})
		return
	}
	req.Header.Set("Authorization", c.GetHeader("Authorization"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"code": 3001, "message": "下载云端模块包失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		c.JSON(resp.StatusCode, errResp)
		return
	}
	var cloudResp struct {
		Code int                 `json:"code"`
		Data model.PackageBundle `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cloudResp); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"code": 3001, "message": "解析云端模块包失败: " + err.Error()})
		return
	}
	rec, err := h.moduleService.ApplyPackage(cloudResp.Data)
	if err != nil {
		h.logger.Warn("应用云端模块包失败", zap.String("module_id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "落盘失败: " + err.Error()})
		return
	}
	// T5.2：官方免费包下载即补齐派生 SKU 购买记录（「一键激活全部」可用）；付费 SKU 保持门禁。
	// 幂等（已购跳过），失败仅告警不阻断下载成功返回。
	userIDVal, _ := c.Get("user_id")
	userID, _ := userIDVal.(string)
	if err := h.moduleService.EnsurePackageProvision(id, userID); err != nil {
		h.logger.Warn("下载包后补购买记录失败", zap.String("module_id", id), zap.Error(err))
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": rec})
}
