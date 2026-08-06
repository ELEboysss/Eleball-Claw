package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
)

// AgentToolLoader 将用户购买的 AgentItem（ToolManifest 载体）加载为动态工具
type AgentToolLoader struct {
	agentRepo      *repository.AgentRepo
	driverRegistry *ToolDriverRegistry
	moduleService  *ModuleService
}

// NewAgentToolLoader 创建动态工具加载器
func NewAgentToolLoader(agentRepo *repository.AgentRepo, driverRegistry *ToolDriverRegistry, moduleRegistry *SkillRuntimeRegistry) *AgentToolLoader {
	return &AgentToolLoader{
		agentRepo:      agentRepo,
		driverRegistry: driverRegistry,
	}
}

// SetModuleService 设置模块业务服务，用于动态驱动解析
func (l *AgentToolLoader) SetModuleService(svc *ModuleService) {
	l.moduleService = svc
}

// LoadToolsForUser 加载某用户已购买且 approved 的可执行秘技为 Tool 列表
func (l *AgentToolLoader) LoadToolsForUser(ctx context.Context, userID string) ([]*Tool, error) {
	items, err := l.agentRepo.ListPurchasedExecutableTools(userID)
	if err != nil {
		return nil, err
	}
	return l.buildTools(items), nil
}

// LoadToolsForUserFiltered 同上，但仅加载指定秘技 ID 集合内的工具（助手组合过滤用）。
// 空集合直接返回空工具列表（不注入动态工具，不报错）。
func (l *AgentToolLoader) LoadToolsForUserFiltered(ctx context.Context, userID string, agentIDs []string) ([]*Tool, error) {
	if len(agentIDs) == 0 {
		return []*Tool{}, nil
	}
	items, err := l.agentRepo.ListPurchasedExecutableToolsFiltered(userID, agentIDs)
	if err != nil {
		return nil, err
	}
	return l.buildTools(items), nil
}

// buildTools 将秘技条目转换为 Tool 列表（跳过未上架/模块离线/manifest 解析失败的项）
func (l *AgentToolLoader) buildTools(items []*model.AgentItem) []*Tool {
	tools := make([]*Tool, 0, len(items))
	for _, item := range items {
		if item.Status != model.AgentStatusApproved {
			continue
		}
		// 若该秘技声明了依赖模块，仅当模块在线时才载入工具
		if l.moduleService != nil {
			manifest, _ := item.Manifest()
			moduleID := l.ResolveModuleID(manifest)
			if moduleID != "" {
				status := l.moduleService.CheckRuntime(moduleID)
				if status == nil || !status.Online {
					// 模块离线：跳过该工具，不暴露给大模型
					continue
				}
			}
		}
		tool, err := l.buildTool(item)
		if err != nil {
			// 单个 manifest 解析失败不应影响其他工具
			continue
		}
		if tool != nil {
			tools = append(tools, tool)
		}
	}
	return tools
}

