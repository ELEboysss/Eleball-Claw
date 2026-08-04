package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/marketplace"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ModuleService 集市模块业务层（兼容层）。
// 底层已统一为 SkillRuntime，本层保留原有方法签名，负责与旧 API/admin-web 的字段转换。
type ModuleService struct {
	registry   *SkillRuntimeRegistry
	manager    *SkillRuntimeManager
	repo       *repository.SkillRuntimeRepo
	agentRepo  *repository.AgentRepo  // 可选：用于「已购模块」接口查询用户已购 SKU
	moduleRepo *repository.ModuleRepo // 可选：claw InstallFromCloudMeta 保留旧 modules 表 InstallSource 标记
	driverRepo *repository.DriverRepo // 可选：claw InstallFromCloudMeta 保留旧 drivers 表绑定
	// chatService 可选：skill-maker AI 起草 main.py 草稿（F1 收尾，调对话模型生成 stdio MCP 脚本）。
	chatService *ChatProxyService
	// P4：第三方模块镜像安装器（拉镜像 + 签名校验 + 启动容器）。官方预置模块不经此安装器。
	installer *ImageInstaller
}

// NewModuleService 创建模块业务服务
func NewModuleService(registry *SkillRuntimeRegistry, manager *SkillRuntimeManager, repo *repository.SkillRuntimeRepo, agentRepo *repository.AgentRepo) *ModuleService {
	return &ModuleService{
		registry:  registry,
		manager:   manager,
		repo:      repo,
		agentRepo: agentRepo,
		installer: NewImageInstaller(""), // 自动探测 docker/podman
	}
}

// SetAgentRepo 注入秘技仓库（claw 用：云端秘技安装后落本地 AgentItem/AgentPurchase）
func (s *ModuleService) SetAgentRepo(repo *repository.AgentRepo) {
	s.agentRepo = repo
}

// SetModuleRepo 注入旧模块仓库（claw 用：InstallFromCloudMeta 保留 InstallSource 来源标记）
func (s *ModuleService) SetModuleRepo(repo *repository.ModuleRepo) {
	s.moduleRepo = repo
}

// SetDriverRepo 注入旧驱动仓库（claw 用：InstallFromCloudMeta 保留驱动别名绑定）
func (s *ModuleService) SetDriverRepo(repo *repository.DriverRepo) {
	s.driverRepo = repo
}

// SetChatProxyService 注入对话代理服务（claw 用：skill-maker AI 起草 main.py，F1 收尾）。
func (s *ModuleService) SetChatProxyService(svc *ChatProxyService) {
	s.chatService = svc
}

// CheckRuntime 查询指定运行时状态，供 AgentToolLoader 过滤离线 SKU。
func (s *ModuleService) CheckRuntime(runtimeID string) *SkillRuntimeStatusSnapshot {
	if s.registry == nil {
		return nil
	}
	return s.registry.Check(runtimeID)
}

// ListInstalledModulesForUser 拉取当前用户已购秘技对应的可安装模块元数据
// （GET /v1/market/modules/installed，claw 云端拉取接口）。
func (s *ModuleService) ListInstalledModulesForUser(userID string, since *time.Time) ([]*ModuleInstallMeta, error) {
	if s.agentRepo == nil || s.repo == nil {
		return nil, errors.New("ModuleService 依赖未初始化")
	}
	items, err := s.agentRepo.ListPurchasedByUser(userID)
	if err != nil {
		return nil, err
	}

	purchaseTimes := map[string]time.Time{}
	if purchases, err := s.agentRepo.ListPurchasesByUser(userID); err == nil {
		for _, p := range purchases {
			if t, ok := purchaseTimes[p.AgentID]; !ok || p.CreatedAt.After(t) {
				purchaseTimes[p.AgentID] = p.CreatedAt
			}
		}
	}

	out := make([]*ModuleInstallMeta, 0, len(items))
	for _, item := range items {
		if item.ManifestJSON == "" {
			continue
		}
		var mf model.ToolManifest
		if err := json.Unmarshal([]byte(item.ManifestJSON), &mf); err != nil {
			continue
		}
		driverName := string(mf.Driver)
		if driverName == "" || driverName == string(model.ToolDriverNone) || driverName == string(model.ToolDriverBuiltin) {
			continue
		}
		rt, err := s.repo.GetByDriverID(driverName)
		if err != nil || rt == nil {
			continue
		}

		updatedAt := rt.UpdatedAt
		if pt, ok := purchaseTimes[item.ID]; ok && pt.After(updatedAt) {
			updatedAt = pt
		}
		if since != nil && !updatedAt.After(*since) {
			continue
		}

		meta := &ModuleInstallMeta{
			ModuleID:      rt.ID,
			AgentID:       item.ID,
			Name:          rt.Name,
			Description:   rt.Description,
			Version:       rt.Version,
			TransportType: legacyTransportType(rt.Transport),
			DriverID:      rt.DriverID,
			Official:      rt.Official,
			Capabilities:  rt.CapabilitiesList(),
			Manifest:      json.RawMessage(item.ManifestJSON),
			AuthToken:     rt.AuthToken,
			AvgRating:     item.AvgRating,
			PurchaseCount: item.PurchaseCount,
			UpdatedAt:     updatedAt.Format(time.RFC3339),
		}
		if activeCount, err := s.agentRepo.CountActiveUsers(item.ID); err == nil {
			meta.ActiveCount = activeCount
		}
		if !rt.Official {
			meta.Image = parseImageRef(rt.ImageRef, rt.ImageDigest)
			meta.Signature = rt.Signature
		}
		out = append(out, meta)
	}
	return out, nil
}

