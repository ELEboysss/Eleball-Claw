package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EstimateTokenCount 对文本做粗略的 token 估算兜底。
// 当上游模型不返回 usage 时，用此结果代替真实 token 数进行计费与统计。
//
// 估算规则：
//   - CJK 统一表意文字每个字符按 1 token 计算
//   - 其他字符每 4 个字符按 1 token 计算
//   - 空文本返回 0
//
// 该规则仅为兜底，精度远低于 tiktoken，但能避免完全漏记漏扣。
func EstimateTokenCount(text string) int {
	if text == "" {
		return 0
	}

	units := 0
	for _, r := range text {
		if isCJK(r) {
			units += 4
		} else {
			units += 1
		}
	}

	tokens := units / 4
	if tokens == 0 {
		return 1
	}
	return tokens
}

// EstimateUsageFromMessages 根据请求消息与完成内容估算 usage。
// 兼容 string 与 []ContentPart 两种消息内容格式。
func EstimateUsageFromMessages(messages []Message, completion string) *Usage {
	promptTokens := 0
	for _, m := range messages {
		promptTokens += EstimateTokenCount(messageContentToString(m.Content))
	}
	completionTokens := EstimateTokenCount(completion)
	totalTokens := promptTokens + completionTokens
	return &Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	}
}

// messageContentToString 将消息 content（string 或 []ContentPart）统一转为文本。
func messageContentToString(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	if parts, ok := content.([]ContentPart); ok {
		var sb strings.Builder
		for _, p := range parts {
			switch p.Type {
			case "text":
				sb.WriteString(p.Text)
			case "image_url":
				if p.ImageURL != nil {
					sb.WriteString(p.ImageURL.URL)
				}
			case "file":
				if p.File != nil {
					sb.WriteString(p.File.Text)
				}
			}
		}
		return sb.String()
	}
	// 兜底：序列化为 JSON 字符串
	b, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprintf("%v", content)
	}
	return string(b)
}

// isCJK 判断 rune 是否属于 CJK 统一表意文字区段。
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) ||
		(r >= 0x2B740 && r <= 0x2B81F) ||
		(r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x2F800 && r <= 0x2FA1F)
}

// EstimateTotalTokens 兼容旧调用：直接对文本做估算。
func EstimateTotalTokens(text string) int {
	return EstimateTokenCount(text)
}
