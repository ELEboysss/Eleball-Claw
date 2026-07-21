package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/eleball/gateway/internal/model"
)

// moduleDriver 通用集市模块驱动
// 任何通过独立 Docker 模块运行的秘技（如 firecrawl / agent-reach）都可以使用此驱动。
// 目标模块 ID 通过 drivers 表中的驱动别名映射解析（或兼容 metadata.module），再由 ModuleRegistry 调用。
// 对于需要用户凭证的 SKU，凭证由 AgentCredentialService 按 (user_id, agent_id) 读取并注入请求。
type moduleDriver struct {
	registry          *ModuleRegistry
	credentialService *AgentCredentialService
}

// NewModuleDriver 创建通用模块驱动
func NewModuleDriver(registry *ModuleRegistry, credentialService *AgentCredentialService) ToolDriver {
	return &moduleDriver{registry: registry, credentialService: credentialService}
}

func (d *moduleDriver) Name() string {
	return string(model.ToolDriverModule)
}

func (d *moduleDriver) Schema() model.ToolManifest {
	return model.ToolManifest{
		ID:          "com.eleball.tools.module",
		Name:        "集市模块驱动",
		Description: "通过 Eleball 模块运行时调用独立部署的集市秘技模块。",
		Driver:      model.ToolDriverModule,
		Category:    "system",
		Level:       1,
		Permissions: []model.ToolPermission{model.ToolPermissionNetwork},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{"type": "string", "description": "模块 action"},
				"params": map[string]interface{}{"type": "object", "description": "模块参数"},
			},
			"required": []string{"action"},
		},
	}
}

func (d *moduleDriver) Execute(ctx context.Context, action string, params map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	if d.registry == nil {
		return nil, errors.New("模块注册表未初始化")
	}

	moduleID, _ := params["__module_id__"].(string)
	if moduleID == "" {
		return nil, errors.New("module driver 缺少 __module_id__")
	}

	// 校验模块在线状态
	status := d.registry.Check(moduleID)
	if status == nil || !status.Online {
		msg := fmt.Sprintf("%s 模块离线", moduleID)
		if status != nil && status.Error != "" {
			msg += ": " + status.Error
		}
		return ToolResult{
			Content:   "",
			Error:     msg,
			ErrorCode: "module_offline",
		}.ToMap(), nil
	}

	// 统一规范：模块 /execute 的 params 应为业务参数对象。
	// 为兼容不同 LLM 的调用习惯，同时支持两种传入方式：
	//   1. 推荐（单 action SKU）：arguments 直接为业务参数，如 {"url": "..."}
	//   2. 兼容（多 action SKU）：arguments 包含 action + params，如 {"action":"scrape","params":{"url":"..."}}
	moduleParams, _ := params["params"].(map[string]interface{})
	if moduleParams == nil {
		moduleParams = make(map[string]interface{})
	}

	// 若 LLM 把业务参数放在顶层，则透传到 moduleParams；
	// 已嵌套在 params 中的字段优先保留，避免被顶层同名字段覆盖。
	for k, v := range params {
		if k == "action" || k == "params" || k == "__module_id__" || k == "credentials" {
			continue
		}
		if _, exists := moduleParams[k]; !exists {
			moduleParams[k] = v
		}
	}

	// 移除内部字段，避免透传给模块
	cleanParams := make(map[string]interface{}, len(moduleParams))
	for k, v := range moduleParams {
		if k == "__module_id__" {
			continue
		}
		cleanParams[k] = v
	}

	// 注入用户为当前 SKU 配置的凭证
	if err := d.injectCredentials(cleanParams, env); err != nil {
		return ToolResult{
			Content:   "",
			Error:     err.Error(),
			ErrorCode: "module_credential_required",
		}.ToMap(), nil
	}

	result, err := d.registry.Execute(moduleID, action, cleanParams, env.UserID)
	if err != nil {
		return ToolResult{
			Content:   "",
			Error:     fmt.Sprintf("%s 模块调用失败: %v", moduleID, err),
			ErrorCode: "module_call_failed",
		}.ToMap(), nil
	}
	return result, nil
}

// injectCredentials 从 AgentCredentialService 读取当前用户、当前 SKU 的凭证，
// 校验必填项后注入 cleanParams["credentials"] 中供模块使用。
func (d *moduleDriver) injectCredentials(cleanParams map[string]interface{}, env *ToolEnv) error {
	if d.credentialService == nil || env.UserID == "" || env.AgentID == "" {
		return nil
	}

	// 没有声明凭证定义，直接跳过
	if len(env.Credentials) == 0 {
		return nil
	}

	// 校验必填凭证
	if err := d.credentialService.ValidateRequired(env.UserID, env.AgentID, env.Credentials); err != nil {
		return err
	}

	// 读取已保存的凭证
	values, err := d.credentialService.LoadForExecution(env.UserID, env.AgentID)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}

	// 合并到 params["credentials"]，保留模块可能已传入的其它凭证
	creds, ok := cleanParams["credentials"].(map[string]string)
	if !ok {
		creds = make(map[string]string)
	}
	for k, v := range values {
		creds[k] = v
	}
	cleanParams["credentials"] = creds
	return nil
}