// mcpModuleBaseURL 从 MCP Server URL 中提取基础地址（scheme://host:port）
func mcpModuleBaseURL(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// parseImageRef 把容器镜像引用解析为 registry/repository/tag 结构
func parseImageRef(ref, digest string) *ModuleImageMeta {
	if ref == "" {
		return nil
	}
	rest := ref
	tag := ""
	if i := strings.LastIndex(rest, ":"); i > strings.LastIndex(rest, "/") {
		tag = rest[i+1:]
		rest = rest[:i]
	}
	registry := ""
	repository := rest
	if i := strings.Index(rest, "/"); i > 0 {
		registry = rest[:i]
		repository = rest[i+1:]
	}
	return &ModuleImageMeta{
		Registry:   registry,
		Repository: repository,
		Tag:        tag,
		Digest:     digest,
	}
}

// RegisterModule 管理后台注册/更新模块（转换为 SkillRuntime）
func (s *ModuleService) RegisterModule(record *model.ModuleRecord) error {
	rt := moduleRecordToRuntime(record)
	return s.registry.Register(rt)
}

// UnregisterModule 注销模块
func (s *ModuleService) UnregisterModule(moduleID string) error {
	return s.registry.Unregister(moduleID)
}

// ListModules 列出所有已注册模块（返回实时健康状态）
func (s *ModuleService) ListModules() ([]*model.ModuleRecord, error) {
	statuses := s.registry.List()
	items := make([]*model.ModuleRecord, 0, len(statuses))
	for _, st := range statuses {
		rt := s.registry.Get(st.RuntimeID)
		items = append(items, runtimeToModuleRecord(rt, st))
	}
	return items, nil
}

// GetModule 获取单个模块详情
func (s *ModuleService) GetModule(moduleID string) (*model.ModuleRecord, error) {
	rt := s.registry.Get(moduleID)
	if rt == nil {
		return nil, errors.New("模块不存在")
	}
	return runtimeToModuleRecord(rt, nil), nil
}

// RefreshModule 强制探测模块健康状态（忽略缓存）
func (s *ModuleService) RefreshModule(moduleID string) *SkillRuntimeStatusSnapshot {
	return s.registry.ForceProbe(moduleID)
}

// RegisterModuleFromPlugin 插件自助注册
func (s *ModuleService) RegisterModuleFromPlugin(req *model.ModuleRegisterRequest, providedToken string) (string, error) {
	if req.URL == "" {
		return "", errors.New("url 不能为空")
	}

	transport := model.SkillRuntimeTransportExecute
	deployment := model.SkillRuntimeDeploymentDocker
	switch model.ModuleTransportType(req.TransportType) {
	case model.ModuleTransportTypeMCP:
		transport = model.SkillRuntimeTransportMCPHTTP
	case model.ModuleTransportTypeRemoteURL:
		transport = model.SkillRuntimeTransportRawHTTP
		deployment = model.SkillRuntimeDeploymentNone
	}

	moduleID := req.ModuleID
	if moduleID == "" {
		moduleID = model.GenerateModuleID(req.Name)
	}

	rt := &model.SkillRuntime{
		ID:          moduleID,
		Name:        req.Name,
		Description: req.Description,
		Source:      model.SkillRuntimeSourceMarketplace,
		Transport:   transport,
		Deployment:  deployment,
		Endpoint:    req.URL,
		Version:     req.Version,
		AuthToken:   providedToken,
		Status:      model.SkillRuntimeStatusOffline,
	}
	rt.SetCapabilities(req.Capabilities)

	// 新流程：按 auth_token 绑定到已有驱动别名
	if providedToken != "" {
		if existing, err := s.ResolveDriverByAuthToken(providedToken); err == nil && existing != nil {
			rt.DriverID = existing.ID
			// 若驱动记录已指定 endpoint/transport，以驱动为准
			if existing.Endpoint != "" {
				rt.Endpoint = existing.Endpoint
			}
			if existing.TransportType == string(model.ModuleTransportTypeMCP) && existing.MCPServerConfig != nil {
				rt.Endpoint = mcpModuleBaseURL(existing.MCPServerConfig.URL)
				rt.SetMCPServerConfig(existing.MCPServerConfig)
			}
		}
	}

	if err := s.registry.Register(rt); err != nil {
		return "", err
	}
	return moduleID, nil
}

// ModuleImageMeta 第三方模块容器镜像信息（云端 ModuleInstallMeta.image）
type ModuleImageMeta struct {
	Registry   string `json:"registry"`
	Repository string `json:"repository"`
	Tag        string `json:"tag,omitempty"`
	Digest     string `json:"digest,omitempty"` // sha256:... 内容寻址
}

// ModuleInstallMeta 云端秘技拉取接口返回的单项（见 specs/api-schema.yml ModuleInstallMeta）。
// claw 据此安装到本地：official=true 直接激活预置；否则走 ImageInstaller 拉镜像 + 签名校验。
type ModuleInstallMeta struct {
	ModuleID      string           `json:"module_id"`
	AgentID       string           `json:"agent_id,omitempty"` // 云端秘技（AgentItem）ID；安装后据此在本地 upsert AgentItem，为空回退 manifest.id
	Name          string           `json:"name"`
	Description   string           `json:"description"`
	Version       string           `json:"version"`
	TransportType string           `json:"transport_type"`
	DriverID      string           `json:"driver_id,omitempty"`
	Official      bool             `json:"official"`
	Capabilities  []string         `json:"capabilities,omitempty"`
	Image         *ModuleImageMeta `json:"image,omitempty"`
	Signature     string           `json:"signature,omitempty"`
	Manifest      json.RawMessage  `json:"manifest,omitempty"`
	AuthToken     string           `json:"auth_token,omitempty"`
	UpdatedAt     string           `json:"updated_at,omitempty"`
	AvgRating     float64          `json:"avg_rating,omitempty"`
	PurchaseCount int64            `json:"purchase_count,omitempty"`
	ActiveCount   int64            `json:"active_count,omitempty"`
}

// marketplaceModuleManifest 内置模块目录中的 module.json 定义。
// 支持新旧两种字段命名：新格式使用 id/transport/deployment/endpoint，
// 旧格式使用 module_id/transport_type/url，向后兼容。
type marketplaceModuleManifest struct {
	ID                string                         `json:"id"`
	ModuleID          string                         `json:"module_id"` // 兼容旧格式
	Name              string                         `json:"name"`
	Description       string                         `json:"description"`
	URL               string                         `json:"url"`                           // 兼容旧格式
	Endpoint          string                         `json:"endpoint"`                      // 新格式
	Transport         string                         `json:"transport"`                     // 新格式
	TransportType     string                         `json:"transport_type"`                // 兼容旧格式
	Deployment        string                         `json:"deployment"`                    // 新格式
	Source            string                         `json:"source"`                        // 新格式
	Command           string                         `json:"command,omitempty"`             // process/stdio 启动命令
	Args              []string                       `json:"args,omitempty"`                // process/stdio 参数
	Env               map[string]string              `json:"env,omitempty"`                 // process/stdio 环境变量
	WorkDir           string                         `json:"work_dir,omitempty"`            // process/stdio 工作目录
	DockerComposePath string                         `json:"docker_compose_path,omitempty"` // 新格式
	SKUScope          string                         `json:"sku_scope"`
	AutoSKU           bool                           `json:"auto_sku,omitempty"`         // true=探活后自动派生 SKU，免手写 skus/*.json
	Credentials       map[string]model.CredentialDef `json:"credentials,omitempty"`      // auto_sku 模块凭证声明，派生 SKU 时透传进 manifest
	AllowedTools      []string                       `json:"allowed_tools,omitempty"`    // G2 工具白名单（非空时仅保留）
	DisallowedTools   []string                       `json:"disallowed_tools,omitempty"` // G2 工具黑名单（始终排除）
	Capabilities      []string                       `json:"capabilities"`
	MCPServerConfig   *model.MCPServerConfig         `json:"mcp_server_config,omitempty"`
	Driver            struct {
		ID          string `json:"driver_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"driver"`
}

// GetID 获取运行时 ID（优先新格式 id）
func (m *marketplaceModuleManifest) GetID() string {
	if m.ID != "" {
		return m.ID
	}
	return m.ModuleID
}

// GetTransport 获取 transport（优先新格式）
func (m *marketplaceModuleManifest) GetTransport() string {
	if m.Transport != "" {
		return m.Transport
	}
	return m.TransportType
}

// GetEndpoint 获取 endpoint（优先新格式）
func (m *marketplaceModuleManifest) GetEndpoint() string {
	if m.Endpoint != "" {
		return m.Endpoint
	}
	return m.URL
}

// GetDeployment 获取 deployment，默认 docker
func (m *marketplaceModuleManifest) GetDeployment() string {
	if m.Deployment != "" {
		return m.Deployment
	}
	return "docker"
}

// RescanMarketplace 运行时重新扫描 marketplace 目录，根据 module.json
// 自动补齐官方内置模块记录与驱动别名。新增官方模块时无需重启 gateway，
// 也无需走后台手动注册。
func (s *ModuleService) RescanMarketplace(logger *zap.Logger) error {
	root, err := EnsureMarketplaceRoot()
	if err != nil {
		if logger != nil {
			logger.Warn("初始化 marketplace 目录失败，跳过内置模块自动补齐", zap.Error(err))
		}
		return nil
	}
	if root == "" {
		if logger != nil {
			logger.Warn("未找到 marketplace 目录，跳过内置模块自动补齐")
		}
		return nil
	}
	return s.ensureMarketplaceModules(root, logger)
}

// ResolveMarketplaceRoot 解析 marketplace 根目录：
//  1. CLAW_MARKETPLACE_DIR 环境变量（显式指定，优先级最高）；
//  2. 当前目录下的 marketplace / gateway/marketplace（开发模式，从仓库内启动）；
//  3. ~/.eleball-claw/marketplace（安装版默认 home，首次使用时播种官方模块）。
func ResolveMarketplaceRoot() string {
	if dir := strings.TrimSpace(os.Getenv("CLAW_MARKETPLACE_DIR")); dir != "" {
		return dir
	}
	for _, p := range []string{"marketplace", "gateway/marketplace"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".eleball-claw", "marketplace")
}

// EnsureMarketplaceRoot 解析 marketplace 根目录，并把内嵌官方模块播种进去
// （只补缺失文件，不覆盖用户修改）。返回根目录；无法确定根目录时返回空串。
func EnsureMarketplaceRoot() (string, error) {
	root := ResolveMarketplaceRoot()
	if root == "" {
		return "", nil
	}
	if _, err := marketplace.SeedOfficial(root); err != nil {
		return "", err
	}
	return root, nil
}

// UserModuleGenerateRequest /v1/claw-console/mcp/generate 请求体（skill-maker 一键生成用户模块）。
// 探测用户脚本成功后，据工具列表 + 凭证声明生成 module.json + main.py 写入 marketplace home，
// rescan 注册 SkillRuntime -> autostart -> supervisor 探活触发 DeriveSKUs（阶段 E3）。
type UserModuleGenerateRequest struct {
	Command         string                         `json:"command"`          // stdio 启动命令（默认 python）
	Args            []string                       `json:"args"`             // stdio 参数（默认 [main.py]）
	Env             map[string]string              `json:"env"`              // env 模板（含 ${credentials.KEY}）
	WorkDir         string                         `json:"work_dir"`         // 用户脚本目录（探测 + 拷贝 main.py 来源）
	CredentialsMeta map[string]model.CredentialDef `json:"credentials_meta"` // 凭证声明（透传进 module.json）
	Name            string                         `json:"name"`             // 模块展示名
	Description     string                         `json:"description"`      // 模块描述
	ModuleID        string                         `json:"module_id"`        // 模块 ID（缺省据 name 生成）
	MainPyContent   string                         `json:"main_py_content"`  // main.py 草稿内容（web 编辑器；非空时优先落盘，见 writeUserMainPy）
}

// UserModuleGenerateResult 生成结果
type UserModuleGenerateResult struct {
	ModuleID  string    `json:"module_id"`
	ModuleDir string    `json:"module_dir"`
	Tools     []MCPTool `json:"tools"`
}

// WriteUserModule 一键生成用户 stdio MCP 模块：写 module.json + main.py -> rescan -> autostart。
// tools 为 ProbeStdio 探到的工具列表（写入 capabilities；SKU 由 supervisor 探活时 DeriveSKUs 派生）。
// main.py 优先用 web 编辑器草稿（main_py_content），其次拷贝 work_dir/args[0] 用户脚本，否则落 echo 骨架。
func (s *ModuleService) WriteUserModule(req UserModuleGenerateRequest, tools []MCPTool) (*UserModuleGenerateResult, error) {
	if req.Name == "" && req.ModuleID == "" {
		return nil, errors.New("name 或 module_id 不能为空")
	}
	moduleID := req.ModuleID
	if moduleID == "" {
		moduleID = sanitizeUserModuleID(req.Name)
	}
	if moduleID == "" {
		return nil, errors.New("无法推导合法 module_id，请显式传入")
	}

	// 禁止覆盖官方模块
	if existing := s.registry.Get(moduleID); existing != nil && existing.Official {
		return nil, fmt.Errorf("模块 ID %s 与官方模块冲突，请换一个", moduleID)
	}

	root, err := EnsureMarketplaceRoot()
	if err != nil {
		return nil, fmt.Errorf("初始化 marketplace 目录失败: %w", err)
	}
	if root == "" {
		return nil, errors.New("无法定位 marketplace 目录（设 CLAW_MARKETPLACE_DIR 或在仓库内运行）")
	}
	moduleDir := filepath.Join(root, moduleID)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建模块目录失败: %w", err)
	}

	if err := writeUserMainPy(moduleDir, req); err != nil {
		return nil, err
	}
	if err := writeUserModuleJSON(moduleDir, moduleID, req, tools); err != nil {
		return nil, err
	}

	// rescan 注册 SkillRuntime（含 AutoSKU/Credentials/DriverID）
	if err := s.RescanMarketplace(nil); err != nil {
		return nil, fmt.Errorf("rescan 失败: %w", err)
	}

	// autostart：supervisor 探活成功后自动调 DeriveSKUs 派生 SKU（阶段 D2）。
	// 失败重试：若模块已在运行（重新生成场景），先停旧进程再启动，使新 main.py 生效
	// （startProcess 对已运行进程是 no-op，不重启则旧代码继续跑）。
	if s.manager != nil {
		if s.manager.IsRunning(moduleID) {
			_ = s.manager.Stop(moduleID)
		}
		_ = s.manager.Start(moduleID) // best-effort：失败不阻断生成，模块已落盘，下次 rescan/autostart 仍可拉起
	}

	return &UserModuleGenerateResult{
		ModuleID:  moduleID,
		ModuleDir: moduleDir,
		Tools:     tools,
	}, nil
}

// writeUserMainPy 写 main.py：优先用 web 编辑器草稿（main_py_content），其次拷贝 work_dir/args[0]
// 的用户脚本，最后落 echo 骨架。草稿优先以 honoring 用户在 web 的预览/编辑结果。
func writeUserMainPy(moduleDir string, req UserModuleGenerateRequest) error {
	target := filepath.Join(moduleDir, "main.py")
	if strings.TrimSpace(req.MainPyContent) != "" {
		return os.WriteFile(target, []byte(req.MainPyContent), 0o644)
	}
	if req.WorkDir != "" && len(req.Args) > 0 {
		src := filepath.Join(req.WorkDir, req.Args[0])
		if data, err := os.ReadFile(src); err == nil {
			return os.WriteFile(target, data, 0o644)
		}
	}
	return os.WriteFile(target, []byte(userModuleEchoSkeleton), 0o644)
}

// writeUserModuleJSON 生成 module.json（mcp_stdio + process + auto_sku:true）。
func writeUserModuleJSON(moduleDir, moduleID string, req UserModuleGenerateRequest, tools []MCPTool) error {
	command := req.Command
	if command == "" {
		command = "python"
	}
	args := req.Args
	if len(args) == 0 {
		args = []string{"main.py"}
	}
	caps := make([]string, 0, len(tools))
	for _, t := range tools {
		caps = append(caps, t.Name)
	}
	m := marketplaceModuleManifest{
		ID:           moduleID,
		Name:         req.Name,
		Description:  req.Description,
		Source:       "marketplace",
		Transport:    "mcp_stdio",
		Deployment:   "process",
		Command:      command,
		Args:         args,
		SKUScope:     "claw",
		AutoSKU:      true,
		Env:          req.Env,
		Credentials:  req.CredentialsMeta,
		Capabilities: caps,
	}
	m.Driver.ID = moduleID
	m.Driver.Name = req.Name
	m.Driver.Description = req.Description
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 module.json 失败: %w", err)
	}
	return os.WriteFile(filepath.Join(moduleDir, "module.json"), data, 0o644)
}

