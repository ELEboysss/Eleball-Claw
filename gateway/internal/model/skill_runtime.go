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

// SkillRuntimeSourceOrigin 模块来源属性（provenance），刻画 todo 四类模块「谁提供的」。
// 与 Source（运行时分类 builtin/marketplace/user_local/mcp_remote）正交：Source 区分运行时形态，
// SourceOrigin 区分来源主体。集市扫描默认 cloud=eleball_cloud / claw=eleball_builtin；
// 用户造模块=user（actor=用户名）、MCP 安装=mcp（actor=MCP 名）、云端下载=eleball_cloud。
type SkillRuntimeSourceOrigin string

const (
	// SkillRuntimeOriginEleballCloud eleball 云端：type1（云端运行）+ type3（claw 从云端下载）。
	SkillRuntimeOriginEleballCloud SkillRuntimeSourceOrigin = "eleball_cloud"
	// SkillRuntimeOriginEleballBuiltin eleball 内置：type2（claw 仓库预置）。
	SkillRuntimeOriginEleballBuiltin SkillRuntimeSourceOrigin = "eleball_builtin"
	// SkillRuntimeOriginUser 用户经 /studio 脚本造秘技：type4a（actor=用户名）。
	SkillRuntimeOriginUser SkillRuntimeSourceOrigin = "user"
	// SkillRuntimeOriginMCP 用户 MCP 安装：type4b（actor=MCP 名）。
	SkillRuntimeOriginMCP SkillRuntimeSourceOrigin = "mcp"
)

// SkillRuntimeTransport 通信协议
type SkillRuntimeTransport string

const (
	// SkillRuntimeTransportExecute Eleball 标准 HTTP /execute 协议
	SkillRuntimeTransportExecute SkillRuntimeTransport = "execute"
	// SkillRuntimeTransportMCPHTTP Streamable HTTP JSON-RPC MCP
	SkillRuntimeTransportMCPHTTP SkillRuntimeTransport = "mcp_http"
	// SkillRuntimeTransportMCPStdio stdio JSON-RPC MCP
	SkillRuntimeTransportMCPStdio SkillRuntimeTransport = "mcp_stdio"
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

// SkillRuntimeStatus 运行时状态
type SkillRuntimeStatus string

const (
	SkillRuntimeStatusOnline   SkillRuntimeStatus = "online"
	SkillRuntimeStatusOffline  SkillRuntimeStatus = "offline"
	SkillRuntimeStatusStarting SkillRuntimeStatus = "starting"
	SkillRuntimeStatusError    SkillRuntimeStatus = "error"
	SkillRuntimeStatusDisabled SkillRuntimeStatus = "disabled"
)

// SkillRuntime 统一秘技运行时记录
// 所有可执行能力（模块/MCP/脚本/远端服务）的抽象模型。
type SkillRuntime struct {
	ID          string             `gorm:"primaryKey" json:"id"`
	Name        string             `gorm:"not null" json:"name"`
	Description string             `json:"description"`
	Source      SkillRuntimeSource `gorm:"default:marketplace" json:"source"`
	// SourceOrigin 模块来源属性（eleball_cloud/eleball_builtin/user/mcp），见 SkillRuntimeSourceOrigin。
	// 集市扫描默认 eleball_builtin（claw 内置）；云端下载=eleball_cloud、用户造模块=user、MCP 安装=mcp 由各写入点显式设置。
	SourceOrigin SkillRuntimeSourceOrigin `gorm:"default:eleball_builtin" json:"source_origin,omitempty"`
	// SourceActor 来源主体：user 时为用户名、mcp 时为 MCP 名；eleball_* 为空。
	SourceActor       string                 `json:"source_actor,omitempty"`
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
	AuthToken         string                 `json:"auth_token,omitempty"`
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
	Status          SkillRuntimeStatus `gorm:"default:offline" json:"status"`
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
