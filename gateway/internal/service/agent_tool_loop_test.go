package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/pkg/llm"
)

// mockAgentLLM 用于测试 ToolCallingLoop 的 LLM 客户端桩
type mockAgentLLM struct {
	responses []llm.ChatChunk
	idx       int
}

func (m *mockAgentLLM) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
	if m.idx >= len(m.responses) {
		return &llm.ChatChunk{Delta: "final"}, nil
	}
	resp := m.responses[m.idx]
	m.idx++
	return &resp, nil
}

func (m *mockAgentLLM) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk, 1)
	// 取最后一个非 tool_calls 响应的 delta 作为流式最终输出
	delta := "stream"
	for i := len(m.responses) - 1; i >= 0; i-- {
		if len(m.responses[i].ToolCalls) == 0 && m.responses[i].Delta != "" {
			delta = m.responses[i].Delta
			break
		}
	}
	ch <- llm.ChatChunk{Delta: delta}
	close(ch)
	return ch, nil
}

func TestToolCallingLoop_DirectAnswer(t *testing.T) {
	registry := NewToolRegistry()
	loop := NewToolCallingLoop(registry, 3)
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{{Delta: "hello"}},
	}

	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "hi"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("直接回答不应报错: %v", err)
	}
	if result.FinalContent != "hello" {
		t.Fatalf("直接回答内容不对: %v", result.FinalContent)
	}
	if len(result.Records) != 0 {
		t.Fatalf("不应有工具调用记录: %v", result.Records)
	}
}

func TestToolCallingLoop_SingleToolCall(t *testing.T) {
	registry := NewToolRegistryWithDeps(&mockRunner{shellOutput: "ok"}, &mockSearchProvider{result: map[string]interface{}{"results": []string{"r1"}}})
	loop := NewToolCallingLoop(registry, 3)
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "Shell", Arguments: `{"command":"echo","args":["hello"]}`}},
				},
			},
			{Delta: "done"},
		},
	}

	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "run echo"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("工具调用循环不应报错: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("应有 1 条工具调用记录: %v", result.Records)
	}
	if result.Records[0].Tool != "Shell" {
		t.Fatalf("工具名不对: %v", result.Records[0].Tool)
	}
	if result.Records[0].Error != "" {
		t.Fatalf("工具执行不应报错: %v", result.Records[0].Error)
	}
	if result.FinalContent != "done" {
		t.Fatalf("最终回答不对: %v", result.FinalContent)
	}
}

func TestToolCallingLoop_UnknownTool(t *testing.T) {
	registry := NewToolRegistry()
	loop := NewToolCallingLoop(registry, 3)
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "NotExist", Arguments: `{}`}},
				},
			},
			{Delta: "done"},
		},
	}

	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "test"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("应有 1 条记录: %v", result.Records)
	}
	if result.Records[0].Error == "" {
		t.Fatal("未知工具应记录错误")
	}
}

func TestToolCallingLoop_InvalidArguments(t *testing.T) {
	registry := NewToolRegistry()
	loop := NewToolCallingLoop(registry, 3)
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "Shell", Arguments: `not json`}},
				},
			},
			{Delta: "done"},
		},
	}

	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "test"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if result.Records[0].Error == "" {
		t.Fatal("非法参数应记录错误")
	}
}

func TestToolCallingLoop_ReachMaxSteps(t *testing.T) {
	registry := NewToolRegistryWithDeps(&mockRunner{}, &mockSearchProvider{})
	loop := NewToolCallingLoop(registry, 1)
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "Shell", Arguments: `{"command":"echo","args":["a"]}`}},
				},
			},
			{Delta: "final"},
		},
	}

	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "test"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !result.ReachMaxSteps {
		t.Fatal("应标记达到最大步数")
	}
}

