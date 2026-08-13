package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/marketplace"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ModuleService 集市模块业务层（兼容层）。
// 底层已统一为 SkillRuntime，本层保留原有方法签名，负责与旧 API/admin-web 的字段转换。
type ModuleService struct {
	registry  *SkillRuntimeRegistry
	manager   *SkillRuntimeManager
	repo      *repository.SkillRuntimeRepo
	agentRepo *repository.AgentRepo // 可选：用于「已购模块」接口查询用户已购 SKU
	// chatService 可选：skill-maker AI 起草 main.py 草稿（F1 收尾，调对话模型生成 stdio MCP 脚本）。
	chatService *ChatProxyService
	// bootstrap 可选：H2 装依赖时确保解释器可用（python/node 托管下载，H1）。
	bootstrap *InterpreterBootstrap
	// P4：第三方模块镜像安装器（拉镜像 + 签名校验 + 启动容器）。官方预置模块不经此安装器。
	installer *ImageInstaller
	// dockerStarter 可选：claw 控制台「启动服务」按钮拉起 docker 部署模块时调用
	// （docker compose up 逻辑在 cmd/claw-server，经此回调注入，避免 service 层依赖 cmd）。
	dockerStarter func(moduleID string) error
	// agentToolLoader 可选：manifest→模块解析统一入口（ActivatedModuleIDs 用），main 注入。
	agentToolLoader *AgentToolLoader
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

// SetChatProxyService 注入对话代理服务（claw 用：skill-maker AI 起草 main.py，F1 收尾）。
func (s *ModuleService) SetChatProxyService(svc *ChatProxyService) {
	s.chatService = svc
}

// SetInterpreterBootstrap 注入托管解释器引导器（claw 用：H2 装依赖时确保 python/node 可用）。
func (s *ModuleService) SetInterpreterBootstrap(b *InterpreterBootstrap) {
	s.bootstrap = b
}

// SetDockerStarter 注入 docker 模块启动回调（claw cmd 注入：docker compose up）。
// 控制台「启动服务」按钮对 docker 部署模块调用此回调拉起容器。
func (s *ModuleService) SetDockerStarter(fn func(moduleID string) error) {
	s.dockerStarter = fn
}

// SetAgentToolLoader 注入动态工具加载器，用于 ActivatedModuleIDs 的 manifest→模块解析。
func (s *ModuleService) SetAgentToolLoader(loader *AgentToolLoader) {
	s.agentToolLoader = loader
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
			TransportType: string(rt.Transport),
			DriverID:      rt.DriverID,
			Official:      rt.Official,
			Origin:        string(rt.Origin),
			Actor:         rt.Actor,
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

// RegisterModule 管理后台注册/更新模块（统一落 skill_runtimes）
func (s *ModuleService) RegisterModule(rt *model.SkillRuntime) error {
	return s.registry.Register(rt)
}

// UnregisterModule 注销模块
func (s *ModuleService) UnregisterModule(moduleID string) error {
	return s.registry.Unregister(moduleID)
}

// ListModules 列出所有运行时（管理后台；skill_runtimes 为单一事实源）
func (s *ModuleService) ListModules() ([]*model.SkillRuntime, error) {
	runtimes, err := s.repo.List()
	if err != nil {
		return nil, err
	}
	return runtimes, nil
}

// GetModule 获取单个运行时详情
func (s *ModuleService) GetModule(moduleID string) (*model.SkillRuntime, error) {
	rt, err := s.repo.GetByID(moduleID)
	if err != nil {
		return nil, errors.New("模块不存在")
	}
	return rt, nil
}

// ModuleSubmissionMetaFor 据模块 ID 构造分享审核元数据（T3.2 对齐 package 布局）。
// 优先读 marketplace/<id>/package.json —— T3.1 生成模块的唯一事实源（其运行时是 {pkg}-mcp-main，
// 无同名运行时记录，GetModule 查不到）；origin 读 .origin 侧车（缺省 user）。无 package.json 时
// 回退运行时记录（legacy module.json 目录 / DB-only MCP 模块，运行时 ID == 模块 ID）。
func (s *ModuleService) ModuleSubmissionMetaFor(moduleID string) (*model.ModuleSubmissionMeta, error) {
	if root := ResolveMarketplaceRoot(); root != "" {
		modDir := filepath.Join(root, moduleID)
		if b, err := os.ReadFile(filepath.Join(modDir, "package.json")); err == nil {
			pkg, perr := model.ParsePackageManifest(b)
			if perr != nil {
				return nil, fmt.Errorf("模块 %s 的 package.json 解析失败: %w", moduleID, perr)
			}
			origin := string(model.SkillRuntimeOriginUser)
			if ob, oerr := os.ReadFile(filepath.Join(modDir, ".origin")); oerr == nil {
				if s := strings.TrimSpace(string(ob)); s != "" {
					origin = s
				}
			}
			return &model.ModuleSubmissionMeta{
				ModuleID:    moduleID,
				Name:        pkg.Name,
				Description: pkg.Description,
				Origin:      origin,
				Version:     pkg.Version,
			}, nil
		}
	}
	rt, err := s.GetModule(moduleID)
	if err != nil {
		return nil, err
	}
	return &model.ModuleSubmissionMeta{
		ModuleID:     rt.ID,
		Name:         rt.Name,
		Description:  rt.Description,
		Origin:       string(rt.Origin),
		Actor:        rt.Actor,
		Version:      rt.Version,
		Capabilities: rt.CapabilitiesList(),
	}, nil
}

// requiredEnv 据 SkillRuntime 部署方式 + 启动命令推断所需本地环境，供控制台离线说明展示。
// docker -> "docker"；process -> 据 command（python* / node，缺省 python）；none/external -> ""。
func requiredEnvFor(rt *model.SkillRuntime) string {
	if rt == nil {
		return ""
	}
	switch rt.Deployment {
	case model.SkillRuntimeDeploymentDocker:
		return "docker"
	case model.SkillRuntimeDeploymentProcess:
		cmd := strings.TrimSpace(rt.Command)
		switch {
		case strings.HasPrefix(cmd, "node"):
			return "node"
		default: // 空 / python / python3 / 其它脚本均归 python（示例与生成模块均为 python）
			return "python"
		}
	default:
		return ""
	}
}

// RefreshModule 强制探测模块健康状态（忽略缓存）
func (s *ModuleService) RefreshModule(moduleID string) *SkillRuntimeStatusSnapshot {
	return s.registry.ForceProbe(moduleID)
}

// ActivatedModuleIDs 返回已被购买（激活）的模块 ID 集合。
// claw 单用户：任意 buyer 购买过该模块对应 SKU 即视为激活，用于 boot autostart 门控
// 与控制台「启动服务」前置判断。agentRepo 或解析失败时返回空集（保守不启动）。
func (s *ModuleService) ActivatedModuleIDs() map[string]bool {
	set := make(map[string]bool)
	if s.agentRepo == nil {
		return set
	}
	items, err := s.agentRepo.ListAllPurchasedAgents()
	if err != nil {
		return set
	}
	for _, item := range items {
		manifest, _ := item.Manifest()
		if manifest == nil || s.agentToolLoader == nil {
			continue
		}
		if modID := s.agentToolLoader.ResolveModuleID(manifest); modID != "" {
			set[modID] = true
		}
	}
	return set
}

// Start 拉起指定模块（控制台「启动服务」按钮）。按部署方式分流：
//   - process：同步 manager.Start spawn 子进程，随后 ForceProbe 刷新状态。
//   - docker：异步执行 dockerStarter 回调（pull/compose 耗时长，不阻塞 HTTP），立即返回当前状态；完成后再 ForceProbe。
//   - none/external：无需启动，仅 ForceProbe。
//
// 返回最新状态快照；未注册模块或 docker 回调未注入返回错误。
func (s *ModuleService) Start(moduleID string) (*SkillRuntimeStatusSnapshot, error) {
	rt := s.registry.Get(moduleID)
	if rt == nil {
		return nil, errors.New("模块不存在")
	}
	switch rt.Deployment {
	case model.SkillRuntimeDeploymentProcess:
		if err := s.manager.Start(moduleID); err != nil {
			return nil, err
		}
		return s.registry.ForceProbe(moduleID), nil
	case model.SkillRuntimeDeploymentDocker:
		if s.dockerStarter == nil {
			return nil, errors.New("docker 启动未配置")
		}
		// D-D：dockerStarter 收模块目录名而非 runtime ID——package 布局下 runtime ID 是
		// {pkg}-mcp-{key}（含 -mcp-main 后缀），但 compose 以目录 {pkg}/ 为单位。
		// DockerComposePath={modDir}/docker-compose[.claw].yml → 取父目录 basename；legacy 布局
		// （rt.ID==目录名）回退 moduleID。claw-server main 已按目录名解析 compose 文件。
		dir := moduleID
		if rt.DockerComposePath != "" {
			if base := filepath.Base(filepath.Dir(rt.DockerComposePath)); base != "" && base != "." {
				dir = base
			}
		}
		starter := s.dockerStarter
		go func() {
			if err := starter(dir); err != nil {
				// 异步失败：写入状态 degraded，前端刷新可见异常 + 原因。
				s.registry.SetStatus(moduleID, model.SkillRuntimeStatusDegraded, nil, err.Error())
				return
			}
			s.registry.ForceProbe(moduleID)
		}()
		// 立即返回当前状态（异步启动中），前端稍后刷新。
		return s.registry.ForceProbe(moduleID), nil
	default:
		// none/external：已在线或纯远端，仅探测刷新。
		return s.registry.ForceProbe(moduleID), nil
	}
}

// RegisterModuleFromPlugin 插件自助注册
func (s *ModuleService) RegisterModuleFromPlugin(req *model.PluginRegisterRequest, providedToken string) (string, error) {
	if req.URL == "" {
		return "", errors.New("url 不能为空")
	}

	transport := model.SkillRuntimeTransportExecute
	deployment := model.SkillRuntimeDeploymentDocker
	switch req.TransportType {
	case "mcp":
		transport = model.SkillRuntimeTransportMCPHTTP
	case "remote_url":
		transport = model.SkillRuntimeTransportRawHTTP
		deployment = model.SkillRuntimeDeploymentNone
	}

	moduleID := req.ID
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
		Status:      model.SkillRuntimeStatusInstalled,
	}
	rt.SetCapabilities(req.Capabilities)

	// 新流程：按 auth_token 绑定到已有驱动别名（driver 已统一为 SkillRuntime，key=DriverID）
	if providedToken != "" {
		if existing, err := s.ResolveDriverByAuthToken(providedToken); err == nil && existing != nil {
			rt.DriverID = existing.DriverID
			// 若驱动运行时已指定 endpoint/transport，以驱动为准
			if existing.Endpoint != "" {
				rt.Endpoint = existing.Endpoint
			}
			if existing.IsMCP() && existing.GetMCPServerConfig() != nil {
				cfg := existing.GetMCPServerConfig()
				rt.Endpoint = cfg.URL
				rt.SetMCPServerConfig(cfg)
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
	Origin        string           `json:"origin,omitempty"` // 模块来源（builtin/cloud/user），云端下发，InstallFromCloudMeta 持久化到本地运行时
	Actor         string           `json:"actor,omitempty"`  // 来源主体（user 时为作者）；builtin/cloud（eleball）为空
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
	Origin            string                         `json:"origin,omitempty"`              // 模块来源（builtin/cloud/user），缺失由 Register 按 side 默认
	Actor             string                         `json:"actor,omitempty"`               // 来源主体（user 时为作者）
	Command           string                         `json:"command,omitempty"`             // process/stdio 启动命令
	Args              []string                       `json:"args,omitempty"`                // process/stdio 参数
	Env               map[string]string              `json:"env,omitempty"`                 // process/stdio 环境变量
	WorkDir           string                         `json:"work_dir,omitempty"`            // process/stdio 工作目录
	DockerComposePath string                         `json:"docker_compose_path,omitempty"` // 新格式
	AutoSKU           bool                           `json:"auto_sku,omitempty"`            // true=探活后自动派生 SKU，免手写 skus/*.json
	Credentials       map[string]model.CredentialDef `json:"credentials,omitempty"`         // auto_sku 模块凭证声明，派生 SKU 时透传进 manifest
	AllowedTools      []string                       `json:"allowed_tools,omitempty"`       // G2 工具白名单（非空时仅保留）
	DisallowedTools   []string                       `json:"disallowed_tools,omitempty"`    // G2 工具黑名单（始终排除）
	Capabilities      []string                       `json:"capabilities"`
	Version           string                         `json:"version,omitempty"` // 模块语义版本（可选，catalog/下载比对用）
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
	Username        string                         `json:"username"`         // 创建者用户名（写 .origin 时记录；前端 T5 传入，缺失则空）
	Version         string                         `json:"version"`          // package.json version（缺省 0.1.0，T3.1 秘技包）
	Category        string                         `json:"category"`         // package.json category（缺省空，T3.1 秘技包）
}

// UserModuleGenerateResult 生成结果（T3.1 秘技包：package.json + main.py 落盘 marketplace/{id}/）
type UserModuleGenerateResult struct {
	ModuleID  string    `json:"module_id"`  // 包名 = marketplace 目录名（slug）
	RuntimeID string    `json:"runtime_id"` // 物化 SkillRuntime ID = {pkg}-mcp-main（TestCall 用）
	SKUID     string    `json:"sku_id"`     // 派生 SKU ID = {pkg}-mcp-main
	ModuleDir string    `json:"module_dir"`
	Tools     []MCPTool `json:"tools"`
}

// WriteUserModule 一键生成用户秘技包（T3.1）：写 package.json + main.py -> RescanPackage 物化 -> autostart。
// tools 为 ProbeStdio 探到的工具列表（仅回传前端展示/试跑；SKU 由 RescanPackage 据 mcpServers 派生单条）。
// main.py 优先用 web 编辑器草稿（main_py_content），其次拷贝 work_dir/args[0] 用户脚本，否则落 echo 骨架。
// package.json 以 mcpServers.main（stdio）描述主脚本 → 运行时 {pkg}-mcp-main；.origin 侧车标记 user。
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

	// 禁止覆盖官方模块：registry 命中（legacy 布局 rt.ID==目录名）或 marketplace 目录按 origin 推断
	// （package 布局 runtime ID={pkg}-mcp-{key} ≠ 目录名，registry.Get(moduleID) 取不到——T5.1 官方模块
	// 全部 package 化后必须按目录判定，否则用户可写 module_id=agent-reach 覆盖官方包）。
	root, err := EnsureMarketplaceRoot()
	if err != nil {
		return nil, fmt.Errorf("初始化 marketplace 目录失败: %w", err)
	}
	if root == "" {
		return nil, errors.New("无法定位 marketplace 目录（设 CLAW_MARKETPLACE_DIR 或在仓库内运行）")
	}
	if existing := s.registry.Get(moduleID); existing != nil && existing.Official {
		return nil, fmt.Errorf("模块 ID %s 与官方模块冲突，请换一个", moduleID)
	}
	if _, statErr := os.Stat(filepath.Join(root, moduleID, "package.json")); statErr == nil {
		if packageDirOrigin(filepath.Join(root, moduleID), "claw").IsOfficial(moduleID) {
			return nil, fmt.Errorf("模块 ID %s 与官方模块冲突，请换一个", moduleID)
		}
	}
	moduleDir := filepath.Join(root, moduleID)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建模块目录失败: %w", err)
	}

	if err := writeUserMainPy(moduleDir, req); err != nil {
		return nil, err
	}
	// T3.1：写 package.json（mcpServers.main stdio 描述主脚本）+ .origin 侧车（user），替代 legacy module.json。
	// RescanPackage 物化运行时 {pkg}-mcp-main（mcp_stdio/process，WorkDir=模块目录）与 SKU {pkg}-mcp-main。
	runtimeID := moduleID + "-mcp-main"
	if err := writeUserPackageJSON(moduleDir, moduleID, req); err != nil {
		return nil, err
	}

	// rescan 物化 SkillRuntime + SKU（T2.2 RescanPackage）；claw 侧收录全部来源
	if err := s.RescanPackage("claw", nil); err != nil {
		return nil, fmt.Errorf("rescan 失败: %w", err)
	}

	// autostart：supervisor 拉起 stdio 进程并注册会话，使 TestCall/调用在线。
	// 失败重试：若运行时已在运行（重新生成场景），先停旧进程再启动，使新 main.py 生效
	// （startProcess 对已运行进程是 no-op，不重启则旧代码继续跑）。
	if s.manager != nil {
		if s.manager.IsRunning(runtimeID) {
			_ = s.manager.Stop(runtimeID)
		}
		_ = s.manager.Start(runtimeID) // best-effort：失败不阻断生成，包已落盘，下次 rescan/autostart 仍可拉起
	}

	return &UserModuleGenerateResult{
		ModuleID:  moduleID,
		RuntimeID: runtimeID,
		SKUID:     runtimeID,
		ModuleDir: moduleDir,
		Tools:     tools,
	}, nil
}

// ApplyPackage 下载整包落盘（云端 GET /v1/market/modules/:id/package -> PackageBundle）。
// 解包到 marketplace/<id>/：package.json + .origin 侧车 + skills/*/SKILL.md + skus/*.json +
// tools_files（工具实现脚本/资源文件，含 docker-compose.claw.yml），然后 RescanPackage 物化 + best-effort 拉起。
// skill/mcp/tool 三类：skill 经 skills/ 落盘、mcp 经 package.json.mcpServers 物化、tool 经 tools_files/skus 落盘。
// 手动下载语义（D4：不自动拉取，用户主动触发）：已存在则覆盖同名字段，不删本地额外文件。
func (s *ModuleService) ApplyPackage(pkg model.PackageBundle) (*model.SkillRuntime, error) {
	if pkg.PackageID == "" || len(pkg.PackageJSON) == 0 {
		return nil, errors.New("模块包缺少 package_id 或 package_json")
	}
	root := ResolveMarketplaceRoot()
	if root == "" {
		return nil, errors.New("无法定位 marketplace 目录（设 CLAW_MARKETPLACE_DIR 或在仓库内运行）")
	}
	moduleDir := filepath.Join(root, pkg.PackageID)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建模块目录失败: %w", err)
	}
	// package.json（云端原文；RescanPackage 据 mcpServers 物化 package 运行时）
	if err := os.WriteFile(filepath.Join(moduleDir, "package.json"), pkg.PackageJSON, 0o644); err != nil {
		return nil, fmt.Errorf("写 package.json 失败: %w", err)
	}
	// .origin 侧车（provenance，防伪造——package.json 不声明 origin，T1.3）
	origin := pkg.ToolsFiles[".origin"]
	if origin == "" {
		origin = "cloud"
	}
	if err := os.WriteFile(filepath.Join(moduleDir, ".origin"), []byte(origin), 0o644); err != nil {
		return nil, fmt.Errorf("写 .origin 失败: %w", err)
	}
	// skills/*/SKILL.md
	for _, sk := range pkg.Skills {
		skDir := filepath.Join(moduleDir, "skills", sk.Name)
		if err := os.MkdirAll(skDir, 0o755); err != nil {
			return nil, fmt.Errorf("创建 skills/%s 失败: %w", sk.Name, err)
		}
		if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte(sk.Content), 0o644); err != nil {
			return nil, fmt.Errorf("写 skills/%s/SKILL.md 失败: %w", sk.Name, err)
		}
	}
	// skus/*.json（手写 SKU；能力派生 SKU 由 rescan + DeriveSKUs 再生成）
	if len(pkg.SKUs) > 0 {
		skuDir := filepath.Join(moduleDir, "skus")
		if err := os.MkdirAll(skuDir, 0o755); err != nil {
			return nil, fmt.Errorf("创建 skus 目录失败: %w", err)
		}
		for _, sku := range pkg.SKUs {
			name := strings.TrimSuffix(sku.FileName, ".json") + ".json"
			if err := os.WriteFile(filepath.Join(skuDir, name), sku.Content, 0o644); err != nil {
				return nil, fmt.Errorf("写 SKU %s 失败: %w", sku.FileName, err)
			}
		}
	}
	// tools_files 工具实现脚本/资源文件（含 docker-compose.claw.yml——docker 模块 serve 拉起时 ACR pull_first）
	for rel, content := range pkg.ToolsFiles {
		if rel == ".origin" || rel == "" || strings.Contains(rel, "..") {
			continue
		}
		target := filepath.Join(moduleDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("创建 %s 目录失败: %w", rel, err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("写 %s 失败: %w", rel, err)
		}
	}
	// rescan 注册 SkillRuntime + SKU（package 布局物化为 {id}-mcp-{key} 运行时）
	if err := s.RescanPackage("claw", nil); err != nil {
		return nil, fmt.Errorf("rescan 失败: %w", err)
	}
	rec, err := s.GetModule(pkg.PackageID)
	if err != nil || rec == nil {
		// package 布局运行时 ID 形如 {package_id}-mcp-main，兼容回退
		rec, err = s.GetModule(pkg.PackageID + "-mcp-main")
	}
	if err != nil || rec == nil {
		return nil, fmt.Errorf("模块 %s 落盘后 rescan 未注册成功", pkg.PackageID)
	}
	// best-effort 拉起（docker 触发 ACR pull_first 起 container，process 起 stdio）。
	// 异步启动，立即返回可能 offline；失败不阻断下载（模块已落盘，前端 refresh 看最终状态）。
	_, _ = s.Start(rec.ID)
	return rec, nil
}

// CloudCatalogEnriched 云端目录项 + 本地安装比对（T4.4「检查更新」）。
// 透传云端 catalog 原值；Installed/LocalVersion/HasUpdate/LocalStatus 由 EnrichCloudCatalog 现算。
// web「云端模块」tab 据此决定按钮态：未安装→下载 / 已装且更新→更新 / 已装最新→已最新（禁用）。
type CloudCatalogEnriched struct {
	model.PackageCatalogItem
	Installed    bool   `json:"installed"`     // 本地是否已安装该包
	LocalVersion string `json:"local_version"` // 本地已装版本（空=未安装）
	HasUpdate    bool   `json:"has_update"`    // 云端有新版可更新（已装且 catalog.version > local_version）
	LocalStatus  string `json:"local_status"`  // 本地运行时状态（T1.4 枚举；未安装为空）
}

// EnrichCloudCatalog 为云端目录项补充本地安装/更新状态（T4.4 更新检测，比对主键=version，
// updated_at 透传供展示）。纯查询，不触发任何安装/下载副作用；web 每次打开目录时现算。
func (s *ModuleService) EnrichCloudCatalog(items []model.PackageCatalogItem) []CloudCatalogEnriched {
	out := make([]CloudCatalogEnriched, 0, len(items))
	for _, it := range items {
		e := CloudCatalogEnriched{PackageCatalogItem: it}
		if rt := s.findPackageRuntime(it.PackageID); rt != nil {
			e.Installed = true
			e.LocalVersion = rt.Version
			if it.Version != "" && compareVersions(it.Version, rt.Version) > 0 {
				e.HasUpdate = true
			}
			// 状态以 registry 现算快照为准（探活结果），无快照回退 DB 记录值。
			e.LocalStatus = string(rt.Status)
			if st := s.registry.Check(rt.ID); st != nil {
				e.LocalStatus = string(st.Status)
			}
		}
		out = append(out, e)
	}
	return out
}

// findPackageRuntime 在本地已安装运行时中定位 packageID 对应的包运行时。
// package 布局下包没有同名运行时（派生运行时形如 {pkg}-mcp-{key} / {pkg}-{tool}），
// 先试精确 ID 与主 MCP 运行时，再按 {pkg}- 前缀扫描（同包派生运行时版本一致，取首个即可）。
// 返回 nil 表示本地未安装。
func (s *ModuleService) findPackageRuntime(packageID string) *model.SkillRuntime {
	if rt, err := s.GetModule(packageID); err == nil {
		return rt
	}
	if rt, err := s.GetModule(packageID + "-mcp-main"); err == nil {
		return rt
	}
	if runtimes, err := s.ListModules(); err == nil {
		prefix := packageID + "-"
		for _, rt := range runtimes {
			if strings.HasPrefix(rt.ID, prefix) {
				return rt
			}
		}
	}
	return nil
}

// compareVersions 宽松 semver 比较（依赖无关）：按 '.' 分段逐段比较，短串缺省段补 0
// （"1.2" == "1.2.0"，避免因版本写法差异误报更新）。纯数字段数值比较（"1.10.0" > "1.9.0"），
// 数字段 > 非数字段（发布段 > 预发布段，如 "1.0.0" > "1.0.0-beta"），双非数字段字典序。
// 返回 -1/0/1；空串低于任何版本。用于 T4.4 更新检测：仅 catalog.version 严格大于
// local_version 才判定有新版（无新版不误报）。
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		sa, sb := "0", "0" // 短串缺省段视为 0
		if i < len(as) {
			sa = as[i]
		}
		if i < len(bs) {
			sb = bs[i]
		}
		if cmp := compareVersionSegment(sa, sb); cmp != 0 {
			return cmp
		}
	}
	return 0
}