// sanitizeUserModuleID 据展示名推导合法 module ID：小写 + [a-z0-9-]，其余折叠为单 -。
func sanitizeUserModuleID(name string) string {
	var sb strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			prevDash = false
		} else if !prevDash && sb.Len() > 0 {
			sb.WriteRune('-')
			prevDash = true
		}
	}
	return strings.Trim(sb.String(), "-")
}

// userModuleEchoSkeleton 最小 stdio MCP 骨架（echo 工具），用户未提供脚本时落盘以便后续编辑。
const userModuleEchoSkeleton = `#!/usr/bin/env python3
"""用户模块 stdio MCP 骨架（由 skill-maker /mcp/generate 生成）。替换 tools_list/tools_call 为真实逻辑。"""
import json, sys

def make_result(req_id, result): return {"jsonrpc": "2.0", "id": req_id, "result": result}
def make_error(req_id, code, msg): return {"jsonrpc": "2.0", "id": req_id, "error": {"code": code, "message": msg}}

def tools_list():
    return [{"name": "echo", "description": "回显输入",
             "inputSchema": {"type": "object", "properties": {"message": {"type": "string"}}, "required": ["message"]}}]

def tools_call(name, arguments):
    if name == "echo":
        return {"content": [{"type": "text", "text": arguments.get("message", "")}]}
    return {"isError": True, "content": [{"type": "text", "text": "Unknown tool: %s" % name}]}

def main():
    for line in sys.stdin:
        line = line.strip()
        if not line: continue
        try: req = json.loads(line)
        except Exception as e:
            sys.stdout.write(json.dumps(make_error(None, -32700, "Parse error: %s" % e)) + "\n"); sys.stdout.flush(); continue
        req_id = req.get("id"); method = req.get("method"); params = req.get("params", {}) or {}
        if method == "initialize":
            resp = make_result(req_id, {"protocolVersion": "2024-11-05", "capabilities": {"tools": {}},
                                        "serverInfo": {"name": "user-module", "version": "1.0.0"}})
        elif method == "notifications/initialized": continue
        elif method == "tools/list": resp = make_result(req_id, {"tools": tools_list()})
        elif method == "tools/call": resp = make_result(req_id, tools_call(params.get("name"), params.get("arguments", {})))
        else: resp = make_error(req_id, -32601, "Method not found: %s" % method)
        sys.stdout.write(json.dumps(resp) + "\n"); sys.stdout.flush()

if __name__ == "__main__": main()
`