func TestToolCallingLoop_BareJSONFeedback(t *testing.T) {
	// 模型第一轮用裸 JSON 调 Shell（无标记），应被反馈而非执行；
	// 第二轮改用结构化 tool_calls，正常执行。AR-25 反馈式。
	registry := NewToolRegistryWithDeps(&mockRunner{shellOutput: "ok"}, &mockSearchProvider{result: map[string]interface{}{"results": []string{"r1"}}})
	loop := NewToolCallingLoop(registry, 5)
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{Delta: `[{"name":"Shell","parameters":{"command":"echo","args":["hello"]}}]`},
			{ToolCalls: []llm.ToolCall{
				{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "Shell", Arguments: `{"command":"echo","args":["hello"]}`}},
			}},
			{Delta: "done"},
		},
	}

	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "run echo"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	// 裸 JSON 那轮被反馈（不执行），只有结构化那轮执行 -> 1 条记录
	if len(result.Records) != 1 {
		t.Fatalf("裸 JSON 应被反馈不执行，期望 1 条记录，实际 %d: %v", len(result.Records), result.Records)
	}
	if result.Records[0].Tool != "Shell" {
		t.Fatalf("工具名不对: %v", result.Records[0].Tool)
	}
	if result.FinalContent != "done" {
		t.Fatalf("最终回答不对: %v", result.FinalContent)
	}
}

func TestToolCallingLoop_BareJSONNotResolvedTransfers(t *testing.T) {
	// 裸 JSON name 不命中 registry（如正文里的 JSON 数据/示例）-> 不当工具调用，透传原文。AR-25。
	registry := NewToolRegistry()
	loop := NewToolCallingLoop(registry, 3)
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{Delta: `{"name":"SomeData","parameters":{"x":1}}`},
		},
	}
	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "hi"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(result.Records) != 0 {
		t.Fatalf("不命中的裸 JSON 不应执行工具: %v", result.Records)
	}
	if result.FinalContent != `{"name":"SomeData","parameters":{"x":1}}` {
		t.Fatalf("应透传原 JSON，实际: %v", result.FinalContent)
	}
}

func TestToolResultToString(t *testing.T) {
	if s := toolResultToString(nil, "error"); s != "工具执行失败: error" {
		t.Fatalf("错误结果转换不对: %v", s)
	}
	out := map[string]interface{}{"a": 1}
	b, _ := json.Marshal(out)
	if s := toolResultToString(out, ""); s != string(b) {
		t.Fatalf("正常结果转换不对: %v", s)
	}
}

