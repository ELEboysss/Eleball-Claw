package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/eleball/gateway/pkg/llm"
)

// 内联函数调用标记兼容解析。
//
// 部分模型（如小米 MiMo 系列）不使用 OpenAI 结构化 tool_calls 字段，
// 而是在正文输出 <|FunctionCallBegin|>[{"name":"...","parameters":{...}}]<|FunctionCallEnd|>。
// 不解析时 Agent 循环会把整段标记当作最终回答透传给用户（表现为「调用出错」）。
// 这里将其还原为真实 tool_calls，并从正文中剥离标记。

const (
	inlineCallBegin = "<|FunctionCallBegin|>"
	inlineCallEnd   = "<|FunctionCallEnd|>"
)

// inlineCallSeq 生成进程内唯一的 tool_call ID（同一会话多轮内联调用不可重号，
// 否则严格上游会因 tool_call_id 冲突拒绝请求）
var inlineCallSeq uint64

// inlineFunctionCall 内联标记中的单个调用；parameters/arguments 两种键名均兼容
type inlineFunctionCall struct {
	Name       string                 `json:"name"`
	Parameters map[string]interface{} `json:"parameters"`
	Arguments  map[string]interface{} `json:"arguments"`
}

// parseInlineFunctionCalls 从正文解析内联函数调用标记。
// 返回解析出的 tool_calls 与剥离标记后的正文；无标记或解析失败返回 nil 与原正文。
func parseInlineFunctionCalls(content string) ([]llm.ToolCall, string) {
	idx := strings.Index(content, inlineCallBegin)
	if idx < 0 {
		return nil, content
	}
	rest := content[idx+len(inlineCallBegin):]
	var payload, tail string
	if endIdx := strings.Index(rest, inlineCallEnd); endIdx >= 0 {
		payload = rest[:endIdx]
		tail = rest[endIdx+len(inlineCallEnd):]
	} else {
		// 结束标记缺失（输出被截断）：按整个剩余内容尝试解析
		payload = rest
	}
	payload = strings.TrimSpace(payload)

	var calls []inlineFunctionCall
	if err := json.Unmarshal([]byte(payload), &calls); err != nil || len(calls) == 0 {
		// 兼容单个对象（非数组）形式
		var single inlineFunctionCall
		if err2 := json.Unmarshal([]byte(payload), &single); err2 != nil || single.Name == "" {
			return nil, content
		}
		calls = []inlineFunctionCall{single}
	}

	out := make([]llm.ToolCall, 0, len(calls))
	for _, c := range calls {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			return nil, content
		}
		args := c.Parameters
		if args == nil {
			args = c.Arguments
		}
		if args == nil {
			args = map[string]interface{}{}
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, content
		}
		seq := atomic.AddUint64(&inlineCallSeq, 1)
		out = append(out, llm.ToolCall{
			ID:   fmt.Sprintf("inline_call_%d", seq),
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      name,
				Arguments: string(raw),
			},
		})
	}
	cleaned := strings.TrimSpace(content[:idx] + tail)
	return out, cleaned
}
