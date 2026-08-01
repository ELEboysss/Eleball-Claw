package model

import (
	"time"

	"github.com/google/uuid"
)

// GenerateDriverAuthToken 生成一个驱动自助注册令牌。
func GenerateDriverAuthToken() string {
	return "drv_" + uuid.New().String()
}

// MCPServerConfig MCP 服务器配置（一期仅支持 Streamable HTTP）
// 预留 command/args/env 字段用于后续 stdio 传输，本期不实现。
type MCPServerConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"` // stdio 预留
	Args    []string          `json:"args,omitempty"`    // stdio 预留
	Env     map[string]string `json:"env,omitempty"`     // stdio 预留
}

// DriverRecord 动态驱动注册记录
// 驱动并不承载可执行代码，而是声明 SKU 的 driver 字段如何映射到运行时：
//   - transport_type=module     → 调用对应 ModuleRecord 的 /execute
//   - transport_type=remote_url → 调用指定 HTTP endpoint
//   - transport_type=mcp        → 调用 Streamable HTTP JSON-RPC tools/call
//
// 对于第三方模块，auth_token 绑定在驱动别名上；开发者凭此 token 自助注册模块服务后，
// 网关自动将该模块 ID 回写到本记录的 module_id 字段。
//
// 注意：此处的 transport_type 描述的是「网关如何与运行时通信」，
// 与 ToolManifest 中的 runtime_type（builtin/wasm/sidecar/remote 运行时层级）含义不同。
type DriverRecord struct {
	ID              string           `gorm:"primaryKey" json:"driver_id"`
	Name            string           `gorm:"not null" json:"name"`
	Description     string           `json:"description"`
	TransportType   string           `gorm:"not null" json:"transport_type"` // module / remote_url / mcp
	ModuleID        string           `json:"module_id,omitempty"`            // transport_type=module 时指向模块
	Endpoint        string           `json:"endpoint,omitempty"`             // transport_type=remote_url 时必填
	MCPServerConfig *MCPServerConfig `gorm:"serializer:json" json:"mcp_server_config,omitempty"` // transport_type=mcp 时必填
	AuthToken       string           `gorm:"index:idx_drivers_auth_token" json:"auth_token,omitempty"` // 开发者自助注册令牌；空字符串可重复，非空需业务层保证唯一
	SchemaJSON      string           `json:"schema_json,omitempty"`          // 驱动默认 schema（可选）
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// DriverRegisterRequest 驱动注册请求
type DriverRegisterRequest struct {
	ID              string           `json:"driver_id" binding:"required"`
	Name            string           `json:"name" binding:"required"`
	Description     string           `json:"description"`
	TransportType   string           `json:"transport_type" binding:"required,oneof=module remote_url mcp"`
	ModuleID        string           `json:"module_id"`
	Endpoint        string           `json:"endpoint"`
	MCPServerConfig *MCPServerConfig `json:"mcp_server_config,omitempty"`
	AuthToken       string           `json:"auth_token"`
	SchemaJSON      string           `json:"schema_json"`
}
