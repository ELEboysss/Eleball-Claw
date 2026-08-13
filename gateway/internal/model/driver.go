package model

import (
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