// DraftMainPyRequest skill-maker AI 起草 main.py 请求（F1 收尾）。
// 据能力描述 + 凭证声明调对话模型生成 stdio MCP 脚本草稿，供用户在 web 编辑器预览/编辑后再 generate。
type DraftMainPyRequest struct {
	CapabilityDescription string                         `json:"capability_description"` // 自然语言能力描述（要做什么、暴露哪些工具）
	CredentialsMeta       map[string]model.CredentialDef `json:"credentials_meta"`       // 凭证声明（告知 LLM 有哪些 env 可读，key 大写即变量名）
	Command               string                         `json:"command"`                // 启动命令（仅信息性，默认 python）
	Args                  []string                       `json:"args"`                   // 参数
	Provider              string                         `json:"provider"`               // LLM provider（eleagent）
	Model                 string                         `json:"model"`                  // LLM 模型名 provider/model_name
}

// DraftMainPy 用配置的对话模型据能力描述起草 stdio MCP main.py 草稿。
// 返回纯 Python 代码（已剥离 markdown 围栏）。chatService 未注入 / 能力描述为空 / 模型未指定时返回可读错误。
func (s *ModuleService) DraftMainPy(ctx context.Context, req DraftMainPyRequest) (string, error) {
	if s.chatService == nil {
		return "", errors.New("对话服务未初始化，无法 AI 起草")
	}
	if strings.TrimSpace(req.CapabilityDescription) == "" {
		return "", errors.New("请填写能力描述")
	}
	provider := strings.TrimSpace(req.Provider)
	model := strings.TrimSpace(req.Model)
	if provider == "" || model == "" {
		return "", errors.New("请选择用于起草的模型")
	}

	chatReq := &ChatRequest{
		Provider: provider,
		Model:    model,
		Messages: []llm.Message{
			{Role: "system", Content: skillMakerDraftSystem},
			{Role: "user", Content: buildDraftMainPyUserPrompt(req)},
		},
		Stream:    false,
		MaxTokens: 4096,
	}
	chunk, err := s.chatService.Chat(ctx, chatReq)
	if err != nil {
		return "", fmt.Errorf("起草模型调用失败: %w", err)
	}
	if chunk == nil || strings.TrimSpace(chunk.Delta) == "" {
		return "", errors.New("起草模型未返回有效内容")
	}
	return stripCodeFences(chunk.Delta), nil
}

// buildDraftMainPyUserPrompt 组装起草 user prompt：能力描述 + 启动方式 + 凭证 -> 环境变量映射。
func buildDraftMainPyUserPrompt(req DraftMainPyRequest) string {
	var sb strings.Builder
	sb.WriteString("能力描述：\n")
	sb.WriteString(strings.TrimSpace(req.CapabilityDescription))
	sb.WriteString("\n\n启动方式：")
	command := req.Command
	if command == "" {
		command = "python"
	}
	args := req.Args
	if len(args) == 0 {
		args = []string{"main.py"}
	}
	sb.WriteString(command + " " + strings.Join(args, " "))

	sb.WriteString("\n\n凭证声明：\n")
	if len(req.CredentialsMeta) == 0 {
		sb.WriteString("无（脚本无需读取任何凭证）\n")
	} else {
		// 固定顺序输出，避免 map 迭代顺序不稳导致 prompt 抖动
		keys := make([]string, 0, len(req.CredentialsMeta))
		for k := range req.CredentialsMeta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			def := req.CredentialsMeta[k]
			sb.WriteString(fmt.Sprintf("- key=%s 类型=%s 标签=%s -> 环境变量 %s（脚本用 os.environ.get('%s') 读取）\n",
				k, def.Type, def.Label, strings.ToUpper(k), strings.ToUpper(k)))
		}
	}
	sb.WriteString("\n请据此生成完整可运行的 main.py。")
	return sb.String()
}

