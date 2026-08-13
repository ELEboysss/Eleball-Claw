package model

import (
	"encoding/json"
	"time"
)

// SkillRuntimeSource 运行时来源
type SkillRuntimeSource string

const (
	SkillRuntimeSourceBuiltin     SkillRuntimeSource = "builtin"     // 网关内置
	SkillRuntimeSourceMarketplace SkillRuntimeSource = "marketplace" // 集市模块
	SkillRuntimeSourceUserLocal   SkillRuntimeSource = "user_local"  // 用户本地
	SkillRuntimeSourceMCPRemote   SkillRuntimeSource = "mcp_remote"  // 远端 MCP
)

// SkillRuntimeOrigin 模块来源（provenance），刻画「谁提供的」。
// 与 Source（运行时分类 builtin/marketplace/user_local/mcp_remote）正交：Source 区分运行时形态，
// Origin 区分来源主体。cloud=云端市场（cloud 侧扫描默认 / claw 从云端下载）、builtin=随 claw 内置、
// user=本地创作或安装（/studio 脚本造秘技 / MCP 安装，actor=作者名）。
type SkillRuntimeOrigin string

const (
	// SkillRuntimeOriginBuiltin eleball 内置：随 claw 仓库分发（如 search-web）。
	SkillRuntimeOriginBuiltin SkillRuntimeOrigin = "builtin"
	// SkillRuntimeOriginCloud eleball 云端市场：cloud 侧扫描默认 / claw 从云端下载。
	SkillRuntimeOriginCloud SkillRuntimeOrigin = "cloud"
	// SkillRuntimeOriginUser 用户本地创作/安装：type4a /studio 脚本（actor=用户名）
	// + type4b MCP 安装（actor=MCP 名）。
	SkillRuntimeOriginUser SkillRuntimeOrigin = "user"
)

// OfficialModuleIDs eleball 官方维护的云端模块 ID。cloud 来源中仅这些为官方（免费直接下载，D9）；
// builtin 恒官方；user 一律非官方。package.json/module.json 不声明 official（防伪造），一律由 IsOfficial 推断。
var OfficialModuleIDs = map[string]bool{
	"agent-reach": true,
	"firecrawl":   true,
	"mcp-hello":   true,
}

// IsOfficial 据 origin + moduleID 推断是否官方模块（official 不落库声明，防伪造）：
// builtin 恒官方；cloud 仅官方维护列表内为官方；user 一律非官方。
func (o SkillRuntimeOrigin) IsOfficial(moduleID string) bool {
	switch o {
	case SkillRuntimeOriginBuiltin:
		return true
	case SkillRuntimeOriginCloud:
		return OfficialModuleIDs[moduleID]
	default:
		return false
	}
}

// SkillRuntimeTransport 通信协议
type SkillRuntimeTransport string

const (
	// SkillRuntimeTransportExecute Eleball 标准 HTTP /execute 协议
	SkillRuntimeTransportExecute SkillRuntimeTransport = "execute"
	// SkillRuntimeTransportMCPHTTP Streamable HTTP JSON-RPC MCP
	SkillRuntimeTransportMCPHTTP SkillRuntimeTransport = "mcp_http"
	// SkillRuntimeTransportMCPStdio stdio JSON-RPC MCP
	SkillRuntimeTransportMCPStdio SkillRuntimeTransport = "mcp_stdio"
	// SkillRuntimeTransportMCPSSE HTTP+SSE JSON-RPC MCP（T2.5：仅声明传输常量，客户端后置未实现；
	// 不并入 IsMCP() 与探活分发，避免提前把 SSE runtime 路由进 MCP 路径）
	SkillRuntimeTransportMCPSSE SkillRuntimeTransport = "mcp_sse"
	// SkillRuntimeTransportRawHTTP 直接 HTTP POST（原 remote_url）
	SkillRuntimeTransportRawHTTP SkillRuntimeTransport = "raw_http"
)

// SkillRuntimeDeployment 启动/部署方式
type SkillRuntimeDeployment string

const (
	// SkillRuntimeDeploymentNone 已在线，网关只连接不管理
	SkillRuntimeDeploymentNone SkillRuntimeDeployment = "none"
	// SkillRuntimeDeploymentProcess 本地子进程（claw 专用/云端受控 sidecar）
	SkillRuntimeDeploymentProcess SkillRuntimeDeployment = "process"
	// SkillRuntimeDeploymentDocker 容器部署
	SkillRuntimeDeploymentDocker SkillRuntimeDeployment = "docker"
	// SkillRuntimeDeploymentExternal 远端 SaaS，只注册 endpoint
	SkillRuntimeDeploymentExternal SkillRuntimeDeployment = "external"
)

// SkillRuntimeStatus 运行时状态（T1.4 统一状态机，单一 status 字段）
// 完整状态：not_installed / installed / activating / active / degraded / needs_update / disabled。
// purchased 是否已购是独立布尔（InstallStatus），不占用本枚举。
type SkillRuntimeStatus string

