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

// parseBracketToolCalls 解析形如 [ToolName]{json}[/ToolName] 的方括号标签工具调用。
//
// 部分模型（尤其中小厂商 / BYOK 弱模型）不走结构化 tool_calls，而在正文里输出
// [WriteFile]{"path":"x","content":"..."}[/WriteFile] 这类伪 XML 标签。不解析时
// Agent 循环会把整段标签当作最终回答透传给用户（表现为「调用了却没执行」）。
//
// 严格约束（借鉴 jcode chatgpt_web 信封解析，防误判正文方括号）：
//   - 整段 response（TrimSpace 后）必须形如 [name]{json}[/name]，前后不得有其它正文
//   - name 须匹配 [A-Za-z_][A-Za-z0-9_]*（工具名风格，排除中文标签 [注意] / markdown [link] 等误判）
//   - 参数须为合法 JSON 对象；非法 JSON -> malformed=true，由调用方反馈 LLM 换结构化调用
//
// 返回：解析出的 tool_calls（name 原样，归一化由 tool loop 做）、剥离标签后的正文、
// 是否检测到形似标签但格式错误（malformed）。无配对标签形态返回 nil, 原 content, false。
func parseBracketToolCalls(content string) (calls []llm.ToolCall, cleaned string, malformed bool) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "[") {
		return nil, content, false
	}
	openEnd := strings.Index(trimmed, "]")
	if openEnd < 0 {
		return nil, content, false
	}
	name := trimmed[1:openEnd]
	if !isToolNameIdent(name) {
		return nil, content, false // [link]/[注意] 等非工具名标签，按普通文本
	}
	closeStart := strings.LastIndex(trimmed, "[/")
	if closeStart <= openEnd {
		return nil, content, false // 无配对 [/...] 闭合，按普通文本（不误判 [note] 等）
	}
	if !strings.HasSuffix(trimmed, "]") {
		return nil, content, true // 有 [/ 但末尾非 ]，格式错
	}
	closeName := trimmed[closeStart+2 : len(trimmed)-1]
	if !isToolNameIdent(closeName) {
		return nil, content, true
	}
	payload := strings.TrimSpace(trimmed[openEnd+1 : closeStart])
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &args); err != nil {
		return nil, content, true // 非法 JSON -> malformed，反馈 LLM 换方式
	}
	raw, _ := json.Marshal(args)
	seq := atomic.AddUint64(&inlineCallSeq, 1)
	calls = []llm.ToolCall{{
		ID:   fmt.Sprintf("inline_bracket_%d", seq),
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      name,
			Arguments: string(raw),
		},
	}}
	return calls, "", false
}

// isToolNameIdent 判断是否为工具名风格标识符 [A-Za-z_][A-Za-z0-9_]*
func isToolNameIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		isAlpha := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		isDigit := r >= '0' && r <= '9'
		if i == 0 {
			if !(isAlpha || r == '_') {
				return false
			}
		} else if !(isAlpha || isDigit || r == '_') {
			return false
		}
	}
	return true
}