// stripCodeFences 剥离模型输出可能的 markdown 代码围栏（```python ... ```），返回纯源码。
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// 去掉首行围栏开头（```python / ``` 等）
	if idx := strings.Index(s, "\n"); idx >= 0 {
		s = strings.TrimSpace(s[idx+1:])
	} else {
		return ""
	}
	// 去掉结尾的 ```
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// skillMakerDraftSystem skill-maker AI 起草 main.py 的系统提示。
// 约束模型产出零依赖、可直接探测运行的 stdio MCP 服务端脚本。
const skillMakerDraftSystem = `你是一位 stdio MCP 服务端开发专家。任务：根据用户的能力描述，生成一个完整、可直接运行的最小 stdio MCP 服务端 Python 脚本（main.py）。

技术规范（必须严格遵守）：
1. 协议：从 stdin 逐行读 NDJSON（每行一个 JSON-RPC 2.0 请求），向 stdout 写 NDJSON 响应，每条响应后立即 sys.stdout.flush()。
2. 仅用 Python 标准库（json/sys/os/urllib 等）。禁止在模块顶层 import 任何第三方库；若某工具确实需要第三方包，只能在该工具函数内部懒加载 import，并在注释中标注所需依赖（如 # 需要：pip install requests）。
3. 必须处理这些 JSON-RPC 方法：
   - initialize：返回 {"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"<据能力命名>","version":"1.0.0"}}
   - notifications/initialized：不返回任何响应（continue）
   - tools/list：返回 {"tools":[...]}
   - tools/call：按 params.name 分发，返回 {"content":[{"type":"text","text":...}]}；未知工具返回 {"isError":true,"content":[{"type":"text","text":"Unknown tool: ..."}]}
   - 其它方法返回 JSON-RPC 错误码 -32601
4. 每个工具声明 name、description、inputSchema（JSON Schema 对象，含 type/properties/required）。
5. 凭证：用户声明的凭证会以环境变量注入（变量名 = 凭证 key 大写）。脚本用 os.environ.get('KEY_UPPERCASE') 读取，绝不要从请求参数读取凭证。
6. 工具逻辑：据能力描述实现真实可用逻辑——优先用标准库 urllib.request 调外部 HTTP API；纯本地能力直接实现。不要只写占位 echo。
7. 输出：只输出 Python 源码本身，以 #!/usr/bin/env python3 开头。不要 markdown 围栏、不要解释、不要前后多余文字。`

// TestCallRequest /v1/claw-console/modules/:id/test-call 请求体（F2）。
// 直接调用模块某工具（绕过 LLM/Agent 层），展示原始返回，供造秘技页验证生成模块可调用。
type TestCallRequest struct {
	ToolName  string                 `json:"tool_name"` // 工具名（MCP tool name）
	Arguments map[string]interface{} `json:"arguments"` // tools/call 参数
}

// TestCall 直接调用模块某工具并返回原始结果。用于造秘技页验证「从 0 造模块并调用成功」（F2）。
// 凭证：stdio 模块在 spawn 时已注入 module 级 env（D1），此处无需 per-call 注入；
// 模块离线或会话未注册时返回可读错误。userID 用于 execute/raw_http transport 透传（stdio 不用）。
func (s *ModuleService) TestCall(ctx context.Context, moduleID string, req TestCallRequest, userID string) (map[string]interface{}, error) {
	if s.registry == nil {
		return nil, errors.New("SkillRuntimeRegistry 未初始化")
	}
	if strings.TrimSpace(req.ToolName) == "" {
		return nil, errors.New("tool_name 不能为空")
	}
	rt := s.registry.Get(moduleID)
	if rt == nil {
		return nil, errors.New("模块不存在")
	}
	st := s.registry.Check(moduleID)
	if st == nil || !st.Online {
		msg := "模块离线，请稍候或检查脚本/解释器"
		if st != nil && st.Error != "" {
			msg += "：" + st.Error
		}
		return nil, errors.New(msg)
	}
	args := req.Arguments
	if args == nil {
		args = map[string]interface{}{}
	}
	return s.registry.Execute(moduleID, req.ToolName, args, userID)
}

func (s *ModuleService) ensureMarketplaceModules(root string, logger *zap.Logger) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "module.json")
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		var m marketplaceModuleManifest
		if err := json.Unmarshal(data, &m); err != nil {
			if logger != nil {
				logger.Warn("解析 module.json 失败", zap.String("path", path), zap.Error(err))
			}
			continue
		}
		moduleID := m.GetID()
		if moduleID == "" || m.Driver.ID == "" {
			if logger != nil {
				logger.Warn("module.json 缺少必填字段", zap.String("path", path))
			}
			continue
		}

		transport := parseSkillRuntimeTransport(m.GetTransport())
		deployment := parseSkillRuntimeDeployment(m.GetDeployment())
		endpoint := m.GetEndpoint()
		if transport == model.SkillRuntimeTransportMCPHTTP && m.MCPServerConfig != nil {
			endpoint = mcpModuleBaseURL(m.MCPServerConfig.URL)
		}

		existing, err := s.repo.GetByID(moduleID)
		if err != nil {
			existing = nil
		}

		rt := &model.SkillRuntime{
			ID:                moduleID,
			Name:              m.Name,
			Description:       m.Description,
			Source:            model.SkillRuntimeSource(m.Source),
			Transport:         transport,
			Deployment:        deployment,
			Endpoint:          endpoint,
			Command:           m.Command,
			WorkDir:           m.WorkDir,
			DockerComposePath: m.DockerComposePath,
			Official:          true,
			DriverID:          m.Driver.ID,
			AutoSKU:           m.AutoSKU,
		}
		rt.SetArgs(m.Args)
		rt.SetEnv(m.Env)
		rt.SetCredentials(m.Credentials)
		rt.SetAllowedTools(m.AllowedTools)
		rt.SetDisallowedTools(m.DisallowedTools)
		if rt.Source == "" {
			rt.Source = model.SkillRuntimeSourceMarketplace
		}
		if rt.DockerComposePath == "" && deployment == model.SkillRuntimeDeploymentDocker {
			rt.DockerComposePath = filepath.Join(root, moduleID, "docker-compose.yml")
		}
		// process 部署默认工作目录为模块目录（spawn 时 cmd.Dir），使相对 command/args 能定位模块脚本
		if deployment == model.SkillRuntimeDeploymentProcess && rt.WorkDir == "" {
			rt.WorkDir = filepath.Join(root, moduleID)
		}
		rt.SetCapabilities(m.Capabilities)
		if transport == model.SkillRuntimeTransportMCPHTTP && m.MCPServerConfig != nil {
			rt.SetMCPServerConfig(m.MCPServerConfig)
		}

		if existing != nil {
			rt.Status = existing.Status
			rt.CreatedAt = existing.CreatedAt
			rt.UpdatedAt = time.Now()
		} else {
			rt.Status = model.SkillRuntimeStatusOffline
			rt.CreatedAt = time.Now()
			rt.UpdatedAt = rt.CreatedAt
		}

		if err := s.registry.Register(rt); err != nil {
			if logger != nil {
				logger.Warn("自动补齐内置 SkillRuntime 失败", zap.String("id", moduleID), zap.Error(err))
			}
		} else {
			if logger != nil {
				logger.Info("已自动补齐内置 SkillRuntime", zap.String("id", moduleID), zap.String("transport", string(transport)))
			}
		}
	}
	return nil
}

