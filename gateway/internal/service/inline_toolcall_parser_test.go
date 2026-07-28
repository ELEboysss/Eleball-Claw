package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseInlineFunctionCalls_NoMarker(t *testing.T) {
	calls, cleaned := parseInlineFunctionCalls("普通回答，没有标记")
	assert.Nil(t, calls)
	assert.Equal(t, "普通回答，没有标记", cleaned)
}

func TestParseInlineFunctionCalls_ArrayForm(t *testing.T) {
	content := `<|FunctionCallBegin|>[{"name":"search","parameters":{"query":"咕咕嘎嘎 梗"}}]<|FunctionCallEnd|>`
	calls, cleaned := parseInlineFunctionCalls(content)
	require.Len(t, calls, 1)
	assert.Equal(t, "search", calls[0].Function.Name)
	assert.Equal(t, "function", calls[0].Type)
	assert.NotEmpty(t, calls[0].ID)
	var args map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &args))
	assert.Equal(t, "咕咕嘎嘎 梗", args["query"])
	assert.Equal(t, "", cleaned)
}

func TestParseInlineFunctionCalls_SurroundingTextPreserved(t *testing.T) {
	content := "我先搜一下。<|FunctionCallBegin|>[{\"name\":\"search\",\"parameters\":{\"query\":\"x\"}}]<|FunctionCallEnd|>请稍等"
	calls, cleaned := parseInlineFunctionCalls(content)
	require.Len(t, calls, 1)
	assert.Equal(t, "我先搜一下。请稍等", cleaned)
}

func TestParseInlineFunctionCalls_SingleObjectForm(t *testing.T) {
	content := `<|FunctionCallBegin|>{"name":"ocr","arguments":{"image":"a.png"}}<|FunctionCallEnd|>`
	calls, _ := parseInlineFunctionCalls(content)
	require.Len(t, calls, 1)
	assert.Equal(t, "ocr", calls[0].Function.Name)
	var args map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &args))
	assert.Equal(t, "a.png", args["image"])
}

func TestParseInlineFunctionCalls_MissingEndMarker(t *testing.T) {
	content := `<|FunctionCallBegin|>[{"name":"search","parameters":{"query":"x"}}]`
	calls, _ := parseInlineFunctionCalls(content)
	require.Len(t, calls, 1)
	assert.Equal(t, "search", calls[0].Function.Name)
}

func TestParseInlineFunctionCalls_InvalidJSON(t *testing.T) {
	content := `<|FunctionCallBegin|>[{"name":]<|FunctionCallEnd|>`
	calls, cleaned := parseInlineFunctionCalls(content)
	assert.Nil(t, calls)
	assert.Equal(t, content, cleaned)
}

func TestParseInlineFunctionCalls_UniqueIDs(t *testing.T) {
	content := `<|FunctionCallBegin|>[{"name":"a","parameters":{}},{"name":"b","parameters":{}}]<|FunctionCallEnd|>`
	calls, _ := parseInlineFunctionCalls(content)
	require.Len(t, calls, 2)
	assert.NotEqual(t, calls[0].ID, calls[1].ID)
}

func TestParseBracketToolCalls_JSONObject(t *testing.T) {
	content := `[WriteFile]{"path":"hello.txt","content":"hi"}[/WriteFile]`
	calls, cleaned, malformed := parseBracketToolCalls(content)
	assert.False(t, malformed)
	require.Len(t, calls, 1)
	assert.Equal(t, "WriteFile", calls[0].Function.Name)
	assert.Equal(t, "function", calls[0].Type)
	assert.NotEmpty(t, calls[0].ID)
	var args map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &args))
	assert.Equal(t, "hello.txt", args["path"])
	assert.Equal(t, "hi", args["content"])
	assert.Equal(t, "", cleaned)
}