const (
	// SkillRuntimeStatusNotInstalled 未安装（catalog 中存在但本地未下载）
	SkillRuntimeStatusNotInstalled SkillRuntimeStatus = "not_installed"
	// SkillRuntimeStatusInstalled 已安装未运行
	SkillRuntimeStatusInstalled SkillRuntimeStatus = "installed"
	// SkillRuntimeStatusActivating 拉起中（异步启动未回写）
	SkillRuntimeStatusActivating SkillRuntimeStatus = "activating"
	// SkillRuntimeStatusActive 运行中
	SkillRuntimeStatusActive SkillRuntimeStatus = "active"
	// SkillRuntimeStatusDegraded 拉起重试耗尽或运行异常
	SkillRuntimeStatusDegraded SkillRuntimeStatus = "degraded"
	// SkillRuntimeStatusNeedsUpdate 检测到新版本待更新
	SkillRuntimeStatusNeedsUpdate SkillRuntimeStatus = "needs_update"
	// SkillRuntimeStatusDisabled 手动禁用
	SkillRuntimeStatusDisabled SkillRuntimeStatus = "disabled"
)

// 旧枚举值兼容别名（T1.4 状态机重构：值改名后映射到新枚举）。
// 新代码请直接用新常量；旧常量仅保证存量调用点在新语义下编译通过。
const (
	SkillRuntimeStatusOnline   = SkillRuntimeStatusActive    // 旧 "online"  -> active
	SkillRuntimeStatusOffline  = SkillRuntimeStatusInstalled // 旧 "offline" -> installed
	SkillRuntimeStatusStarting = SkillRuntimeStatusActivating // 旧 "starting" -> activating
	SkillRuntimeStatusError    = SkillRuntimeStatusDegraded   // 旧 "error"   -> degraded
)