// RegisterDriver 注册/更新驱动映射（转换为 SkillRuntime）
func (s *ModuleService) RegisterDriver(req *model.DriverRegisterRequest) error {
	transport := model.SkillRuntimeTransportExecute
	deployment := model.SkillRuntimeDeploymentExternal
	endpoint := ""
	switch model.ModuleTransportType(req.TransportType) {
	case model.ModuleTransportTypeMCP:
		transport = model.SkillRuntimeTransportMCPHTTP
		if req.MCPServerConfig != nil {
			endpoint = req.MCPServerConfig.URL
		}
	case model.ModuleTransportTypeRemoteURL:
		transport = model.SkillRuntimeTransportRawHTTP
		deployment = model.SkillRuntimeDeploymentNone
		endpoint = req.Endpoint
	case model.ModuleTransportTypeModule:
		deployment = model.SkillRuntimeDeploymentDocker
		if req.ModuleID != "" {
			endpoint = "http://" + req.ModuleID + ":8080"
		}
	}

	rt := &model.SkillRuntime{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		Source:      model.SkillRuntimeSourceMarketplace,
		Transport:   transport,
		Deployment:  deployment,
		Endpoint:    endpoint,
		DriverID:    req.ID,
		AuthToken:   req.AuthToken,
		Status:      model.SkillRuntimeStatusOffline,
	}
	if req.TransportType == string(model.ModuleTransportTypeMCP) && req.MCPServerConfig != nil {
		rt.SetMCPServerConfig(req.MCPServerConfig)
	}
	return s.registry.Register(rt)
}

// MCPInstallRequest 动态安装远端 MCP server 请求（G3，Smithery 式）。
// 用户在 web 输入 stdio 启动命令或 http URL，探测成功后创建 source=mcp_remote 的 SkillRuntime。
type MCPInstallRequest struct {
	Transport   string `json:"transport"` // mcp_stdio（默认） | mcp_http
	Name        string `json:"name"`      // 必填，运行时名称 + ID 派生
	Description string `json:"description"`
	// stdio 字段
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	WorkDir string            `json:"work_dir"`
	// http 字段
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers"`
}

// MCPInstallResult 动态安装结果
type MCPInstallResult struct {
	RuntimeID string    `json:"runtime_id"`
	DriverID  string    `json:"driver_id"`
	Transport string    `json:"transport"`
	Tools     []MCPTool `json:"tools"`
	SKUCount  int       `json:"sku_count"`
}

// InstallMCPRuntime 动态安装远端 MCP server（G3）。
// 不经 marketplace 目录，直接创建 source=mcp_remote 的 SkillRuntime 并持久化到 DB。
// tools 为调用方（handler）已探测到的工具列表，用于立即派生 SKU（auto_sku）。
// - http：Register 后 ForceProbe 同步探活（probeHeaders 发送字面量鉴权头）-> DeriveSKUs。
// - stdio：Register 后 manager.Start 异步拉起长驻进程，其 probeStdioRuntime -> DeriveSKUs。
// 自驱动：DriverID=runtimeID，SKU manifest.Driver 据此经 ResolveDriver->GetByDriverID 回溯本 runtime。
func (s *ModuleService) InstallMCPRuntime(req *MCPInstallRequest, tools []MCPTool) (*MCPInstallResult, error) {
	if req.Name == "" {
		return nil, errors.New("name 不能为空")
	}
	if len(tools) == 0 {
		return nil, errors.New("探测到的工具为空，无法安装")
	}

	transport := model.SkillRuntimeTransportMCPStdio
	deployment := model.SkillRuntimeDeploymentProcess
	if req.Transport == "mcp_http" {
		transport = model.SkillRuntimeTransportMCPHTTP
		deployment = model.SkillRuntimeDeploymentExternal
	}

	runtimeID := "mcp-remote-" + model.GenerateModuleID(req.Name)
	rt := &model.SkillRuntime{
		ID:          runtimeID,
		Name:        req.Name,
		Description: req.Description,
		Source:      model.SkillRuntimeSourceMCPRemote, // G3：首个使用 mcp_remote 来源
		Transport:   transport,
		Deployment:  deployment,
		AutoSKU:     true,
		DriverID:    runtimeID, // 自驱动
		Status:      model.SkillRuntimeStatusOffline,
	}
	caps := make([]string, 0, len(tools))
	for _, t := range tools {
		caps = append(caps, t.Name)
	}
	rt.SetCapabilities(caps)

	switch transport {
	case model.SkillRuntimeTransportMCPHTTP:
		rt.Endpoint = req.Endpoint
		rt.SetMCPServerConfig(&model.MCPServerConfig{
			URL:     req.Endpoint,
			Headers: req.Headers,
		})
	default: // stdio
		rt.Command = req.Command
		rt.SetArgs(req.Args)
		rt.SetEnv(req.Env)
		rt.WorkDir = req.WorkDir
	}

	if err := s.registry.Register(rt); err != nil {
		return nil, fmt.Errorf("注册运行时失败: %w", err)
	}

	skuCount := 0
	if transport == model.SkillRuntimeTransportMCPHTTP {
		// 同步探活：probeHeaders 发送字面量头 -> tools/list -> DeriveSKUs -> online
		if snap := s.registry.ForceProbe(runtimeID); snap != nil && snap.Online {
			skuCount = len(snap.Capabilities)
		}
	} else {
		// stdio：异步拉起长驻进程，SKU 由后台 probeStdioRuntime 派生；返回期望数
		_ = s.manager.Start(runtimeID)
		skuCount = len(caps)
	}

	return &MCPInstallResult{
		RuntimeID: runtimeID,
		DriverID:  runtimeID,
		Transport: string(transport),
		Tools:     tools,
		SKUCount:  skuCount,
	}, nil
}

// UnregisterDriver 注销驱动映射
func (s *ModuleService) UnregisterDriver(driverID string) error {
	rt, err := s.repo.GetByDriverID(driverID)
	if err != nil {
		return nil
	}
	return s.registry.Unregister(rt.ID)
}

// ListDrivers 列出所有动态驱动映射
func (s *ModuleService) ListDrivers() ([]*model.DriverRecord, error) {
	runtimes, err := s.repo.List()
	if err != nil {
		return nil, err
	}
	items := make([]*model.DriverRecord, 0, len(runtimes))
	for _, rt := range runtimes {
		items = append(items, runtimeToDriverRecord(rt))
	}
	return items, nil
}

// ResolveDriver 根据驱动名解析动态驱动记录
func (s *ModuleService) ResolveDriver(driverID string) (*model.DriverRecord, error) {
	rt, err := s.repo.GetByDriverID(driverID)
	if err != nil {
		return nil, err
	}
	return runtimeToDriverRecord(rt), nil
}

// ResolveDriverByAuthToken 根据 auth_token 解析动态驱动记录
func (s *ModuleService) ResolveDriverByAuthToken(token string) (*model.DriverRecord, error) {
	if token == "" {
		return nil, errors.New("auth_token 不能为空")
	}
	runtimes, err := s.repo.List()
	if err != nil {
		return nil, err
	}
	for _, rt := range runtimes {
		if rt.AuthToken == token {
			return runtimeToDriverRecord(rt), nil
		}
	}
	return nil, errors.New("驱动不存在")
}

// BindDriverModule 将驱动别名绑定到指定模块（更新 SkillRuntime 的 endpoint/module_id）
func (s *ModuleService) BindDriverModule(driverID, moduleID string) error {
	rt, err := s.repo.GetByDriverID(driverID)
	if err != nil {
		return err
	}
	rt.Endpoint = "http://" + moduleID + ":8080"
	return s.registry.Register(rt)
}

// ===== 转换辅助函数 =====

