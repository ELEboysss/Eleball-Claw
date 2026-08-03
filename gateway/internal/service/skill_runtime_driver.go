package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/eleball/gateway/internal/model"
)

// SkillRuntimeDriver 统一秘技运行时驱动。
// 一个实例对应一个驱动别名（driver_id），执行时按别名或内部字段定位到 SkillRuntime，
// 再由 SkillRuntimeRegistry 按 transport 分发到 execute/raw_http/mcp_http/mcp_stdio。
type SkillRuntimeDriver struct {
	name              string
	registry          *SkillRuntimeRegistry
	credentialService *AgentCredentialService
}

// NewSkillRuntimeDriver 创建指定驱动别名的运行时驱动。
func NewSkillRuntimeDriver(name string, registry *SkillRuntimeRegistry, credentialService *AgentCredentialService) ToolDriver {
	return &SkillRuntimeDriver{
		name:              name,
		registry:          registry,
		credentialService: credentialService,
	}
}

// NewModuleDriver 保留旧名，实际返回统一 SkillRuntimeDriver（兼容既有测试与 manifest driver=module）。
func NewModuleDriver(registry *SkillRuntimeRegistry, credentialService *AgentCredentialService) ToolDriver {
	return NewSkillRuntimeDriver(string(model.ToolDriverModule), registry, credentialService)
}

// NewMCPDriver 保留旧名，实际返回统一 SkillRuntimeDriver（兼容既有 manifest driver=mcp）。
func NewMCPDriver(registry *SkillRuntimeRegistry, credentialService *AgentCredentialService) ToolDriver {
	return NewSkillRuntimeDriver(string(model.ToolDriverMCP), registry, credentialService)
}

func (d *SkillRuntimeDriver) Name() string {
	return d.name
}

func (d *SkillRuntimeDriver) Schema() model.ToolManifest {
	return model.ToolManifest{
		ID:          "com.eleball.tools.skill_runtime",
		Name:        "秘技运行时驱动",
		Description: "统一调用 SkillRuntime 暴露的工具能力，支持 execute/mcp_http/mcp_stdio/raw_http 多种传输协议。",
		Driver:      model.ToolDriverType(d.name),
		Category:    "system",
		Level:       1,
		Permissions: []model.ToolPermission{model.ToolPermissionNetwork},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{"type": "string", "description": "工具动作名"},
				"params": map[string]interface{}{"type": "object", "description": "工具业务参数"},
			},
			"required": []string{"action"},
		},
	}
}

func (d *SkillRuntimeDriver) Execute(ctx context.Context, action string, params map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	runtimeID, err := d.resolveRuntimeID(params)
	if err != nil {
		return ToolResult{Error: err.Error(), ErrorCode: "runtime_not_found"}.ToMap(), nil
	}

	// 校验运行时在线状态
	status := d.registry.Check(runtimeID)
	if status == nil || !status.Online {
		msg := fmt.Sprintf("%s 运行时离线", runtimeID)
		if status != nil && status.Error != "" {
			msg += ": " + status.Error
		}
		return ToolResult{Error: msg, ErrorCode: "runtime_offline"}.ToMap(), nil
	}

	rt := d.registry.Get(runtimeID)
	if rt == nil {
		return ToolResult{Error: "运行时记录不存在", ErrorCode: "runtime_not_found"}.ToMap(), nil
	}

	// 注入用户为该 SKU 配置的凭证
	if err := d.injectCredentials(params, env); err != nil {
		return ToolResult{Error: err.Error(), ErrorCode: "credential_required"}.ToMap(), nil
	}

	// MCP HTTP：把运行时配置中的 headers 模板用凭证替换后传入。
	if rt.IsMCP() {
		if err := d.prepareMCPHeaders(rt, params); err != nil {
			return ToolResult{Error: err.Error(), ErrorCode: "mcp_config_invalid"}.ToMap(), nil
		}
	}

	result, err := d.registry.Execute(runtimeID, action, params, env.UserID)
	if err != nil {
		code := "runtime_call_failed"
		msg := fmt.Sprintf("%s 调用失败: %v", runtimeID, err)
		if result != nil {
			if c, ok := result["error_code"].(string); ok && c != "" {
				code = c
			}
			if m, ok := result["error_message"].(string); ok && m != "" {
				msg = fmt.Sprintf("%s: %s", runtimeID, m)
			} else if detail, ok := result["detail"].(string); ok && detail != "" {
				msg = fmt.Sprintf("%s: %s", runtimeID, detail)
			}
		}
		return ToolResult{Error: msg, ErrorCode: code}.ToMap(), nil
	}
	return result, nil
}

