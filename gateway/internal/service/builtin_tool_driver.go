package service

import (
	"context"
	"errors"

	"github.com/eleball/gateway/internal/model"
)

// builtinToolDriver 系统内置工具驱动
// 把 ToolRegistry 中已有的 ReadFile/WriteFile/Shell/OCR/SearchWeb/FetchURL 等工具
// 包装为 ToolDriver 接口的实现，使它们与外部 driver 在集市中等价。
type builtinToolDriver struct {
	registry *ToolRegistry
}

func (d *builtinToolDriver) Name() string {
	return string(model.ToolDriverBuiltin)
}

func (d *builtinToolDriver) Schema() model.ToolManifest {
	return model.ToolManifest{
		ID:          "com.eleball.tools.builtin",
		Name:        "系统内置工具",
		Description: "Eleball 系统内置的文件、Shell、OCR、搜索等基础工具。",
		Driver:      model.ToolDriverBuiltin,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tool": map[string]interface{}{
					"type":        "string",
					"description": "要调用的内置工具名",
					"enum": []string{
						"ReadFile", "WriteFile", "StrReplaceFile", "Grep",
						"Shell", "OCR", "SearchWeb", "FetchURL",
					},
				},
				"input": map[string]interface{}{
					"type":        "object",
					"description": "对应工具的输入参数",
				},
			},
			"required": []string{"tool", "input"},
		},
	}
}

func (d *builtinToolDriver) Execute(ctx context.Context, action string, params map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	// action 对应内置工具名
	tool, ok := d.registry.Get(action)
	if !ok {
		return nil, errors.New("未知内置工具: " + action)
	}
	return tool.Func(ctx, params, env)
}