func moduleRecordToRuntime(rec *model.ModuleRecord) *model.SkillRuntime {
	transport := model.SkillRuntimeTransportExecute
	deployment := model.SkillRuntimeDeploymentDocker
	switch rec.TransportType {
	case model.ModuleTransportTypeMCP:
		transport = model.SkillRuntimeTransportMCPHTTP
	case model.ModuleTransportTypeRemoteURL:
		transport = model.SkillRuntimeTransportRawHTTP
		deployment = model.SkillRuntimeDeploymentNone
	}

	rt := &model.SkillRuntime{
		ID:          rec.ID,
		Name:        rec.Name,
		Description: rec.Description,
		Source:      model.SkillRuntimeSourceMarketplace,
		Transport:   transport,
		Deployment:  deployment,
		Endpoint:    rec.URL,
		ImageRef:    rec.ImageRef,
		ImageDigest: rec.ImageDigest,
		Signature:   rec.Signature,
		AuthToken:   rec.AuthToken,
		Version:     rec.Version,
		Official:    rec.Official,
		DriverID:    rec.ID,
	}
	rt.SetCapabilities(rec.CapabilitiesList())
	return rt
}

func runtimeToModuleRecord(rt *model.SkillRuntime, st *SkillRuntimeStatusSnapshot) *model.ModuleRecord {
	rec := &model.ModuleRecord{
		ID:            rt.ID,
		Name:          rt.Name,
		Description:   rt.Description,
		URL:           rt.Endpoint,
		TransportType: model.ModuleTransportTypeModule,
		Version:       rt.Version,
		Official:      rt.Official,
		ImageRef:      rt.ImageRef,
		ImageDigest:   rt.ImageDigest,
		Signature:     rt.Signature,
		AuthToken:     rt.AuthToken,
	}
	rec.SetCapabilities(rt.CapabilitiesList())

	switch rt.Transport {
	case model.SkillRuntimeTransportMCPHTTP:
		rec.TransportType = model.ModuleTransportTypeMCP
	case model.SkillRuntimeTransportRawHTTP:
		rec.TransportType = model.ModuleTransportTypeRemoteURL
	}

	if st != nil {
		rec.Status = model.ModuleStatusOffline
		if st.Online {
			rec.Status = model.ModuleStatusOnline
		}
		rec.HealthError = st.Error
		now := time.Now()
		rec.LastHeartbeat = &now
	}
	return rec
}

func runtimeToDriverRecord(rt *model.SkillRuntime) *model.DriverRecord {
	rec := &model.DriverRecord{
		ID:        rt.DriverID,
		Name:      rt.Name,
		ModuleID:  rt.ID,
		Endpoint:  rt.Endpoint,
		AuthToken: rt.AuthToken,
	}
	switch rt.Transport {
	case model.SkillRuntimeTransportMCPHTTP:
		rec.TransportType = string(model.ModuleTransportTypeMCP)
		rec.MCPServerConfig = rt.GetMCPServerConfig()
	case model.SkillRuntimeTransportRawHTTP:
		rec.TransportType = string(model.ModuleTransportTypeRemoteURL)
	default:
		rec.TransportType = string(model.ModuleTransportTypeModule)
	}
	return rec
}

func parseSkillRuntimeTransport(s string) model.SkillRuntimeTransport {
	switch s {
	case "execute":
		return model.SkillRuntimeTransportExecute
	case "mcp_http", "mcp":
		return model.SkillRuntimeTransportMCPHTTP
	case "raw_http":
		return model.SkillRuntimeTransportRawHTTP
	case "mcp_stdio":
		return model.SkillRuntimeTransportMCPStdio
	default:
		return model.SkillRuntimeTransportExecute
	}
}

func parseSkillRuntimeDeployment(s string) model.SkillRuntimeDeployment {
	switch s {
	case "process":
		return model.SkillRuntimeDeploymentProcess
	case "docker":
		return model.SkillRuntimeDeploymentDocker
	case "external":
		return model.SkillRuntimeDeploymentExternal
	case "none":
		return model.SkillRuntimeDeploymentNone
	default:
		return model.SkillRuntimeDeploymentDocker
	}
}

func legacyTransportType(t model.SkillRuntimeTransport) string {
	switch t {
	case model.SkillRuntimeTransportMCPHTTP:
		return "mcp"
	case model.SkillRuntimeTransportRawHTTP:
		return "remote_url"
	default:
		return "module"
	}
}

// upsertCloudPurchasedModuleRecord 将官方模块标记为云端购买来源并写入旧 modules 表。
// 用于 InstallFromCloudMeta 的幂等与首次安装路径，保证 IsCloudPurchasedAgent 能判定来源。
func (s *ModuleService) upsertCloudPurchasedModuleRecord(moduleID string, rt *model.SkillRuntime, version string) error {
	if s.moduleRepo == nil {
		return nil
	}
	old, _ := s.moduleRepo.GetByID(moduleID)
	if old == nil {
		old = &model.ModuleRecord{ID: moduleID, Name: rt.Name}
	}
	old.Official = true
	old.InstallSource = "cloud-purchased"
	if version != "" {
		old.Version = version
	}
	old.URL = rt.Endpoint
	old.TransportType = model.ModuleTransportTypeModule
	if rt.Transport == model.SkillRuntimeTransportMCPHTTP {
		old.TransportType = model.ModuleTransportTypeMCP
	} else if rt.Transport == model.SkillRuntimeTransportRawHTTP {
		old.TransportType = model.ModuleTransportTypeRemoteURL
	}
	old.SetCapabilities(rt.CapabilitiesList())
	return s.moduleRepo.CreateOrUpdate(old)
}