// buildTool 将 AgentItem 转换为 Tool
func (l *AgentToolLoader) buildTool(item *model.AgentItem) (*Tool, error) {
	manifest, err := item.Manifest()
	if err != nil {
		return nil, fmt.Errorf("解析 manifest 失败: %w", err)
	}
	if manifest == nil {
		return nil, nil
	}
	if manifest.Driver == model.ToolDriverNone {
		return nil, nil
	}

	toolName := sanitizeToolName(manifest.ID)
	if toolName == "" {
		toolName = fmt.Sprintf("Agent_%s", sanitizeToolName(item.ID))
	}

	parameters := manifest.Parameters
	if parameters == nil {
		parameters = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	// 描述拼接：item 描述 + manifest 描述 + driver 信息
	description := item.Description
	if description == "" {
		description = manifest.Description
	}
	if manifest.Driver != model.ToolDriverBuiltin {
		description = fmt.Sprintf("%s [driver=%s]", description, manifest.Driver)
	}

	isModuleDriver := manifest.Driver == model.ToolDriverModule || l.isDynamicModuleDriver(string(manifest.Driver))
	serverSide := isModuleDriver
	for _, p := range manifest.Permissions {
		if p == model.ToolPermissionFileTools ||
			p == model.ToolPermissionShell ||
			p == model.ToolPermissionAgentReach ||
			p == model.ToolPermissionNetwork {
			serverSide = true
			break
		}
	}

	return &Tool{
		Name:        toolName,
		Description: description,
		Parameters:  parameters,
		Func:        l.buildToolFunc(manifest, item.ID),
		ServerSide:  serverSide,
		Manifest:    manifest,
		Driver:      string(manifest.Driver),
		AgentID:     item.ID,
		Credentials: manifest.Credentials,
	}, nil
}

// buildToolFunc 根据 manifest.Driver 构造 ToolFunc
func (l *AgentToolLoader) buildToolFunc(manifest *model.ToolManifest, agentID string) ToolFunc {
	driverName := string(manifest.Driver)
	timeout := manifest.TimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}

	return func(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
		// 把当前 SKU 的 ID 与凭证定义写入执行环境，供驱动注入/校验
		env.AgentID = agentID
		env.Credentials = manifest.Credentials

		driver, dynRec, ok := l.resolveDriver(driverName)
		if !ok {
			return nil, fmt.Errorf("驱动未注册: %s", driverName)
		}

		ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()

		// 从 input 中提取 action，优先取 action 字段，否则取第一个 action 名
		action, _ := input["action"].(string)
		if action == "" && len(manifest.Actions) > 0 {
			action = manifest.Actions[0].Name
		}

		// 对于 remote_url 驱动，需要把 endpoint 注入 input
		if manifest.Driver == model.ToolDriverRemoteURL || (dynRec != nil && dynRec.TransportType == string(model.ModuleTransportTypeRemoteURL)) {
			endpoint := manifest.Metadata["endpoint"]
			if dynRec != nil && dynRec.Endpoint != "" {
				endpoint = dynRec.Endpoint
			}
			if endpoint != "" {
				input["__endpoint__"] = endpoint
			}
		}

		// 对于 builtin 驱动，需要把具体工具名注入 input
		if manifest.Driver == model.ToolDriverBuiltin {
			if toolName, ok := manifest.Metadata["builtin_tool"]; ok && toolName != "" {
				input["tool"] = toolName
			}
		}

		// 对于通用模块驱动，通过动态驱动记录（driver 别名）或 metadata.module 指定目标模块
		if manifest.Driver == model.ToolDriverModule || (dynRec != nil && dynRec.TransportType == string(model.ModuleTransportTypeModule)) {
			moduleID := manifest.Metadata["module"]
			if dynRec != nil && dynRec.ModuleID != "" {
				moduleID = dynRec.ModuleID
			}
			if moduleID != "" {
				input["__module_id__"] = moduleID
			}
			// SKU 封装的固定参数（如 search-web baidu SKU 的 provider=baidu）：
			// 从 metadata 注入，LLM 无需感知也无需传递，避免误选源或漏传 provider。
			if fixedProvider, ok := manifest.Metadata["provider"]; ok && fixedProvider != "" {
				if _, exists := input["provider"]; !exists {
					input["provider"] = fixedProvider
				}
			}
		}

		// 对于 MCP 驱动，将服务端点配置注入 input，供 mcpDriver 读取。
		// 同时兼容 manifest.metadata.mcp_endpoint 的遗留写法。
		if manifest.Driver == model.ToolDriverMCP || (dynRec != nil && dynRec.TransportType == string(model.ModuleTransportTypeMCP)) {
			if dynRec != nil && dynRec.MCPServerConfig != nil {
				if b, err := json.Marshal(dynRec.MCPServerConfig); err == nil {
					input["__mcp_server__"] = string(b)
				}
			}
			if endpoint := manifest.Metadata["mcp_endpoint"]; endpoint != "" {
				input["__mcp_endpoint__"] = endpoint
			}
		}

		return driver.Execute(ctx, action, input, env)
	}
}

// ActivateToolOnPurchase 购买成功后激活动态工具
func (l *AgentToolLoader) ActivateToolOnPurchase(userID, agentID string) error {
	agent, err := l.agentRepo.GetByID(agentID)
	if err != nil {
		return err
	}
	manifest, err := agent.Manifest()
	if err != nil {
		return err
	}
	if manifest == nil || manifest.Driver == model.ToolDriverNone {
		return nil
	}

	toolName := manifest.ID
	if toolName == "" {
		toolName = fmt.Sprintf("Agent_%s", agentID)
	}

	// 幂次：已存在则跳过
	exists, _ := l.agentRepo.HasUserTool(userID, agentID)
	if exists {
		return nil
	}

	return l.agentRepo.CreateUserTool(&model.AgentUserTool{
		ID:        uuid.New().String(),
		UserID:    userID,
		AgentID:   agentID,
		ToolName:  toolName,
		Active:    true,
		CreatedAt: time.Now(),
	})
}