// resolveRuntimeID 解析目标运行时 ID。
// 优先级：显式 __runtime_id__ > __module_id__ > 驱动别名匹配 > metadata.module。
func (d *SkillRuntimeDriver) resolveRuntimeID(params map[string]interface{}) (string, error) {
	if id, ok := params["__runtime_id__"].(string); ok && id != "" {
		return id, nil
	}
	if id, ok := params["__module_id__"].(string); ok && id != "" {
		return id, nil
	}

	// 驱动别名直接对应 SkillRuntime.DriverID
	if d.registry != nil && d.name != "" {
		if rt := d.registry.GetByDriverID(d.name); rt != nil {
			return rt.ID, nil
		}
		// 也允许 driver 别名直接就是 runtime ID
		if rt := d.registry.Get(d.name); rt != nil {
			return rt.ID, nil
		}
	}

	return "", errors.New("未找到目标 SkillRuntime")
}

// injectCredentials 从 AgentCredentialService 读取当前用户、当前 SKU 的凭证并注入 params["credentials"]。
func (d *SkillRuntimeDriver) injectCredentials(params map[string]interface{}, env *ToolEnv) error {
	if d.credentialService == nil || env.UserID == "" || env.AgentID == "" {
		return nil
	}
	if len(env.Credentials) == 0 {
		return nil
	}
	if err := d.credentialService.ValidateRequired(env.UserID, env.AgentID, env.Credentials); err != nil {
		return err
	}
	values, err := d.credentialService.LoadForExecution(env.UserID, env.AgentID)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}

	creds, ok := params["credentials"].(map[string]string)
	if !ok {
		creds = make(map[string]string)
	}
	for k, v := range values {
		creds[k] = v
	}
	params["credentials"] = creds
	return nil
}

// prepareMCPHeaders 用已注入的 credentials 替换 MCP headers 模板，并写入 params["__mcp_headers__"]。
func (d *SkillRuntimeDriver) prepareMCPHeaders(rt *model.SkillRuntime, params map[string]interface{}) error {
	cfg := rt.GetMCPServerConfig()
	if cfg == nil || len(cfg.Headers) == 0 {
		return nil
	}

	credentials := make(map[string]string)
	if c, ok := params["credentials"].(map[string]string); ok {
		credentials = c
	}

	headers := make(map[string]string, len(cfg.Headers))
	for k, v := range cfg.Headers {
		headers[k] = substituteCredentialPlaceholders(v, credentials)
	}
	params["__mcp_headers__"] = headers
	return nil
}

// credentialPlaceholderRe 匹配 header 模板中的 ${credentials.KEY}。
var credentialPlaceholderRe = regexp.MustCompile(`\$\{credentials\.([^}]+)\}`)

// substituteCredentialPlaceholders 将 ${credentials.KEY} 替换为对应凭证值。
func substituteCredentialPlaceholders(value string, credentials map[string]string) string {
	return credentialPlaceholderRe.ReplaceAllStringFunc(value, func(match string) string {
		matches := credentialPlaceholderRe.FindStringSubmatch(match)
		if len(matches) < 2 {
			return ""
		}
		if v, ok := credentials[matches[1]]; ok {
			return v
		}
		return ""
	})
}

// SkillRuntimeRegistry.GetByDriverID 的本地快捷方法，避免 nil 检查重复。
func (r *SkillRuntimeRegistry) GetByDriverID(driverID string) *model.SkillRuntime {
	if r == nil || r.runtimeRepo == nil {
		return nil
	}
	rt, err := r.runtimeRepo.GetByDriverID(driverID)
	if err != nil {
		return nil
	}
	return rt
}
