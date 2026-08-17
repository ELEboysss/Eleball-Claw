package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/eleball/gateway/pkg/llm"
)

// capturingPairingLLM 记录每次 Chat 请求的 LLM 客户端桩，用于断言发给上游的消息序列。
type capturingPairingLLM struct {
	responses []llm.ChatChunk
	idx       int
	requests  []llm.ChatRequest
}

var _ AgentLLMClient = (*capturingPairingLLM)(nil)

func (m *capturingPairingLLM) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
	m.requests = append(m.requests, req)
	if m.idx >= len(m.responses) {
		return &llm.ChatChunk{Delta: "final"}, nil
	}
	resp := m.responses[m.idx]
	m.idx++
	return &resp, nil
}

func (m *capturingPairingLLM) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk, 1)
	ch <- llm.ChatChunk{Delta: "stream"}
	close(ch)
	return ch, nil
}

// validateToolPairing 校验 OpenAI tool_calls 配对不变式：
// 带 tool_calls 的 assistant 消息后必须紧跟覆盖全部 tool_call_id 的 tool 消息
// （中间不得插入 system/user 等其他角色，严格上游如 DeepSeek 否则报 400）。
func validateToolPairing(msgs []llm.Message) error {
	for i, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			pending := map[string]bool{}
			for _, tc := range m.ToolCalls {
				pending[tc.ID] = true
			}
			j := i + 1
			for j < len(msgs) && msgs[j].Role == "tool" {
				delete(pending, msgs[j].ToolCallID)
				j++
			}
			if len(pending) > 0 {
				return fmt.Errorf("assistant@%d 的 tool_calls 缺少紧邻的 tool 响应: %v", i, pending)
			}
		}
		if m.Role == "tool" {
			found := false
			for k := i - 1; k >= 0; k-- {
				if msgs[k].Role == "assistant" {
					for _, tc := range msgs[k].ToolCalls {
						if tc.ID == m.ToolCallID {
							found = true
						}
					}
					if len(msgs[k].ToolCalls) > 0 {
						break
					}
				}
			}
			if !found {
				return fmt.Errorf("tool@%d (id=%q) 找不到所属 assistant tool_call", i, m.ToolCallID)
			}
		}
	}
	return nil
}

// registerTinyTool 注册一个返回极小结果（触发稀疏 nudge）的测试工具。
func registerTinyTool(registry *ToolRegistry) {
	registry.Register(&Tool{
		Name: "Tiny",
		Func: func(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
			return map[string]interface{}{"ok": true}, nil
		},
	})
}

// TestToolCallingLoop_ParallelToolCallsSparseNudgeKeepsPairing 回归：
// 并行 tool_calls + 首个结果触发稀疏 nudge 时，nudge 必须在本轮全部 tool 消息之后注入，
// 不得插在 tool 消息之间（否则 DeepSeek 等严格上游报 400
// "insufficient tool messages following tool_calls message"）。
func TestToolCallingLoop_ParallelToolCallsSparseNudgeKeepsPairing(t *testing.T) {
	registry := NewToolRegistry()
	registerTinyTool(registry)
	loop := NewToolCallingLoop(registry, 5)
	client := &capturingPairingLLM{responses: []llm.ChatChunk{
		{ToolCalls: []llm.ToolCall{
			{ID: "p1", Type: "function", Function: llm.ToolCallFunction{Name: "Tiny", Arguments: `{"q":"a"}`}},
			{ID: "p2", Type: "function", Function: llm.ToolCallFunction{Name: "Tiny", Arguments: `{"q":"b"}`}},
		}},
		{Delta: "done"},
	}}

	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "hi"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if result.FinalContent != "done" {
		t.Fatalf("最终回答不对: %v", result.FinalContent)
	}
	if len(client.requests) < 2 {
		t.Fatalf("应至少发起 2 次 LLM 调用，实际 %d", len(client.requests))
	}
	second := client.requests[1].Messages
	if verr := validateToolPairing(second); verr != nil {
		for i, m := range second {
			t.Logf("  [%d] role=%s toolCalls=%d toolCallID=%q", i, m.Role, len(m.ToolCalls), m.ToolCallID)
		}
		t.Fatalf("并行 tool_calls 的配对被 nudge 破坏: %v", verr)
	}
	// 稀疏 nudge 应存在且位于本轮最后一个 tool 消息之后
	lastToolIdx := -1
	nudgeIdx := -1
	for i, m := range second {
		if m.Role == "tool" {
			lastToolIdx = i
		}
		if m.Role == "system" && strings.Contains(fmt.Sprintf("%v", m.Content), "信息较为有限") {
			nudgeIdx = i
		}
	}
	if nudgeIdx < 0 {
		t.Fatal("稀疏结果应触发一次 nudge")
	}
	if nudgeIdx < lastToolIdx {
		t.Fatalf("稀疏 nudge（@%d）不得插在本轮 tool 消息（最后 @%d）之间", nudgeIdx, lastToolIdx)
	}
}

// TestToolCallingLoop_ParallelToolCallsNoProgressNudgeKeepsPairing 回归：
// 并行轮中首个调用命中无进展 strike 时，advisory nudge 同样须延迟到本轮 tool 消息之后注入。
func TestToolCallingLoop_ParallelToolCallsNoProgressNudgeKeepsPairing(t *testing.T) {
	registry := NewToolRegistry()
	registerTinyTool(registry)
	loop := NewToolCallingLoop(registry, 5)
	mk := func(id, q string) llm.ChatChunk {
		return llm.ChatChunk{ToolCalls: []llm.ToolCall{
			{ID: id, Type: "function", Function: llm.ToolCallFunction{Name: "Tiny", Arguments: fmt.Sprintf(`{"q":%q}`, q)}},
		}}
	}
	client := &capturingPairingLLM{responses: []llm.ChatChunk{
		mk("a1", "x"), // 第 1 轮：单次调用
		{ToolCalls: []llm.ToolCall{ // 第 2 轮：并行，其中 a2 与 a1 同参同返回 -> strike
			{ID: "a2", Type: "function", Function: llm.ToolCallFunction{Name: "Tiny", Arguments: `{"q":"x"}`}},
			{ID: "a3", Type: "function", Function: llm.ToolCallFunction{Name: "Tiny", Arguments: `{"q":"y"}`}},
		}},
		{Delta: "done"},
	}}

	result, err := loop.Run(context.Background(), client, "m", nil, []llm.Message{{Role: "user", Content: "hi"}}, &ToolEnv{}, nil, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if result.FinalContent != "done" {
		t.Fatalf("最终回答不对: %v", result.FinalContent)
	}
	if len(client.requests) < 3 {
		t.Fatalf("应至少发起 3 次 LLM 调用，实际 %d", len(client.requests))
	}
	third := client.requests[2].Messages
	if verr := validateToolPairing(third); verr != nil {
		for i, m := range third {
			t.Logf("  [%d] role=%s toolCalls=%d toolCallID=%q", i, m.Role, len(m.ToolCalls), m.ToolCallID)
		}
		t.Fatalf("无进展 nudge 破坏了并行 tool_calls 配对: %v", verr)
	}
	nudged := false
	for _, m := range third {
		if m.Role == "system" && strings.Contains(fmt.Sprintf("%v", m.Content), "没有获得新信息") {
			nudged = true
		}
	}
	if !nudged {
		t.Fatal("无进展 strike 应注入一次 advisory nudge")
	}
}
