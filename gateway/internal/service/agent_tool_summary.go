package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// buildToolSummary 根据工具调用记录生成简短文本摘要，用于写入对话历史上下文。
// 摘要控制长度，避免完整 tool output 撑爆上下文窗口。
func buildToolSummary(records []ToolCallRecord) string {
	if len(records) == 0 {
		return ""
	}

	var parts []string
	for _, r := range records {
		line := summarizeToolCallRecord(r)
		parts = append(parts, line)
	}

	summary := strings.Join(parts, "\n")
	const maxSummaryLen = 1000
	if len(summary) > maxSummaryLen {
		summary = summary[:maxSummaryLen] + "\n[工具摘要已截断]"
	}
	return summary
}

// summarizeToolCallRecord 针对单个工具调用记录生成一行摘要
func summarizeToolCallRecord(record ToolCallRecord) string {
	if record.Error != "" {
		return fmt.Sprintf("[%s] 调用失败: %s", record.Tool, truncateText(record.Error, 80))
	}

	switch record.Tool {
	case "SearchWeb":
		return summarizeSearchWeb(record.Arguments, record.Output)
	case "FetchURL":
		return summarizeFetchURL(record.Arguments, record.Output)
	default:
		return fmt.Sprintf("[%s] 执行成功", record.Tool)
	}
}

// summarizeSearchWeb 摘要搜索工具结果
func summarizeSearchWeb(arguments string, output map[string]interface{}) string {
	query := ""
	if arguments != "" {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(arguments), &args); err == nil {
			if q, ok := args["query"].(string); ok {
				query = q
			}
		}
	}

	count := countResults(output)

	if query != "" {
		return fmt.Sprintf("[SearchWeb] 搜索 \"%s\"，获得 %d 条结果", query, count)
	}
	return fmt.Sprintf("[SearchWeb] 获得 %d 条结果", count)
}

// countResults 统计搜索结果数量，兼容 []interface{} 与 []SearchResult 两种实际类型。
// 搜索源返回的是 []SearchResult，直接断言 []interface{} 会失败，导致结果永远显示 0 条。
func countResults(output map[string]interface{}) int {
	if output == nil {
		return 0
	}
	raw, ok := output["results"]
	if !ok || raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case []interface{}:
		return len(v)
	case []SearchResult:
		return len(v)
	case []map[string]interface{}:
		return len(v)
	default:
		// 兜底：通过 JSON 序列化/反序列化统一转成 []interface{} 再统计
		b, err := json.Marshal(raw)
		if err != nil {
			return 0
		}
		var arr []interface{}
		if err := json.Unmarshal(b, &arr); err != nil {
			return 0
		}
		return len(arr)
	}
}

// summarizeFetchURL 摘要网页抓取工具结果
func summarizeFetchURL(arguments string, output map[string]interface{}) string {
	urlStr := ""
	if arguments != "" {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(arguments), &args); err == nil {
			if u, ok := args["url"].(string); ok {
				urlStr = u
			}
		}
	}
	if urlStr == "" && output != nil {
		if u, ok := output["url"].(string); ok {
			urlStr = u
		}
	}

	title := ""
	if output != nil {
		if t, ok := output["title"].(string); ok {
			title = t
		}
	}

	if title != "" && urlStr != "" {
		return fmt.Sprintf("[FetchURL] 已抓取 \"%s\"（%s）", title, truncateText(urlStr, 60))
	}
	if urlStr != "" {
		return fmt.Sprintf("[FetchURL] 已抓取 %s", truncateText(urlStr, 60))
	}
	return "[FetchURL] 已抓取网页"
}

// truncateText 截断文本到指定长度
func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "…"
}
