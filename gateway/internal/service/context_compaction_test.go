package service

import (
	"strings"
	"testing"

	"github.com/eleball/gateway/pkg/llm"
)

// TestCompactToolResult_SearchWeb 验证搜索结果提炼为 title+url+snippet 摘要
func TestCompactToolResult_SearchWeb(t *testing.T) {
	output := map[string]interface{}{
		"results": []map[string]interface{}{
			{"title": "Seedance API", "url": "https://example.com/1", "snippet": "视频生成 API 文档", "extra": "应被丢弃"},
			{"title": "Result2", "url": "https://example.com/2", "content": "长正文内容"},
		},
	}
	got := compactToolResult(output, "")
	if !strings.Contains(got, "Seedance API") || !strings.Contains(got, "example.com/1") {
		t.Fatalf("SearchWeb 提炼应保留 title/url，got: %s", got)
	}
	if strings.Contains(got, "应被丢弃") {
		t.Fatalf("SearchWeb 提炼应丢弃 extra 字段，got: %s", got)
	}
	if !strings.Contains(got, "视频生成 API 文档") {
		t.Fatalf("SearchWeb 提炼应保留 snippet，got: %s", got)
	}
}

// TestCompactToolResult_BudgetTruncation 验证超长输出按预算截断
func TestCompactToolResult_BudgetTruncation(t *testing.T) {
	long := strings.Repeat("a", 10000)
	output := map[string]interface{}{"data": long}
	got := compactToolResultWithBudget(output, "", 500)
	if len([]rune(got)) > 600 { // 截断后 + 省略号
		t.Fatalf("超长输出应按预算截断，got len: %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("截断应以省略号结尾，got: ...%s", got[len(got)-10:])
	}
}

// TestCompactToolResult_Error 验证错误信息保留并兜底截断
func TestCompactToolResult_Error(t *testing.T) {
	got := compactToolResult(nil, "网络超时")
	if !strings.Contains(got, "工具执行失败") || !strings.Contains(got, "网络超时") {
		t.Fatalf("错误信息应保留，got: %s", got)
	}
}

// TestCompactOldToolMessages_SlidingWindow 验证滑动窗口：最近 K 个保留，更早的压缩
func TestCompactOldToolMessages_SlidingWindow(t *testing.T) {
	// 构造 5 个 tool message + 对应 records
	records := make([]ToolCallRecord, 5)
	messages := make([]llm.Message, 0, 10)
	messages = append(messages, llm.Message{Role: "user", Content: "q"})
	for i := 0; i < 5; i++ {
		records[i] = ToolCallRecord{
			Step:      i + 1,
			Tool:      "ReadFile",
			Arguments: "{}",
			Output:    map[string]interface{}{"content": strings.Repeat("x", 3000)},
		}
		messages = append(messages, llm.Message{Role: "tool", ToolCallID: "tc" + string(rune('1'+i)), Content: strings.Repeat("x", 3000)})
	}

	out := compactOldToolMessages(messages, records, recentToolKeep)
	// 最近 3 个应保留原长（3000），最早 2 个应被压缩到 oldToolCompactBudget 以内
	toolCount := 0
	for _, m := range out {
		if m.Role != "tool" {
			continue
		}
		toolCount++
	}
	if toolCount != 5 {
		t.Fatalf("tool message 数量应不变，got: %d", toolCount)
	}
	// 最早的 2 个被压缩（out[1] 是第一个 tool message）
	earliest, _ := out[1].Content.(string)
	if len([]rune(earliest)) > oldToolCompactBudget+10 {
		t.Fatalf("最早 tool message 应被压缩到 %d 以内，got: %d", oldToolCompactBudget, len([]rune(earliest)))
	}
	// 最近的 3 个保留原长（out[4] 是最后一个 tool message）
	latest, _ := out[4].Content.(string)
	if len([]rune(latest)) != 3000 {
		t.Fatalf("最近 tool message 应保留原长 3000，got: %d", len([]rune(latest)))
	}
}

// TestCompactOldToolMessages_WithinWindow 验证 tool message 数 <= keep 时不压缩
func TestCompactOldToolMessages_WithinWindow(t *testing.T) {
	records := []ToolCallRecord{{Tool: "ReadFile", Output: map[string]interface{}{"content": "短内容"}}}
	messages := []llm.Message{
		{Role: "user", Content: "q"},
		{Role: "tool", Content: "短内容"},
	}
	out := compactOldToolMessages(messages, records, recentToolKeep)
	content, _ := out[1].Content.(string)
	if content != "短内容" {
		t.Fatalf("窗口内 tool message 不应被压缩，got: %s", content)
	}
}