func TestParseBracketToolCalls_SnakeName(t *testing.T) {
	// 蛇形名原样返回，归一化由 tool loop 做
	calls, _, malformed := parseBracketToolCalls(`[write_file]{"path":"x"}[/write_file]`)
	assert.False(t, malformed)
	require.Len(t, calls, 1)
	assert.Equal(t, "write_file", calls[0].Function.Name)
}

func TestParseBracketToolCalls_MalformedNonJSON(t *testing.T) {
	// 形似标签但参数非 JSON 体 -> malformed，由调用方反馈 LLM 换结构化调用
	_, _, malformed := parseBracketToolCalls(`[WriteFile]文件内容[/WriteFile]`)
	assert.True(t, malformed)
}

func TestParseBracketToolCalls_MalformedTrailingText(t *testing.T) {
	// 有 [/ 但末尾非 ]（标签后带文字）-> malformed
	_, _, malformed := parseBracketToolCalls(`[WriteFile]{"path":"x"}[/WriteFile] extra`)
	assert.True(t, malformed)
}

func TestParseBracketToolCalls_NoCloseIsPlainText(t *testing.T) {
	// 有开标签无配对 [/...] 闭合 -> 当普通文本（不误判 [note] 等），非 malformed
	calls, _, malformed := parseBracketToolCalls(`[WriteFile]{"path":"x"}`)
	assert.False(t, malformed)
	assert.Nil(t, calls)
}

func TestParseBracketToolCalls_PlainText(t *testing.T) {
	calls, cleaned, malformed := parseBracketToolCalls("普通回答，没有标签")
	assert.False(t, malformed)
	assert.Nil(t, calls)
	assert.Equal(t, "普通回答，没有标签", cleaned)
}

func TestParseBracketToolCalls_ChineseTag(t *testing.T) {
	// 中文标签 [注意] 非工具名标识符 -> 普通文本，不误判
	calls, _, malformed := parseBracketToolCalls(`[注意]这是一段说明[/注意]`)
	assert.False(t, malformed)
	assert.Nil(t, calls)
}

func TestParseBracketToolCalls_MarkdownLink(t *testing.T) {
	// markdown [link](url) 无 [/...] 闭合 -> 普通文本
	calls, _, malformed := parseBracketToolCalls(`[link](https://example.com)`)
	assert.False(t, malformed)
	assert.Nil(t, calls)
}

func TestParseBracketToolCalls_PrefixedNotParsed(t *testing.T) {
	// 带前缀文字（非整段标签）-> 不解析，当普通文本（防误判正文）
	calls, _, malformed := parseBracketToolCalls(`我来写文件：[WriteFile]{"path":"x"}[/WriteFile]`)
	assert.False(t, malformed)
	assert.Nil(t, calls)
}

func TestParseInlineFunctionCalls_LiteralNewlineInString(t *testing.T) {
	// 复现 MiMo 等模型在字符串参数值内直接输出未转义字面换行的场景：
	// content 字段值跨多行（字面 \n），标准 JSON 解析会报 "invalid character '\n'
	// in string literal" 失败。parser 应容错转义后解析，否则整段标记被透传给用户
	// （表现为工具调用没执行）。见 debugs/log。
	content := "<|FunctionCallBegin|>[{\"name\":\"WriteFile\",\"parameters\":{\"path\":\"x.md\",\"content\":\"# 标题\n\n正文第二行\"}}]<|FunctionCallEnd|>"
	calls, cleaned := parseInlineFunctionCalls(content)
	require.Len(t, calls, 1)
	assert.Equal(t, "WriteFile", calls[0].Function.Name)
	assert.Equal(t, "", cleaned)
	var args map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &args))
	assert.Equal(t, "x.md", args["path"])
	assert.Equal(t, "# 标题\n\n正文第二行", args["content"])
}

