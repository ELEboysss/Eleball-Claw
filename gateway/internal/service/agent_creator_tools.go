package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 创造模式（F3，借鉴 DSH 创造流）：对话内直接完成「造秘技 + 导入」闭环。
// creator 模式下 AgentService 向 registry 注入本文件的 3 个创造工具，并在 system prompt
// 追加接入规范段（creatorModeSystemPrompt）。工具走与 DIY 工作室相同的 service 路径
// （WritePromptSkill / probe + InstallMCPRuntime / MCPRegistryClient），保证产物结构一致。

// AgentModeStandard / AgentModeCreator 对话页顶栏模式（standard=默认对话；creator=秘技创造）。
const (
	AgentModeStandard = "standard"
	AgentModeCreator  = "creator"
)

// NormalizeAgentMode 归一化模式值（空/未知回落 standard）。
func NormalizeAgentMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), AgentModeCreator) {
		return AgentModeCreator
	}
	return AgentModeStandard
}

// creatorModeSystemPrompt 创造模式附加的秘技接入规范段。
// 内容即「我们的接入规范」：秘技包结构（package.json + SKILL.md + mcpServers）、
// 三种创造路径对应的工具、命名约束与禁止事项。
const creatorModeSystemPrompt = `

【创造模式：秘技创造与导入】当前会话处于创造模式，你的任务是按接入规范帮助用户创造/导入秘技（Skill）。你可以使用以下专属工具：
1. SearchMCPRegistry：搜索官方 MCP 社区注册表，找现成的 MCP server（按功能关键词搜，如 "filesystem"、"github"）。
2. InstallMCPServer：安装一个 MCP server 为本地秘技（stdio 给 command/args/env；http 给 endpoint/headers）。安装前若用户没给完整配置，先 SearchMCPRegistry 或向用户确认；安装成功后其工具立即可用。
3. CreatePromptSkill：创建纯提示词秘技（人格/流程/规范类，无需代码）。name 为中文展示名，skill_id 为小写字母/数字/中划线，description 写清触发条件（何时该用这个秘技），body 为完整提示词正文（角色设定、工作流程、输出要求、约束）。
工作方式：先理解用户想要的秘技能力；若可用现成 MCP server 实现则搜索并安装；若是提示词/人格类则直接创建；需要文件类秘技包时也可用 WriteFile 在工作目录组织 package.json（含 skills[]/tools[]/mcpServers 字段）+ SKILL.md 结构。完成后简要告知用户秘技名称、用途与生效方式（新对话即可用）。不要编造安装结果——以工具实际返回为准。`

// creatorToolSearchRegistry / creatorToolInstallMCP / creatorToolCreatePromptSkill 工具名。
const (
	creatorToolSearchRegistry    = "SearchMCPRegistry"
	creatorToolInstallMCP        = "InstallMCPServer"
	creatorToolCreatePromptSkill = "CreatePromptSkill"
)