func compareVersionSegment(a, b string) int {
	an, aErr := strconv.Atoi(a)
	bn, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case aErr == nil: // a 为数字段（发布），b 非数字（预发布/构建）：发布段更高
		return 1
	case bErr == nil:
		return -1
	}
	switch { // 双非数字段：字典序
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
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

// writeUserPackageJSON 生成 package.json（T3.1）：mcpServers.main 描述主脚本（stdio）。
// name=moduleID（slug，满足包名校验），version/category 来自请求（version 缺省 0.1.0）。
// env 透传请求 env 模板（${credentials.KEY}，spawn 时按运行时凭证解析）。
// 同时写 .origin 侧车（user）：package.json 不声明 origin 防伪造（T1.3），RescanPackage 据侧车定 origin。
// 注：凭证 defs 本期不写入（PackageMCPServer schema 无 credentials 字段），待 T4.3 激活/凭证链补。
func writeUserPackageJSON(moduleDir, moduleID string, req UserModuleGenerateRequest) error {
	command := req.Command
	if command == "" {
		command = "python"
	}
	args := req.Args
	if len(args) == 0 {
		args = []string{"main.py"}
	}
	version := req.Version
	if version == "" {
		version = "0.1.0"
	}
	pkg := model.PackageManifest{
		Name:        moduleID,
		Version:     version,
		Description: req.Description,
		Category:    req.Category,
		Level:       1,
		MCPServers: map[string]model.PackageMCPServer{
			"main": {
				Transport: "stdio",
				Command:   []string{command},
				Args:      args,
				Env:       req.Env,
			},
		},
	}
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 package.json 失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "package.json"), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(moduleDir, ".origin"), []byte(string(model.SkillRuntimeOriginUser)), 0o644)
}

