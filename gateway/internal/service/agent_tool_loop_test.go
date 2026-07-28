package service

import (
	"context"
	"encoding/json"
	"testing"

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

var _ AgentLLMClient = (*mockAgentLLM)(nil)
