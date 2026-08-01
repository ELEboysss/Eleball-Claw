package service

import (
	"encoding/json"
	"strings"

	"github.com/eleball/gateway/internal/model"
)

// ToolSchemaBuilder 根据用户权限构建可用工具 schema
type ToolSchemaBuilder struct {
	registry *ToolRegistry
}

// NewToolSchemaBuilder 创建 schema 构建器
func NewToolSchemaBuilder(registry *ToolRegistry) *ToolSchemaBuilder {
	return &ToolSchemaBuilder{registry: registry}
}

// Build 返回 OpenAI 兼容的 tools 列表（默认启用联网工具，保持向后兼容）
func (b *ToolSchemaBuilder) Build(isVIP bool) []map[string]interface{} {
	return b.BuildWithOptions(isVIP, true, model.PermissionModeDefault)
}

// BuildWithOptions 根据用户权限和联网开关构建可用工具 schema
// enableWebSearch=false 时过滤 SearchWeb / FetchURL，避免 Agent 调用需要服务器资源的搜索/抓取服务
func (b *ToolSchemaBuilder) BuildWithOptions(isVIP bool, enableWebSearch bool, permissionMode model.PermissionMode) []map[string]interface{} {
	return b.BuildWithOptionsAndDynamic(isVIP, enableWebSearch, nil, permissionMode)
}

// BuildWithOptionsAndDynamic 在 BuildWithOptions 基础上合并用户购买的动态工具。
// C3：ExitPlanMode 仅在 plan 模式暴露给 LLM（其余模式隐藏，避免误调）。
func (b *ToolSchemaBuilder) BuildWithOptionsAndDynamic(isVIP bool, enableWebSearch bool, dynamicTools []*Tool, permissionMode model.PermissionMode) []map[string]interface{} {
	tools := b.registry.ListAvailable(isVIP)
	allTools := make([]*Tool, 0, len(tools)+len(dynamicTools))
	allTools = append(allTools, tools...)
	for _, t := range dynamicTools {
		if t.ServerSide && !isVIP {
			continue
		}
		allTools = append(allTools, t)
	}

	planMode := permissionMode == model.PermissionModePlan
	result := make([]map[string]interface{}, 0, len(allTools))
	for _, tool := range allTools {
		if !enableWebSearch && (tool.Name == "SearchWeb" || tool.Name == "FetchURL") {
			continue
		}
		// ExitPlanMode 仅 plan 模式暴露
		if tool.Name == "ExitPlanMode" && !planMode {
			continue
		}
		result = append(result, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			},
		})
	}
	return result
}

// RenderToolsAsText 将 OpenAI 兼容的 tools schema 列表渲染为 Markdown 文本，
// 供 FunctionGet 元工具返回给走内嵌标记（不支持原生 function calling）的模型。
// 输入为 ToolSchemaBuilder.Build* 返回的 schema（当前 assistant 的可用工具集），
// 保证文本与结构化 tools 参数同源（单一事实源）。AR-26。
func RenderToolsAsText(tools []map[string]interface{}) string {
	if len(tools) == 0 {
		return "（当前无可用工具）"
	}
	var b strings.Builder
	b.WriteString("可用工具列表及用法：\n")
	for _, t := range tools {
		fn, ok := t["function"].(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		b.WriteString("\n### ")
		b.WriteString(name)
		b.WriteString("\n")
		b.WriteString(desc)
		b.WriteString("\n")
		if params, ok := fn["parameters"]; ok {
			if pj, err := json.Marshal(params); err == nil && len(pj) > 2 { // 非 "{}"
				b.WriteString("参数 schema：")
				b.Write(pj)
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\n调用方式：使用内联标记 <|FunctionCallBegin|>[{\"name\":\"工具名\",\"parameters\":{...}}]<|FunctionCallEnd|>，工具名需精确匹配上述列表（区分大小写）。")
	return b.String()
}
