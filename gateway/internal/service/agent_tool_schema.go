package service

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
	return b.BuildWithOptions(isVIP, true)
}

// BuildWithOptions 根据用户权限和联网开关构建可用工具 schema
// enableWebSearch=false 时过滤 SearchWeb / FetchURL，避免 Agent 调用需要服务器资源的搜索/抓取服务
func (b *ToolSchemaBuilder) BuildWithOptions(isVIP bool, enableWebSearch bool) []map[string]interface{} {
	return b.BuildWithOptionsAndDynamic(isVIP, enableWebSearch, nil)
}

// BuildWithOptionsAndDynamic 在 BuildWithOptions 基础上合并用户购买的动态工具
func (b *ToolSchemaBuilder) BuildWithOptionsAndDynamic(isVIP bool, enableWebSearch bool, dynamicTools []*Tool) []map[string]interface{} {
	tools := b.registry.ListAvailable(isVIP)
	allTools := make([]*Tool, 0, len(tools)+len(dynamicTools))
	allTools = append(allTools, tools...)
	for _, t := range dynamicTools {
		if t.ServerSide && !isVIP {
			continue
		}
		allTools = append(allTools, t)
	}

	result := make([]map[string]interface{}, 0, len(allTools))
	for _, tool := range allTools {
		if !enableWebSearch && (tool.Name == "SearchWeb" || tool.Name == "FetchURL") {
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