// InstallFromCloudMeta 把云端拉取的 ModuleInstallMeta 安装到本地。
//
// P4 安装流程（见 docs/marketing/claw-implementation-plan.md §F.2）：
//   - official=true：直接激活本地预置（marketplace/ 已扫描注册），无需拉镜像。
//   - 第三方：ImageInstaller 拉镜像 + 签名校验 + 启动容器，再写入 registry 激活。
//
// 返回安装后的 ModuleRecord（含 image/signature 元数据）。已安装同 module_id 视为幂等成功。
func (s *ModuleService) InstallFromCloudMeta(meta ModuleInstallMeta) (*model.ModuleRecord, error) {
	if s.registry == nil {
		return nil, errors.New("SkillRuntimeRegistry 未初始化")
	}

	// 已存在则幂等返回（避免重复拉镜像/启动容器）
	if existing, err := s.repo.GetByID(meta.ModuleID); err == nil && existing != nil {
		// 官方预置模块：幂等路径也补齐旧 modules 表的 official/来源标记（本地扫描建记录时无此信息）。
		// 注意：经云端 installed 接口安装的官方模块同样标记 cloud-purchased，
		// 与本地纯扫描预置（InstallSource 为空）区分，激活时统一走 VIP 门控。
		if meta.Official && s.moduleRepo != nil {
			if err := s.upsertCloudPurchasedModuleRecord(meta.ModuleID, existing, meta.Version); err != nil {
				return nil, err
			}
		}
		// 幂等路径也要补齐驱动绑定（首次安装时驱动写库失败重试、云端补发 driver_id 等场景）
		if err := s.upsertDriverBinding(meta); err != nil {
			return nil, err
		}
		record := runtimeToModuleRecord(existing, s.registry.Check(meta.ModuleID))
		if meta.Official {
			record.InstallSource = "cloud-purchased"
		}
		return record, nil
	}

	var record *model.ModuleRecord
	if meta.Official {
		// 官方模块：依赖 marketplace 扫描已注册；此处补齐旧 modules 表的 official/来源标记并返回
		rec, err := s.repo.GetByID(meta.ModuleID)
		if err != nil || rec == nil {
			return nil, fmt.Errorf("官方模块 %s 未在本地预置，请确认 marketplace/ 已包含", meta.ModuleID)
		}
		if err := s.upsertCloudPurchasedModuleRecord(meta.ModuleID, rec, meta.Version); err != nil {
			return nil, err
		}
		record = runtimeToModuleRecord(rec, s.registry.Check(meta.ModuleID))
		record.InstallSource = "cloud-purchased"
	} else {
		// 第三方：拉镜像 + 签名 + 启动容器
		if s.installer == nil || s.installer.Runtime() == "" {
			return nil, fmt.Errorf("未检测到容器运行时（docker/podman），无法安装第三方模块 %s", meta.ModuleID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		rec, err := s.installer.Install(ctx, meta)
		if err != nil {
			return nil, err
		}
		rt := moduleRecordToRuntime(rec)
		if err := s.registry.Register(rt); err != nil {
			return nil, fmt.Errorf("注册模块到 registry 失败: %w", err)
		}
		record = rec
	}

	// 安装成功后按 meta upsert 本地驱动别名并绑定模块（official/第三方通用）。
	// 本地 drivers 表缺该 DriverRecord 时新建，存在则更新绑定与令牌。
	if err := s.upsertDriverBinding(meta); err != nil {
		return nil, err
	}

	// 触发一次健康探测刷新状态
	record.Status = model.ModuleStatusOffline
	if st := s.registry.ForceProbe(meta.ModuleID); st != nil {
		if st.Online {
			record.Status = model.ModuleStatusOnline
		}
		record.Version = st.Version
		record.SetCapabilities(st.Capabilities)
		record.HealthError = st.Error
	}
	return record, nil
}

// upsertDriverBinding 按云端 meta upsert 本地驱动别名并绑定到已安装模块。
// meta.DriverID 为空时不做任何事。已存在的驱动记录只更新绑定/传输类型/令牌，保留其余字段。
func (s *ModuleService) upsertDriverBinding(meta ModuleInstallMeta) error {
	if meta.DriverID == "" {
		return nil
	}
	if s.driverRepo == nil {
		return errors.New("DriverRepo 未初始化")
	}

	transportType := meta.TransportType
	if transportType == "" {
		transportType = string(model.ModuleTransportTypeModule)
	}

	if existing, err := s.driverRepo.GetByID(meta.DriverID); err == nil && existing != nil {
		changed := false
		if existing.ModuleID != meta.ModuleID {
			existing.ModuleID = meta.ModuleID
			changed = true
		}
		if meta.TransportType != "" && existing.TransportType != meta.TransportType {
			existing.TransportType = meta.TransportType
			changed = true
		}
		if meta.AuthToken != "" && existing.AuthToken != meta.AuthToken {
			existing.AuthToken = meta.AuthToken
			changed = true
		}
		if existing.Name == "" {
			existing.Name = meta.Name
			if existing.Name == "" {
				existing.Name = meta.DriverID
			}
			changed = true
		}
		if !changed {
			return nil
		}
		if err := s.driverRepo.CreateOrUpdate(existing); err != nil {
			return fmt.Errorf("更新驱动别名 %s 失败: %w", meta.DriverID, err)
		}
		return nil
	}

	name := meta.Name
	if name == "" {
		name = meta.DriverID
	}
	rec := &model.DriverRecord{
		ID:            meta.DriverID,
		Name:          name,
		TransportType: transportType,
		ModuleID:      meta.ModuleID,
		AuthToken:     meta.AuthToken,
	}
	if err := s.driverRepo.CreateOrUpdate(rec); err != nil {
		return fmt.Errorf("创建驱动别名 %s 失败: %w", meta.DriverID, err)
	}
	return nil
}

// EnsureCloudAgentProvision 云端秘技安装成功后，按 meta.Manifest 在本地落库：
//   - upsert AgentItem（status=approved、manifest_json 落库；已存在时保留 purchase_count 等统计字段）；
//   - 为当前用户幂等写入 AgentPurchase（金额 0，来源为云端已购）。
//
// 未注入 AgentRepo（云端 cmd/server）或 meta 未携带 manifest 时直接跳过。
// 本地 AgentItem.ID 优先取 meta.AgentID，为空回退 manifest.id。
func (s *ModuleService) EnsureCloudAgentProvision(meta ModuleInstallMeta, userID string) error {
	if s.agentRepo == nil {
		return nil
	}
	if len(meta.Manifest) == 0 || string(meta.Manifest) == "null" {
		return nil
	}
	if userID == "" {
		return errors.New("无法识别当前用户，无法写入秘技购买记录")
	}

	var manifest model.ToolManifest
	if err := json.Unmarshal(meta.Manifest, &manifest); err != nil {
		return fmt.Errorf("manifest 解析失败: %w", err)
	}

	agentID := meta.AgentID
	if agentID == "" {
		agentID = manifest.ID
	}
	if agentID == "" {
		return errors.New("manifest 缺少 id 且 meta 未下发 agent_id，无法落库秘技记录")
	}

	name := manifest.Name
	if name == "" {
		name = meta.Name
	}
	description := manifest.Description
	if description == "" {
		description = meta.Description
	}
	level := model.AgentLevel(manifest.Level)
	if level < model.AgentLevelHuang || level > model.AgentLevelFenJue {
		level = model.AgentLevelHuang
	}

	if existing, err := s.agentRepo.GetByID(agentID); err == nil && existing != nil {
		// 已存在：只更新描述性字段，保留 purchase_count/avg_rating 等统计与价格、创作者信息
		existing.Name = name
		existing.Description = description
		existing.Category = manifest.Category
		existing.Level = level
		existing.ManifestJSON = string(meta.Manifest)
		existing.Status = model.AgentStatusApproved
		if err := s.agentRepo.Update(existing); err != nil {
			return fmt.Errorf("更新本地秘技 %s 失败: %w", agentID, err)
		}
	} else {
		item := &model.AgentItem{
			ID:           agentID,
			Name:         name,
			Description:  description,
			CreatorID:    "cloud",
			CreatorName:  "Eleball 云端",
			Category:     manifest.Category,
			Level:        level,
			ManifestJSON: string(meta.Manifest),
			Status:       model.AgentStatusApproved,
		}
		if err := s.agentRepo.Create(item); err != nil {
			return fmt.Errorf("创建本地秘技 %s 失败: %w", agentID, err)
		}
	}

	// 幂等写入购买记录：云端已购秘技本地补单，金额记 0
	purchased, err := s.agentRepo.HasPurchased(agentID, userID)
	if err != nil {
		return err
	}
	if purchased {
		return nil
	}
	purchase := &model.AgentPurchase{
		ID:              uuid.New().String(),
		AgentID:         agentID,
		BuyerID:         userID,
		PricePaid:       0,
		Currency:        "cloud-purchased",
		CreatorEarnings: 0,
		PlatformFee:     0,
	}
	if err := s.agentRepo.CreatePurchase(purchase); err != nil {
		return fmt.Errorf("写入秘技购买记录失败: %w", err)
	}
	return nil
}