// SkillRuntime 统一秘技运行时记录
// 所有可执行能力（模块/MCP/脚本/远端服务）的抽象模型。
type SkillRuntime struct {
	ID          string             `gorm:"primaryKey" json:"id"`
	Name        string             `gorm:"not null" json:"name"`
	Description string             `json:"description"`
	Source      SkillRuntimeSource `gorm:"default:marketplace" json:"source"`
	// Origin 模块来源（builtin/cloud/user），见 SkillRuntimeOrigin。
	// 集市扫描默认 cloud（云端）；claw 内置模块扫描默认 builtin；用户造模块/MCP 安装=user。
	Origin SkillRuntimeOrigin `gorm:"default:cloud" json:"origin"`
	// Actor 来源主体：user 时为作者（用户名 / MCP 名）；builtin/cloud（eleball 维护）为空。
	Actor             string                 `json:"actor,omitempty"`
	Transport         SkillRuntimeTransport  `gorm:"not null" json:"transport"`
	Deployment        SkillRuntimeDeployment `gorm:"not null" json:"deployment"`
	Endpoint          string                 `json:"endpoint,omitempty"` // HTTP 类 transport 连接地址
	Command           string                 `json:"command,omitempty"`  // process/stdio 启动命令
	Args              string                 `json:"args,omitempty"`     // JSON array
	Env               string                 `json:"env,omitempty"`      // JSON map
	WorkDir           string                 `json:"work_dir,omitempty"` // 工作目录
	DockerComposePath string                 `json:"docker_compose_path,omitempty"`
	ImageRef          string                 `json:"image_ref,omitempty"`
	ImageDigest       string                 `json:"image_digest,omitempty"`
	Signature         string                 `json:"signature,omitempty"`
	Capabilities      string                 `json:"capabilities"` // JSON ["search", ...]
	Version           string                 `json:"version"`
	AuthToken         string                 `gorm:"index:idx_skill_runtime_auth_token" json:"auth_token,omitempty"`
	Official          bool                   `gorm:"default:false" json:"official"`
	// AutoSKU 是否据 tools/list 自动派生可购买 SKU（默认 false，保护手写 SKU 模块）。
	// 为 true 时，supervisor/探活成功后由 SkillRuntimeSKUService 合成并同步 AgentItem+ToolManifest，
	// 免去在 marketplace/<mod>/skus/ 下手写 SKU 文件。stdio 模块凭证须 scope=module。
	AutoSKU bool `gorm:"default:false" json:"auto_sku,omitempty"`
	// DriverID 该运行时对外暴露的驱动别名，SKU manifest 的 driver 字段与此对应。
	DriverID string `gorm:"index:idx_skill_runtime_driver_id" json:"driver_id,omitempty"`
	// MCPServerConfig MCP HTTP 服务器配置（JSON），transport=mcp_http 时必填。
	MCPServerConfig string `json:"mcp_server_config,omitempty"`
	// Credentials 凭证声明（JSON map[string]CredentialDef）。auto_sku 模块从 module.json 透传，
	// 派生 SKU 时复制进 ToolManifest.Credentials 供 web 提示用户填写；env 模板 ${credentials.KEY} 引用同名 key。
	Credentials string `json:"credentials,omitempty"`
	// AllowedTools / DisallowedTools 工具白/黑名单（JSON []string，工具名）。
	// allowed_tools 非空时仅保留白名单内工具；disallowed_tools 始终排除（黑名单优先）。
	// 探活时过滤 tools/list，DeriveSKUs 只为允许的工具出 SKU（G2，对标 openhuman apply_safety_filter）。
	AllowedTools    string             `json:"allowed_tools,omitempty"`
	DisallowedTools string             `json:"disallowed_tools,omitempty"`
	Status          SkillRuntimeStatus `gorm:"default:installed" json:"status"`
	LastHeartbeat   *time.Time         `json:"last_heartbeat,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

// ArgsList 解析 args JSON
func (r *SkillRuntime) ArgsList() []string {
	if r.Args == "" {
		return nil
	}
	var args []string
	_ = json.Unmarshal([]byte(r.Args), &args)
	return args
}

// SetArgs 序列化 args
func (r *SkillRuntime) SetArgs(args []string) {
	if len(args) == 0 {
		r.Args = "[]"
		return
	}
	b, _ := json.Marshal(args)
	r.Args = string(b)
}

// EnvMap 解析 env JSON
func (r *SkillRuntime) EnvMap() map[string]string {
	if r.Env == "" {
		return nil
	}
	var env map[string]string
	_ = json.Unmarshal([]byte(r.Env), &env)
	return env
}

// SetEnv 序列化 env
func (r *SkillRuntime) SetEnv(env map[string]string) {
	if len(env) == 0 {
		r.Env = "{}"
		return
	}
	b, _ := json.Marshal(env)
	r.Env = string(b)
}

// CapabilitiesList 解析 capabilities JSON
func (r *SkillRuntime) CapabilitiesList() []string {
	if r.Capabilities == "" {
		return nil
	}
	var caps []string
	_ = json.Unmarshal([]byte(r.Capabilities), &caps)
	return caps
}

// SetCapabilities 序列化 capabilities
func (r *SkillRuntime) SetCapabilities(caps []string) {
	if len(caps) == 0 {
		r.Capabilities = "[]"
		return
	}
	b, _ := json.Marshal(caps)
	r.Capabilities = string(b)
}

// GetMCPServerConfig 解析 MCP 服务器配置
func (r *SkillRuntime) GetMCPServerConfig() *MCPServerConfig {
	if r.MCPServerConfig == "" {
		return nil
	}
	var cfg MCPServerConfig
	_ = json.Unmarshal([]byte(r.MCPServerConfig), &cfg)
	return &cfg
}

// SetMCPServerConfig 序列化 MCP 服务器配置
func (r *SkillRuntime) SetMCPServerConfig(cfg *MCPServerConfig) {
	if cfg == nil {
		r.MCPServerConfig = ""
		return
	}
	b, _ := json.Marshal(cfg)
	r.MCPServerConfig = string(b)
}

// CredentialsMap 解析 credentials 声明 JSON
func (r *SkillRuntime) CredentialsMap() map[string]CredentialDef {
	if r.Credentials == "" {
		return nil
	}
	var creds map[string]CredentialDef
	_ = json.Unmarshal([]byte(r.Credentials), &creds)
	return creds
}

// AllowedToolsList 解析 allowed_tools 白名单 JSON
func (r *SkillRuntime) AllowedToolsList() []string {
	if r.AllowedTools == "" {
		return nil
	}
	var names []string
	_ = json.Unmarshal([]byte(r.AllowedTools), &names)
	return names
}

// SetAllowedTools 序列化 allowed_tools 白名单
func (r *SkillRuntime) SetAllowedTools(names []string) {
	if len(names) == 0 {
		r.AllowedTools = ""
		return
	}
	b, _ := json.Marshal(names)
	r.AllowedTools = string(b)
}

// DisallowedToolsList 解析 disallowed_tools 黑名单 JSON
func (r *SkillRuntime) DisallowedToolsList() []string {
	if r.DisallowedTools == "" {
		return nil
	}
	var names []string
	_ = json.Unmarshal([]byte(r.DisallowedTools), &names)
	return names
}

// SetDisallowedTools 序列化 disallowed_tools 黑名单
func (r *SkillRuntime) SetDisallowedTools(names []string) {
	if len(names) == 0 {
		r.DisallowedTools = ""
		return
	}
	b, _ := json.Marshal(names)
	r.DisallowedTools = string(b)
}

// SetCredentials 序列化 credentials 声明
func (r *SkillRuntime) SetCredentials(creds map[string]CredentialDef) {
	if len(creds) == 0 {
		r.Credentials = ""
		return
	}
	b, _ := json.Marshal(creds)
	r.Credentials = string(b)
}

// IsMCP 是否 MCP transport
func (r *SkillRuntime) IsMCP() bool {
	return r.Transport == SkillRuntimeTransportMCPHTTP || r.Transport == SkillRuntimeTransportMCPStdio
}

// IsHTTP 是否 HTTP 类 transport
func (r *SkillRuntime) IsHTTP() bool {
	return r.Transport == SkillRuntimeTransportExecute ||
		r.Transport == SkillRuntimeTransportMCPHTTP ||
		r.Transport == SkillRuntimeTransportRawHTTP
}