// ValidateManifest 校验 ToolManifest 是否合法
func (l *AgentToolLoader) ValidateManifest(manifest *model.ToolManifest) error {
	if manifest == nil {
		return errors.New("manifest 不能为空")
	}
	if manifest.ID == "" {
		return errors.New("manifest.id 不能为空")
	}
	if manifest.Name == "" {
		return errors.New("manifest.name 不能为空")
	}
	if manifest.Driver == "" {
		return errors.New("manifest.driver 不能为空")
	}
	if _, _, ok := l.resolveDriver(string(manifest.Driver)); !ok {
		return fmt.Errorf("驱动未注册: %s", manifest.Driver)
	}
	if manifest.Parameters == nil {
		return errors.New("manifest.parameters 不能为空")
	}
	paramType, _ := manifest.Parameters["type"].(string)
	if paramType != "object" {
		return errors.New("manifest.parameters.type 必须是 object")
	}
	// 简单校验 JSON 可序列化
	if _, err := json.Marshal(manifest); err != nil {
		return fmt.Errorf("manifest 序列化失败: %w", err)
	}
	return nil
}

// resolveDriver 解析驱动：优先从内存注册表查找，否则查找动态驱动记录并映射到通用运行时驱动
func (l *AgentToolLoader) resolveDriver(driverName string) (ToolDriver, *model.DriverRecord, bool) {
	if d, ok := l.driverRegistry.Get(driverName); ok {
		return d, nil, true
	}
	if l.moduleService == nil {
		return nil, nil, false
	}
	rec, err := l.moduleService.ResolveDriver(driverName)
	if err != nil || rec == nil {
		return nil, nil, false
	}
	switch rec.TransportType {
	case string(model.ModuleTransportTypeModule):
		d, ok := l.driverRegistry.Get(string(model.ToolDriverModule))
		return d, rec, ok
	case string(model.ModuleTransportTypeRemoteURL):
		d, ok := l.driverRegistry.Get(string(model.ToolDriverRemoteURL))
		return d, rec, ok
	case string(model.ModuleTransportTypeMCP):
		d, ok := l.driverRegistry.Get(string(model.ToolDriverMCP))
		return d, rec, ok
	}
	return nil, nil, false
}

// isDynamicModuleDriver 判断 driver 名是否注册为 module 型动态驱动
func (l *AgentToolLoader) isDynamicModuleDriver(driverName string) bool {
	_, rec, ok := l.resolveDriver(driverName)
	if !ok || rec == nil {
		return false
	}
	return rec.TransportType == string(model.ModuleTransportTypeModule)
}

// ResolveDriver 公开解析驱动记录，供上层（如 SKU 审批）判断驱动是否已注册。
func (l *AgentToolLoader) ResolveDriver(driverName string) (*model.DriverRecord, bool) {
	_, rec, ok := l.resolveDriver(driverName)
	return rec, ok
}

// ResolveModuleID 从 manifest 或动态驱动记录中解析模块 ID。
// 优先读取 metadata.module（兼容旧写法），其次根据 driver 字段查找动态驱动记录的 ModuleID。
// 注意：driverRegistry 命中 SkillRuntimeDriver 别名时 resolveDriver 返回 rec=nil（别名本身
// 无 DriverRecord），此时手写 SKU（无 metadata.module，如 agent-reach/web.json）会解析失败
// -> 卡片误判离线（而按 runtime ID 探活却在线）。故 rec 为空时回退 moduleService 查 DB
// 拿 DriverRecord.ModuleID，与 ModuleService.resolveModuleIDFromManifest 同语义。
func (l *AgentToolLoader) ResolveModuleID(manifest *model.ToolManifest) string {
	if manifest == nil {
		return ""
	}
	if manifest.Metadata != nil && manifest.Metadata["module"] != "" {
		return manifest.Metadata["module"]
	}
	_, rec, _ := l.resolveDriver(string(manifest.Driver))
	if rec == nil && l.moduleService != nil {
		if r, err := l.moduleService.ResolveDriver(string(manifest.Driver)); err == nil {
			rec = r
		}
	}
	if rec != nil {
		switch rec.TransportType {
		case string(model.ModuleTransportTypeModule), string(model.ModuleTransportTypeMCP):
			return rec.ModuleID
		}
	}
	return ""
}

// sanitizeToolName 保证工具名符合 OpenAI function 命名规范：
// 必须以字母开头，只能包含字母、数字、下划线和连字符，且长度不超过 64。
func sanitizeToolName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	s := sb.String()
	if s == "" {
		return "tool"
	}
	first := rune(s[0])
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')) {
		s = "t_" + s
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}
