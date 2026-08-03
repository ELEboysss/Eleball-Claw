package model

import (
	"encoding/json"
	"time"
)

// SkillRuntimeSource 运行时来源
type SkillRuntimeSource string

const (
	SkillRuntimeSourceBuiltin    SkillRuntimeSource = "builtin"    // 网关内置
	SkillRuntimeSourceMarketplace SkillRuntimeSource = "marketplace" // 集市模块
	SkillRuntimeSourceUserLocal  SkillRuntimeSource = "user_local"  // 用户本地
	SkillRuntimeSourceMCPRemote  SkillRuntimeSource = "mcp_remote"  // 远端 MCP
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
	ID             string                 `gorm:"primaryKey" json:"id"`
	Name           string                 `gorm:"not null" json:"name"`
	Description    string                 `json:"description"`
	Source         SkillRuntimeSource     `gorm:"default:marketplace" json:"source"`
	Transport      SkillRuntimeTransport  `gorm:"not null" json:"transport"`
	Deployment     SkillRuntimeDeployment `gorm:"not null" json:"deployment"`
	Endpoint       string                 `json:"endpoint,omitempty"`        // HTTP 类 transport 连接地址
	Command        string                 `json:"command,omitempty"`         // process/stdio 启动命令
	Args           string                 `json:"args,omitempty"`            // JSON array
	Env            string                 `json:"env,omitempty"`             // JSON map
	WorkDir        string                 `json:"work_dir,omitempty"`        // 工作目录
	DockerComposePath string              `json:"docker_compose_path,omitempty"`
	ImageRef       string                 `json:"image_ref,omitempty"`
	ImageDigest    string                 `json:"image_digest,omitempty"`
	Signature      string                 `json:"signature,omitempty"`
	Capabilities   string                 `json:"capabilities"`              // JSON ["search", ...]
	Version        string                 `json:"version"`
	AuthToken      string                 `json:"auth_token,omitempty"`
	Official       bool                   `gorm:"default:false" json:"official"`
	// DriverID 该运行时对外暴露的驱动别名，SKU manifest 的 driver 字段与此对应。
	DriverID       string                 `gorm:"index:idx_skill_runtime_driver_id" json:"driver_id,omitempty"`
	// MCPServerConfig MCP HTTP 服务器配置（JSON），transport=mcp_http 时必填。
	MCPServerConfig string                `json:"mcp_server_config,omitempty"`
	Status         SkillRuntimeStatus     `gorm:"default:offline" json:"status"`
	LastHeartbeat  *time.Time             `json:"last_heartbeat,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
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
