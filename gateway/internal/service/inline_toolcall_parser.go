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
	// 容错：部分模型（如 MiMo 系列）在字符串参数值内直接输出字面换行/制表符（未转义），
	// 标准 JSON 禁止字符串值内出现字面控制字符，encoding/json 会报 "invalid character
	// '\n' in string literal" 导致整段标记解析失败、被当作最终回答透传给用户（表现为
	// 工具调用没执行）。先转义字符串值内的控制字符再解析；字符串外的结构空白原样保留。
	payload = escapeControlInJSONStrings(payload)

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
	payload = escapeControlInJSONStrings(payload)
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

// escapeControlInJSONStrings 把 JSON 文本中字符串值内的字面控制字符（U+0000~U+001F）
// 转义为合法 JSON 转义序列，兼容部分模型在字符串参数里直接输出未转义换行的情形。
//
// 仅转义字符串值内部的控制字符；字符串外的结构空白（token 间的换行/空格）原样保留，
// 因为它们是合法 JSON 分隔符。字节级扫描，UTF-8 多字节字符（如中文）的高位字节
// （>=0x80）不落入各控制字符分支，原样透传。
func escapeControlInJSONStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inStr := false
	escaped := false
	const hex = "0123456789abcdef"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !inStr {
			b.WriteByte(c)
			if c == '"' {
				inStr = true
			}
			continue
		}
		if escaped { // 上一字节是反斜杠，本字节原样保留（如 \" \\ \n 已是转义序列）
			b.WriteByte(c)
			escaped = false
			continue
		}
		switch c {
		case '\\':
			b.WriteByte(c)
			escaped = true
		case '"':
			b.WriteByte(c)
			inStr = false
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if c < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0x0f])
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}
