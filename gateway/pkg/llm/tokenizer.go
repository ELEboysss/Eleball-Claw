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

// usageTokenBuffer 兜底估算安全垫（AR-19 P2-1，参考 OmniRoute usageTokenBuffer 默认 2000）。
// 上游漏返 usage 时，系统提示词 / 工具定义 / 历史轮次等隐含 token 易被低估，叠加固定安全垫防漏扣。
const usageTokenBuffer = 2000

// EstimateUsageFromMessages 根据请求消息与完成内容估算 usage（兜底，未知模型）。
// 兼容 string 与 []ContentPart 两种消息内容格式；委托 ForModel 走默认系数并叠加安全垫。
func EstimateUsageFromMessages(messages []Message, completion string) *Usage {
	return EstimateUsageFromMessagesForModel(messages, completion, "")
}

// EstimateUsageFromMessagesForModel 根据请求消息、完成内容与模型名估算 usage（AR-19 P2-1）。
// 按模型族施加估算系数（tokenEstimateCoefficient，保守 >=1.0 防漏扣）并叠加 usageTokenBuffer 安全垫。
// 仅在兜底路径（上游未返 usage）生效；上游返回真实 usage 时不经过此处。
func EstimateUsageFromMessagesForModel(messages []Message, completion, model string) *Usage {
	coeff := tokenEstimateCoefficient(model)
	promptTokens := 0
	for _, m := range messages {
		promptTokens += EstimateTokenCount(messageContentToString(m.Content))
	}
	completionTokens := EstimateTokenCount(completion)
	// AR-19 P2-1：按模型族系数调整（仅向上调整保计费安全），prompt 叠加安全垫防漏扣
	promptTokens = int(float64(promptTokens)*coeff) + usageTokenBuffer
	completionTokens = int(float64(completionTokens) * coeff)
	totalTokens := promptTokens + completionTokens
	return &Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	}
}

// tokenEstimateCoefficient 返回模型族的兜底估算系数（AR-19 P2-1，“按 provider 估算系数”方案）。
// 基础启发式对代码类内容低估（~4 字符/token，实际代码 ~3 字符/token）；系数仅向上调整（>=1.0）
// 以确保兜底计费不漏；CJK 按 1 token/字符已偏保守，不再上调。仅用于兜底估算路径。
func tokenEstimateCoefficient(model string) float64 {
	name := strings.ToLower(model)
	switch {
	case strings.Contains(name, "coder") || strings.Contains(name, "code"):
		return 1.2 // 代码模型 token 更密，上调避免低估
	default:
		return 1.0
	}
}

// MessageContentToString 将消息 content（string 或 []ContentPart）统一转为文本。
func MessageContentToString(content interface{}) string {
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

// messageContentToString 将消息 content（string 或 []ContentPart）统一转为文本（兼容旧调用）。
func messageContentToString(content interface{}) string {
	return MessageContentToString(content)
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