// sanitizeUserModuleID 据展示名推导合法 module ID：小写 + [a-z0-9-]，其余折叠为单 -，
// 再追加 uuid8 后缀（复用 cloud generateUniqueModuleID 的 uuid 模式）使重名模块不撞
// （为 T11 分享到云端铺路：不同用户同名模块得到不同 ID，云端无需改名、本地↔云端 ID 链稳定）。
// 仅用于新生成模块（module_id 缺省路径，WriteUserModule:596）；显式传 module_id 的重新生成/定点
// 走 :594 旁路，不经过本函数，故「仅影响新生成模块，不破坏存量查找，官方模块 ID 不变」。
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
	base := strings.Trim(sb.String(), "-")
	if base == "" {
		base = "mod" // 名称无可折叠字符时回退，镜像 cloud generateUniqueModuleID 的 mod-<uuid8>
	}
	return base + "-" + uuid.New().String()[:8]
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

// RegisterDriver 注册/更新驱动运行时（driver 已统一为 SkillRuntime，key=DriverID）
func (s *ModuleService) RegisterDriver(rt *model.SkillRuntime) error {
	return s.registry.Register(rt)
}

// ensureDriver 确保 SKU 所需的驱动别名已存在并持有 auth_token。
// driver 已统一为 SkillRuntime（key=DriverID），返回 driver_id 和 auth_token。
// driver-binding 统一入口：原 AgentMarketService.ensureDriverForManifest 的逻辑收敛至此。
func (s *ModuleService) ensureDriver(driverID, name, description string) (string, string, error) {
	rec, err := s.repo.GetByDriverID(driverID)
	if err == nil && rec != nil {
		// 已存在：若没有 token，生成一个并更新；否则直接返回现有 token
		if rec.AuthToken != "" {
			return rec.ID, rec.AuthToken, nil
		}
		rec.AuthToken = model.GenerateDriverAuthToken()
		rec.UpdatedAt = time.Now()
		if err := s.registry.Register(rec); err != nil {
			return "", "", err
		}
		return rec.ID, rec.AuthToken, nil
	}

	// 不存在：新建驱动运行时（execute 型占位，后续 RescanPackage 物化真实配置）
	token := model.GenerateDriverAuthToken()
	if name == "" {
		name = driverID
	}
	rt := &model.SkillRuntime{
		ID:          driverID,
		Name:        name,
		Description: description,
		Source:      model.SkillRuntimeSourceMarketplace,
		Transport:   model.SkillRuntimeTransportExecute,
		Deployment:  model.SkillRuntimeDeploymentDocker,
		DriverID:    driverID,
		AuthToken:   token,
		Status:      model.SkillRuntimeStatusInstalled,
	}
	if err := s.registry.Register(rt); err != nil {
		return "", "", err
	}
	return driverID, token, nil
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

	// 追加 uuid8 后缀（复用 cloud generateUniqueModuleID 的 uuid 模式）使重名 MCP 模块不撞（T6，为 T11 铺路）。
	runtimeID := "mcp-remote-" + model.GenerateModuleID(req.Name) + "-" + uuid.New().String()[:8]
	rt := &model.SkillRuntime{
		ID:          runtimeID,
		Name:        req.Name,
		Description: req.Description,
		Source:      model.SkillRuntimeSourceMCPRemote, // G3：首个使用 mcp_remote 来源
		Transport:   transport,
		Deployment:  deployment,
		AutoSKU:     true,
		DriverID:    runtimeID, // 自驱动
		Status:      model.SkillRuntimeStatusInstalled,
	}
	rt.Origin = model.SkillRuntimeOriginUser // type4b：MCP 安装 fold 进 user，actor=MCP 名
	rt.Actor = req.Name
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

// mcpDesktopServer Claude Desktop（claude_desktop_config.json）/ Cursor / .mcp.json 单个 MCP server 配置。
// stdio（command/args/env）或 http（url/headers）二选一；不认识的字段（type/alwaysAllow 等）忽略不报错。
type mcpDesktopServer struct {
	Command string            `json:"command"` // stdio 启动命令（如 npx）
	Args    []string          `json:"args"`    // stdio 参数
	Env     map[string]string `json:"env"`     // stdio 环境变量
	URL     string            `json:"url"`     // http MCP 地址（Cursor/.mcp.json 用 url 而非 command）
	Headers map[string]string `json:"headers"` // http MCP 请求头
}

// mcpDesktopConfig Claude Desktop / Cursor / .mcp.json 通用 MCP 配置根结构。
type mcpDesktopConfig struct {
	McpServers map[string]mcpDesktopServer `json:"mcpServers"`
}

// ParseMCPConfig 解析标准 MCP client 配置（Claude Desktop / Cursor / .mcp.json 通用格式，M4），
// 把每个 mcpServers 条目映射为 MCPInstallRequest 供调用方逐个 InstallMCPRuntime。
// 有 url -> mcp_http（endpoint=url, headers）；有 command -> mcp_stdio（command/args/env）；
// 两者皆无则跳过（不报错，便于兼容含纯声明条目的配置）。Name 取 mcpServers 的 key。
// 纯结构化解析，不执行任何命令；命令执行仍由 InstallMCPRuntime 经 G3 受控 spawn。
func ParseMCPConfig(raw []byte) ([]*MCPInstallRequest, error) {
	var cfg mcpDesktopConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析 MCP 配置失败: %w", err)
	}
	reqs := make([]*MCPInstallRequest, 0, len(cfg.McpServers))
	for name, srv := range cfg.McpServers {
		req := &MCPInstallRequest{Name: name}
		if srv.URL != "" {
			req.Transport = "mcp_http"
			req.Endpoint = srv.URL
			req.Headers = srv.Headers
		} else if srv.Command != "" {
			req.Transport = "mcp_stdio"
			req.Command = srv.Command
			req.Args = srv.Args
			req.Env = srv.Env
		} else {
			continue // 无 command 也无 url，跳过（不报错）
		}
		reqs = append(reqs, req)
	}
	return reqs, nil
}

