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
