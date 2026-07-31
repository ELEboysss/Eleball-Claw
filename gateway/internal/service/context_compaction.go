package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/eleball/gateway/pkg/llm"
)

// AR-01 循环内上下文压缩（参考 providers/ai-agent-book「上下文工程」compaction/distillation、
// providers/jcode compaction-core、providers/OmniRoute contextManager）。
//
// 问题：tool 结果原 `json.Marshal` 全量回灌（agent_tool_loop.go toolResultToString），
// 多轮工具调用后 prompt tokens 随步数近似线性累加，最易撞上下文上限且成本超线性增长。
//
// 方案：按工具类型对 tool 结果做结构化提炼（只留关键字段），并对过长输出兜底截断。
// 与 agent_tool_summary.go 的区别：summary 用于持久化展示，compaction 用于回灌循环上下文。
// 本文件与 docs/agent-refactor-multi-llm-cost.md P1-5 的对话压缩层同属一个模块族，
// 对话级压缩（跨多轮 message 摘要）见 chat_proxy_service.go 的 truncateContextText 演进方向。

// compactToolResultBudget 单个工具结果回灌到循环上下文的字符预算。
// 超过即按工具类型提炼或截断，避免长输出（FetchURL 正文、Shell 输出）撑爆上下文。
const compactToolResultBudget = 4000

// compactToolResult 将工具执行结果提炼为回灌循环上下文的字符串（默认预算）。
func compactToolResult(output map[string]interface{}, errStr string) string {
	return compactToolResultWithBudget(output, errStr, compactToolResultBudget)
}

// compactToolResultWithBudget 按指定字符预算提炼工具结果。
// budget 较小时用于滑动窗口中"更早"轮次的进一步压缩。
func compactToolResultWithBudget(output map[string]interface{}, errStr string, budget int) string {
	if errStr != "" {
		// 错误信息保留以便模型换路，但仍做长度兜底
		return truncateRunes(fmt.Sprintf("工具执行失败: %s", errStr), budget)
	}
	if output == nil {
		return ""
	}

	// 工具类型可由 output 中的固定字段推断；按已知工具逐个尝试提炼
	if s := compactSearchWeb(output); s != "" {
		return truncateRunes(s, budget)
	}
	if s := compactFetchURL(output, budget); s != "" {
		return s
	}

	// 通用：JSON 序列化后按预算截断
	b, err := json.Marshal(output)
	if err != nil {
		return truncateRunes(fmt.Sprintf("%v", output), budget)
	}
	return truncateRunes(string(b), budget)
}

// compactSearchWeb 提炼搜索结果：每条只留 title + url + snippet 摘要，丢弃冗余字段。
func compactSearchWeb(output map[string]interface{}) string {
	raw, ok := output["results"]
	if !ok || raw == nil {
		return ""
	}
	results := toInterfaceSlice(raw)
	if results == nil {
		return ""
	}
	const maxItems = 8
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`{"results":[`))
	for i, r := range results {
		if i >= maxItems {
			b.WriteString(fmt.Sprintf(`],"truncated":true,"total":%d}`, len(results)))
			return b.String()
		}
		m, _ := r.(map[string]interface{})
		if m == nil {
			continue
		}
		if i > 0 {
			b.WriteString(",")
		}
		title := strVal(m, "title")
		url := strVal(m, "url")
		snippet := strVal(m, "snippet")
		if snippet == "" {
			snippet = strVal(m, "content")
		}
		b.WriteString(fmt.Sprintf(`{"title":%q,"url":%q,"snippet":%q}`, title, url, truncateRunes(snippet, 200)))
	}
	b.WriteString(`]}`)
	return b.String()
}

// compactFetchURL 提炼网页抓取结果：保留 title + url + 正文摘要（按预算截断）。
func compactFetchURL(output map[string]interface{}, budget int) string {
	content := strVal(output, "content")
	text := strVal(output, "text")
	if content == "" && text == "" {
		return ""
	}
	body := content
	if body == "" {
		body = text
	}
	title := strVal(output, "title")
	url := strVal(output, "url")
	bodyBudget := budget - 200
	if bodyBudget < 100 {
		bodyBudget = 100
	}
	return fmt.Sprintf(`{"title":%q,"url":%q,"content":%q}`, title, url, truncateRunes(body, bodyBudget))
}

// toInterfaceSlice 兼容 []interface{} / []map[string]interface{} / []SearchResult 等。
func toInterfaceSlice(raw interface{}) []interface{} {
	switch v := raw.(type) {
	case []interface{}:
		return v
	case []map[string]interface{}:
		out := make([]interface{}, len(v))
		for i, m := range v {
			out[i] = m
		}
		return out
	default:
		// 兜底：JSON 序列化/反序列化统一转 []interface{}
		b, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var arr []interface{}
		if err := json.Unmarshal(b, &arr); err != nil {
			return nil
		}
		return arr
	}
}

// strVal 安全取 map[string]interface{} 的字符串字段。
func strVal(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// truncateRunes 按 rune 截断到 maxLen，超出加省略号。
func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "…"
}

// recentToolKeep 滑动窗口大小：保留最近 N 个 tool message 的完整内容（默认预算压缩），
// 更早的进一步压缩为短预算（AR-01 滑动窗口）。
const recentToolKeep = 3

// oldToolCompactBudget 滑动窗口之外（更早）tool message 的短预算，进一步压缩上下文。
const oldToolCompactBudget = 600

// compactOldToolMessages 滑动窗口压缩：保留最近 keep 个 tool message 的当前内容，
// 更早的 tool message 用更短预算重新提炼（基于对应的 ToolCallRecord）。
// 在工具循环每轮末尾调用，控制 prompt tokens 随步数线性增长（AR-01）。
func compactOldToolMessages(messages []llm.Message, records []ToolCallRecord, keep int) []llm.Message {
	if keep < 0 {
		keep = 0
	}
	var toolIdx []int
	for i, m := range messages {
		if m.Role == "tool" {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= keep {
		return messages
	}
	// 需要压缩的是前 len-keep 个 tool message（最早的那批）
	compressCount := len(toolIdx) - keep
	for k := 0; k < compressCount; k++ {
		idx := toolIdx[k]
		// 第 k 个 tool message 对应第 k 个 record（按追加顺序一一对应）
		if k < len(records) {
			messages[idx].Content = compactToolResultWithBudget(records[k].Output, records[k].Error, oldToolCompactBudget)
		}
	}
	return messages
}