// UnregisterDriver 注销驱动运行时
func (s *ModuleService) UnregisterDriver(driverID string) error {
	rt, err := s.repo.GetByDriverID(driverID)
	if err != nil {
		// 驱动不存在视为幂等成功；其余错误必须上抛，不再吞错
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("查询驱动 %s 失败: %w", driverID, err)
	}
	return s.registry.Unregister(rt.ID)
}

// ListDrivers 列出所有运行时（驱动已并入 SkillRuntime，管理后台直接展示运行时表）
func (s *ModuleService) ListDrivers() ([]*model.SkillRuntime, error) {
	runtimes, err := s.repo.List()
	if err != nil {
		return nil, err
	}
	return runtimes, nil
}

// ResolveDriver 根据驱动别名解析运行时
func (s *ModuleService) ResolveDriver(driverID string) (*model.SkillRuntime, error) {
	rt, err := s.repo.GetByDriverID(driverID)
	if err != nil {
		return nil, err
	}
	return rt, nil
}

// ResolveDriverByAuthToken 根据 auth_token 解析运行时（索引查询，替代全表线性扫描）
func (s *ModuleService) ResolveDriverByAuthToken(token string) (*model.SkillRuntime, error) {
	if token == "" {
		return nil, errors.New("auth_token 不能为空")
	}
	rt, err := s.repo.GetByAuthToken(token)
	if err != nil {
		return nil, errors.New("驱动不存在")
	}
	return rt, nil
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

// ===== transport/deployment 解析辅助 =====

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

// InstallFromCloudMeta 把云端拉取的 ModuleInstallMeta 安装到本地。
//
// P4 安装流程（见 docs/marketing/claw-implementation-plan.md §F.2）：
//   - official=true：直接激活本地预置（marketplace/ 已扫描注册），无需拉镜像。
//   - 第三方：ImageInstaller 拉镜像 + 签名校验 + 启动容器，再写入 registry 激活。
//
// applyCloudOrigin 把云端下发的来源属性写到本地运行时（type3：云端下载模块）。
// meta.Origin 非空时覆盖本地扫描值（云端为权威来源）；空则保留本地值。返回是否有变更。
// official 不写：据 origin 由 IsOfficial 推断（防伪造），InstallFromCloudMeta 不落 official 声明。
func applyCloudOrigin(rt *model.SkillRuntime, meta ModuleInstallMeta) bool {
	if meta.Origin == "" {
		return false
	}
	changed := string(rt.Origin) != meta.Origin || rt.Actor != meta.Actor
	rt.Origin = model.SkillRuntimeOrigin(meta.Origin)
	rt.Actor = meta.Actor
	return changed
}

// 返回安装后的 SkillRuntime（含 image/signature 元数据）。已安装同 module_id 视为幂等成功。
func (s *ModuleService) InstallFromCloudMeta(meta ModuleInstallMeta) (*model.SkillRuntime, error) {
	if s.registry == nil {
		return nil, errors.New("SkillRuntimeRegistry 未初始化")
	}

	var record *model.SkillRuntime
	if existing, err := s.repo.GetByID(meta.ModuleID); err == nil && existing != nil {
		// 幂等：校正云端下发的来源属性
		if applyCloudOrigin(existing, meta) {
			if err := s.repo.CreateOrUpdate(existing); err != nil {
				return nil, fmt.Errorf("持久化模块来源属性失败: %w", err)
			}
		}
		record = existing
	} else if meta.Official {
		// 官方模块：依赖 marketplace 扫描已注册
		rec, err := s.repo.GetByID(meta.ModuleID)
		if err != nil || rec == nil {
			return nil, fmt.Errorf("官方模块 %s 未在本地预置，请确认 marketplace/ 已包含", meta.ModuleID)
		}
		if applyCloudOrigin(rec, meta) {
			if err := s.repo.CreateOrUpdate(rec); err != nil {
				return nil, fmt.Errorf("持久化模块来源属性失败: %w", err)
			}
		}
		record = rec
	} else {
		// 第三方：拉镜像 + 签名 + 启动容器
		if s.installer == nil || s.installer.Runtime() == "" {
			return nil, fmt.Errorf("未检测到容器运行时（docker/podman），无法安装第三方模块 %s", meta.ModuleID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		rt, err := s.installer.Install(ctx, meta)
		if err != nil {
			return nil, err
		}
		applyCloudOrigin(rt, meta) // Register 持久化（含来源属性）
		if err := s.registry.Register(rt); err != nil {
			return nil, fmt.Errorf("注册模块到 registry 失败: %w", err)
		}
		record = rt
	}

	// 驱动绑定（official/第三方/幂等通用）：按 meta.DriverID 把别名绑到模块记录上
	if meta.DriverID != "" {
		if rt, err := s.repo.GetByID(meta.ModuleID); err == nil && rt != nil {
			changed := false
			if rt.DriverID != meta.DriverID {
				rt.DriverID = meta.DriverID
				changed = true
			}
			if meta.AuthToken != "" && rt.AuthToken != meta.AuthToken {
				rt.AuthToken = meta.AuthToken
				changed = true
			}
			if changed {
				if err := s.repo.CreateOrUpdate(rt); err != nil {
					return nil, fmt.Errorf("绑定驱动别名失败: %w", err)
				}
			}
		}
	}

	// 触发一次健康探测刷新状态
	record.Status = model.SkillRuntimeStatusInstalled
	if st := s.registry.ForceProbe(meta.ModuleID); st != nil {
		if st.Online {
			record.Status = model.SkillRuntimeStatusActive
		}
		record.Version = st.Version
		record.SetCapabilities(st.Capabilities)
	}
	// S2：云端下发了 manifest -> 云端 manifest 定名接管，关闭 auto_sku 并下架遗留派生 SKU。
	if rt, err := s.repo.GetByID(meta.ModuleID); err == nil && rt != nil {
		if err := s.applyCloudManifestSKUAuthority(meta, rt); err != nil {
			return nil, err
		}
	}
	return record, nil
}

// applyCloudManifestSKUAuthority 云端下发了 manifest 时，云端 manifest 即该模块 SKU 的权威来源：
//   - 关闭运行时 auto_sku：停止据 tools/list 自动派生（派生名取 MCP 工具名，与云端 manifest 定名不一致，
//     会导致本地卡片名与云端对不上）。
//   - 下架遗留派生 SKU：仅 manifest 标记 auto_sku_module == rt.ID 的自动派生项；手写 SKU 与云端 manifest
//     落库的 SKU 无此标记，保留。
//
// auto_sku 翻转以入参 rt（DB 副本，含调用方已应用的来源属性等变更）为准写库，避免用 registry 内存副本
// 覆盖那些变更；同时把内存副本（supervisor 持有同一指针）的 auto_sku 同步置 false，使其立即停止派生。
// 未注入 AgentRepo 或 meta 无 manifest 时跳过。
func (s *ModuleService) applyCloudManifestSKUAuthority(meta ModuleInstallMeta, rt *model.SkillRuntime) error {
	if s.agentRepo == nil || rt == nil || len(meta.Manifest) == 0 || string(meta.Manifest) == "null" {
		return nil
	}
	if rt.AutoSKU {
		rt.AutoSKU = false
		if err := s.repo.CreateOrUpdate(rt); err != nil {
			return fmt.Errorf("关闭 auto_sku 失败: %w", err)
		}
	}
	if inMem := s.registry.Get(rt.ID); inMem != nil && inMem != rt {
		inMem.AutoSKU = false // supervisor 持有同一指针，立即停止自动派生
	}
	// 下架本模块遗留的自动派生 SKU（auto_sku_module == rt.ID）；手写/云端 manifest SKU 无此标记，保留。
	existing, err := s.agentRepo.ListByModuleSKUs(rt.ID)
	if err != nil {
		return fmt.Errorf("查询模块 %s 现有 SKU 失败: %w", rt.ID, err)
	}
	for _, item := range existing {
		if item.Status == model.AgentStatusDelisted {
			continue
		}
		mf, _ := item.Manifest()
		if mf == nil || mf.Metadata["auto_sku_module"] != rt.ID {
			continue
		}
		if err := s.agentRepo.UpdateStatus(item.ID, model.AgentStatusDelisted); err != nil {
			return fmt.Errorf("下架遗留派生 SKU %s 失败: %w", item.ID, err)
		}
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

// EnsurePackageProvision 下载云端秘技包后为包内派生 SKU 补齐本地购买记录（T5.2）。
// D9「官方免费直下」的本地体现：官方包 auto_sku 派生 SKU 免费（PriceDanwan==0），下载即视为已领取，
// 使「一键激活全部」（ActivatePackageSKUs）可直接生效；付费 SKU（PriceDanwan>0）跳过，保持云端购买门禁
// （激活仍 ErrNotPurchased → 402 引导）。幂等：已购买跳过。仅收包归属 SKU（Metadata.package_module 精确匹配）。
func (s *ModuleService) EnsurePackageProvision(packageID, userID string) error {
	if s.agentRepo == nil || userID == "" {
		return nil
	}
	items, err := s.agentRepo.ListByModuleSKUs(packageID)
	if err != nil {
		return fmt.Errorf("枚举包 %s 派生 SKU 失败: %w", packageID, err)
	}
	for _, item := range items {
		if item.Status != model.AgentStatusApproved {
			continue
		}
		manifest, err := item.Manifest()
		if err != nil || manifest == nil || manifest.Metadata == nil {
			continue
		}
		if manifest.Metadata["package_module"] != packageID {
			continue // 前缀粗筛可能命中同名前缀的其他包 SKU，精确按 package_module 归属
		}
		if item.PriceDanwan > 0 {
			continue // 付费 SKU 不自动领取，激活仍走购买门禁
		}
		purchased, err := s.agentRepo.HasPurchased(item.ID, userID)
		if err != nil {
			continue
		}
		if purchased {
			continue
		}
		if err := s.agentRepo.CreatePurchase(&model.AgentPurchase{
			ID:              uuid.New().String(),
			AgentID:         item.ID,
			BuyerID:         userID,
			PricePaid:       0,
			Currency:        "cloud-purchased",
			CreatorEarnings: 0,
			PlatformFee:     0,
		}); err != nil {
			return fmt.Errorf("写入包 %s 秘技购买记录失败: %w", packageID, err)
		}
	}
	return nil
}