func TestToolCallingLoop_FunctionGet(t *testing.T) {
	// AR-26：模型发 FunctionGet 元工具调用 -> 拦截返回工具列表 -> 模型随后调真实工具执行。
	registry := NewToolRegistryWithDeps(&mockRunner{shellOutput: "ok"}, &mockSearchProvider{})
	loop := NewToolCallingLoop(registry, 5)
	tools := []map[string]interface{}{
		{"type": "function", "function": map[string]interface{}{
			"name":        "Shell",
			"description": "执行 shell 命令",
			"parameters":  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		}},
	}
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{ToolCalls: []llm.ToolCall{
				{ID: "fg1", Type: "function", Function: llm.ToolCallFunction{Name: "FunctionGet", Arguments: `{}`}},
			}},
			{ToolCalls: []llm.ToolCall{
				{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "Shell", Arguments: `{"command":"echo","args":["hello"]}`}},
			}},
			{Delta: "done"},
		},
	}

	result, err := loop.Run(context.Background(), client, "m", tools, []llm.Message{{Role: "user", Content: "run echo"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	// 第一轮 FunctionGet 拦截（返回工具列表，不执行真实工具），第二轮 Shell 执行 -> 2 条记录
	if len(result.Records) != 2 {
		t.Fatalf("期望 2 条记录（FunctionGet + Shell），实际 %d: %v", len(result.Records), result.Records)
	}
	if result.Records[0].Tool != "FunctionGet" {
		t.Fatalf("第一条记录应为 FunctionGet: %v", result.Records[0].Tool)
	}
	list, ok := result.Records[0].Output["tool_list"].(string)
	if !ok || !strings.Contains(list, "Shell") {
		t.Fatalf("FunctionGet 应返回含 Shell 的工具列表: %v", result.Records[0].Output)
	}
	if result.Records[1].Tool != "Shell" {
		t.Fatalf("第二条记录应为 Shell: %v", result.Records[1].Tool)
	}
	if result.FinalContent != "done" {
		t.Fatalf("最终回答不对: %v", result.FinalContent)
	}
}

func TestToolCallingLoop_FunctionGetLimit(t *testing.T) {
	// AR-26：FunctionGet 累积超过 maxFunctionGetCalls(2) -> 第三次返回「勿重复请求」，不循环塞。
	// 中间穿插 Shell 调用避免触发连续同参数 loop 检测，单独验证 FunctionGet 累积限流。
	registry := NewToolRegistryWithDeps(&mockRunner{shellOutput: "ok"}, &mockSearchProvider{})
	loop := NewToolCallingLoop(registry, 10)
	tools := []map[string]interface{}{
		{"type": "function", "function": map[string]interface{}{"name": "Shell", "description": "shell"}},
	}
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{ToolCalls: []llm.ToolCall{{ID: "fg1", Type: "function", Function: llm.ToolCallFunction{Name: "FunctionGet", Arguments: `{}`}}}},
			{ToolCalls: []llm.ToolCall{{ID: "s1", Type: "function", Function: llm.ToolCallFunction{Name: "Shell", Arguments: `{"command":"echo","args":["a"]}`}}}},
			{ToolCalls: []llm.ToolCall{{ID: "fg2", Type: "function", Function: llm.ToolCallFunction{Name: "FunctionGet", Arguments: `{}`}}}},
			{ToolCalls: []llm.ToolCall{{ID: "s2", Type: "function", Function: llm.ToolCallFunction{Name: "Shell", Arguments: `{"command":"echo","args":["b"]}`}}}},
			{ToolCalls: []llm.ToolCall{{ID: "fg3", Type: "function", Function: llm.ToolCallFunction{Name: "FunctionGet", Arguments: `{}`}}}},
			{Delta: "done"},
		},
	}

	result, err := loop.Run(context.Background(), client, "m", tools, []llm.Message{{Role: "user", Content: "hi"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	// 记录：fg1, s1, fg2, s2, fg3 = 5 条
	if len(result.Records) != 5 {
		t.Fatalf("期望 5 条记录，实际 %d: %v", len(result.Records), result.Records)
	}
	var fgLists []string
	for _, r := range result.Records {
		if r.Tool == "FunctionGet" {
			if list, ok := r.Output["tool_list"].(string); ok {
				fgLists = append(fgLists, list)
			}
		}
	}
	if len(fgLists) != 3 {
		t.Fatalf("期望 3 次 FunctionGet，实际 %d", len(fgLists))
	}
	if !strings.Contains(fgLists[0], "Shell") {
		t.Fatalf("第一次 FunctionGet 应返回工具列表: %v", fgLists[0])
	}
	if !strings.Contains(fgLists[2], "勿重复请求") {
		t.Fatalf("第三次 FunctionGet 应返回勿重复请求: %v", fgLists[2])
	}
}

var _ AgentLLMClient = (*mockAgentLLM)(nil)

// TestToolCallingLoop_DynamicRuleInjection C8：工具调用触及的文件路径会触发 .claw/rules/*.md 动态注入。
func TestToolCallingLoop_DynamicRuleInjection(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".claw", "rules")
	_ = os.MkdirAll(rulesDir, 0755)
	_ = os.WriteFile(filepath.Join(rulesDir, "go.md"), []byte("---\npaths:\n  - '*.go'\n---\nuse gofmt for Go files"), 0644)

	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true, RulesEnabled: true})

	registry := NewToolRegistryWithDeps(&mockRunner{}, &mockSearchProvider{})
	registry.Register(&Tool{
		Name:        "FakeFileTool",
		Description: "fake file tool",
		ReadOnly:    true,
		Driver:      "builtin",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
			"required":   []string{"path"},
		},
		Func: func(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
			return map[string]interface{}{"ok": true}, nil
		},
	})
	loop := NewToolCallingLoop(registry, 5)
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{ToolCalls: []llm.ToolCall{
				{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "FakeFileTool", Arguments: `{"path":"main.go"}`}},
			}},
			{Delta: "done"},
		},
	}

	env := &ToolEnv{Cwd: root, ContextFileSvc: svc}
	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "run"}}, env, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}

	found := false
	for _, m := range result.Messages {
		if m.Role == "system" && strings.Contains(fmt.Sprintf("%v", m.Content), "use gofmt for Go files") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("未在消息列表中找到动态注入的规则：%v", result.Messages)
	}
}