// buildCreatorTools 构造创造模式专属工具集（仅 claw 装配；moduleSvc/mcpStdio 为 nil 时对应工具缺席）。
func (s *AgentService) buildCreatorTools() []*Tool {
	tools := make([]*Tool, 0, 3)

	// 1. 搜索官方 MCP 社区注册表（只读，无需审批）
	if s.mcpRegistry != nil {
		tools = append(tools, &Tool{
			Name:        creatorToolSearchRegistry,
			Description: "搜索官方 MCP 社区注册表（registry.modelcontextprotocol.io），查找现成的 MCP server。返回名称、描述、传输方式与安装配置建议。",
			ReadOnly:    true,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "功能关键词，如 filesystem / github / postgres"},
					"limit": map[string]interface{}{"type": "integer", "description": "返回条数上限（默认 8，最大 50）"},
				},
				"required": []string{"query"},
			},
			Func: func(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
				query, _ := input["query"].(string)
				if strings.TrimSpace(query) == "" {
					return nil, errors.New("query 不能为空")
				}
				limit := 8
				if n, ok := input["limit"].(float64); ok && n > 0 {
					limit = int(n)
				}
				entries, err := s.mcpRegistry.Search(ctx, query, limit)
				if err != nil {
					return nil, fmt.Errorf("搜索 MCP 注册表失败: %w", err)
				}
				return map[string]interface{}{"results": entries, "count": len(entries)}, nil
			},
		})
	}

	// 2. 安装 MCP server（写操作，走审批闸）
	if s.moduleSvc != nil {
		tools = append(tools, &Tool{
			Name:        creatorToolInstallMCP,
			Description: "安装一个 MCP server 为本地秘技：先探测（stdio 临时拉起取工具列表 / http 直接 ListTools），探测成功后注册运行时并派生工具。stdio 需 command/args（可选 env）；http 需 endpoint（可选 headers）。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "description": "秘技名称（运行时标识，如 github-mcp）"},
					"description": map[string]interface{}{"type": "string", "description": "用途简述"},
					"transport":   map[string]interface{}{"type": "string", "enum": []string{"mcp_stdio", "mcp_http"}, "description": "默认 mcp_stdio"},
					"command":     map[string]interface{}{"type": "string", "description": "stdio 启动命令，如 npx"},
					"args":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "stdio 参数，如 [\"-y\",\"@modelcontextprotocol/server-filesystem\",\".\"]"},
					"env":         map[string]interface{}{"type": "object", "description": "stdio 环境变量（如 API key）"},
					"endpoint":    map[string]interface{}{"type": "string", "description": "http MCP 地址（mcp_http 时必填）"},
					"headers":     map[string]interface{}{"type": "object", "description": "http 请求头（如 Authorization）"},
				},
				"required": []string{"name"},
			},
			Func: func(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
				return s.installMCPFromCreator(ctx, input, env)
			},
		})
	}

	// 3. 创建 prompt-only 秘技（写操作，走审批闸）
	if s.moduleSvc != nil {
		tools = append(tools, &Tool{
			Name:        creatorToolCreatePromptSkill,
			Description: "创建纯提示词秘技（Anthropic 标准 SKILL.md）：人格/流程/规范类能力，无需代码。创建后立即同步为可用秘技，新对话生效。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "description": "展示名（可中文），如「周报助手」"},
					"skill_id":    map[string]interface{}{"type": "string", "description": "可选 slug（^[a-z0-9][a-z0-9-]*$），缺省据 name 推导"},
					"description": map[string]interface{}{"type": "string", "description": "触发条件描述：什么场景应使用这个秘技"},
					"category":    map[string]interface{}{"type": "string", "description": "可选分类，如 写作/办公/编程"},
					"body":        map[string]interface{}{"type": "string", "description": "SKILL.md 正文：角色设定、工作流程、输出要求、约束（完整提示词）"},
				},
				"required": []string{"name", "description", "body"},
			},
			Func: func(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
				name, _ := input["name"].(string)
				description, _ := input["description"].(string)
				body, _ := input["body"].(string)
				skillID, _ := input["skill_id"].(string)
				category, _ := input["category"].(string)
				result, err := s.moduleSvc.WritePromptSkill(PromptSkillGenerateRequest{
					SkillID:     skillID,
					Name:        name,
					Description: description,
					Category:    category,
					Body:        body,
				})
				if err != nil {
					return nil, err
				}
				// 定向同步 SKU（与 GeneratePromptSkill handler 同路径）；同步函数由 main 装配（service 不依赖 seed 包）
				synced := false
				if s.skillSyncFn != nil {
					created, updated, _ := s.skillSyncFn(result.Dir, result.SkillID, env.UserID, "我")
					synced = created+updated > 0
				}
				return map[string]interface{}{
					"skill_id": result.SkillID,
					"sku_id":   "skillmd-" + result.SkillID,
					"synced":   synced,
					"note":     "秘技已创建" + map[bool]string{true: "并同步生效（新对话可用）", false: "；SKU 同步未执行，请到 DIY 工作室检查"}[synced],
				}, nil
			},
		})
	}
	return tools
}

// installMCPFromCreator 创造模式的 MCP 安装：探测（30s 超时）→ InstallMCPRuntime。
// stdio work_dir 收敛为会话 cwd（防任意目录探测）；http 直连 ListTools。
func (s *AgentService) installMCPFromCreator(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	name, _ := input["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name 不能为空")
	}
	req := &MCPInstallRequest{
		Name:        name,
		Description: stringInput(input, "description"),
		Transport:   stringInput(input, "transport"),
		Command:     stringInput(input, "command"),
		Endpoint:    stringInput(input, "endpoint"),
		Args:        stringSliceInput(input, "args"),
		Env:         stringMapInput(input, "env"),
		Headers:     stringMapInput(input, "headers"),
	}
	if req.Transport == "" {
		req.Transport = "mcp_stdio"
	}

	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var tools []MCPTool
	var err error
	switch req.Transport {
	case "mcp_stdio":
		if s.mcpStdio == nil {
			return nil, errors.New("stdio MCP 协议未初始化")
		}
		if req.Command == "" {
			return nil, errors.New("stdio 安装需要 command")
		}
		req.WorkDir = env.Cwd // 探测工作目录收敛到会话 cwd（空则继承进程目录）
		tools, err = s.mcpStdio.ProbeStdio(probeCtx, req.Command, req.Args, req.Env, req.WorkDir)
	case "mcp_http":
		if req.Endpoint == "" {
			return nil, errors.New("http 安装需要 endpoint")
		}
		tools, err = NewMCPHTTPProtocol(nil).ListTools(probeCtx, req.Endpoint, req.Headers)
	default:
		return nil, fmt.Errorf("不支持的 transport: %s", req.Transport)
	}
	if err != nil {
		return nil, fmt.Errorf("MCP 探测失败: %w", err)
	}

	result, err := s.moduleSvc.InstallMCPRuntime(req, tools)
	if err != nil {
		return nil, err
	}
	toolNames := make([]string, 0, len(result.Tools))
	for _, t := range result.Tools {
		toolNames = append(toolNames, t.Name)
	}
	return map[string]interface{}{
		"runtime_id": result.RuntimeID,
		"transport":  result.Transport,
		"sku_count":  result.SKUCount,
		"tools":      toolNames,
		"note":       "MCP server 已安装并派生工具，新对话即可使用",
	}, nil
}

// stringInput / stringSliceInput / stringMapInput 工具入参安全取值辅助。
func stringInput(input map[string]interface{}, key string) string {
	s, _ := input[key].(string)
	return s
}

func stringSliceInput(input map[string]interface{}, key string) []string {
	raw, ok := input[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringMapInput(input map[string]interface{}, key string) map[string]string {
	raw, ok := input[key].(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