func TestParseBracketToolCalls_LiteralNewlineInString(t *testing.T) {
	// 方括号标签格式同样兼容字符串值内的字面换行
	content := "[WriteFile]{\"path\":\"x.md\",\"content\":\"行1\n行2\"}[/WriteFile]"
	calls, _, malformed := parseBracketToolCalls(content)
	assert.False(t, malformed)
	require.Len(t, calls, 1)
	assert.Equal(t, "WriteFile", calls[0].Function.Name)
	var args map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &args))
	assert.Equal(t, "行1\n行2", args["content"])
}

func TestParseBareJSONToolCalls_ArrayForm(t *testing.T) {
	// 裸 JSON 数组工具调用（无任何标记包裹），复现 debugs/log 场景
	content := `[{"name":"com_eleball_tools_search_web_baidu","parameters":{"query":"咕咕嘎嘎 梗"}}]`
	calls, cleaned := parseBareJSONToolCalls(content)
	require.Len(t, calls, 1)
	assert.Equal(t, "com_eleball_tools_search_web_baidu", calls[0].Function.Name)
	assert.Equal(t, "function", calls[0].Type)
	assert.NotEmpty(t, calls[0].ID)
	assert.Equal(t, "", cleaned)
	var args map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &args))
	assert.Equal(t, "咕咕嘎嘎 梗", args["query"])
}

func TestParseBareJSONToolCalls_SingleObject(t *testing.T) {
	content := `{"name":"ocr","arguments":{"image":"a.png"}}`
	calls, _ := parseBareJSONToolCalls(content)
	require.Len(t, calls, 1)
	assert.Equal(t, "ocr", calls[0].Function.Name)
	var args map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &args))
	assert.Equal(t, "a.png", args["image"])
}

func TestParseBareJSONToolCalls_PrettyPrinted(t *testing.T) {
	// 多行缩进的 pretty JSON（日志里的实际形态）
	content := `[
  {
    "name": "search",
    "parameters": {
      "query": "x"
    }
  }
]`
	calls, _ := parseBareJSONToolCalls(content)
	require.Len(t, calls, 1)
	assert.Equal(t, "search", calls[0].Function.Name)
}

func TestParseBareJSONToolCalls_LiteralNewline(t *testing.T) {
	// parameters 值内字面换行（未转义）也应容错
	content := "{\"name\":\"WriteFile\",\"parameters\":{\"path\":\"x.md\",\"content\":\"行1\n行2\"}}"
	calls, _ := parseBareJSONToolCalls(content)
	require.Len(t, calls, 1)
	var args map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &args))
	assert.Equal(t, "行1\n行2", args["content"])
}

func TestParseBareJSONToolCalls_NonIdentName(t *testing.T) {
	// name 含空格等非标识符 -> 不解析（防误判）
	content := `{"name":"not a tool","parameters":{}}`
	calls, cleaned := parseBareJSONToolCalls(content)
	assert.Nil(t, calls)
	assert.Equal(t, content, cleaned)
}

func TestParseBareJSONToolCalls_JSONDataNotTool(t *testing.T) {
	// 普通 JSON 数据（无 name 字段）-> 不解析，当普通文本（防误判正文里的 JSON 数据）
	content := `{"id":1,"items":["a","b"]}`
	calls, cleaned := parseBareJSONToolCalls(content)
	assert.Nil(t, calls)
	assert.Equal(t, content, cleaned)
}

func TestParseBareJSONToolCalls_PlainText(t *testing.T) {
	calls, cleaned := parseBareJSONToolCalls("普通回答，没有 JSON")
	assert.Nil(t, calls)
	assert.Equal(t, "普通回答，没有 JSON", cleaned)
}

func TestParseBareJSONToolCalls_PrefixedNotParsed(t *testing.T) {
	// 带前缀文字（非整段 JSON）-> 不解析，当普通文本（防误判正文）
	calls, _ := parseBareJSONToolCalls(`我来搜一下：[{"name":"search","parameters":{}}]`)
	assert.Nil(t, calls)
}
