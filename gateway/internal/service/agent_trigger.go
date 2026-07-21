package service

import (
	"context"
	"strings"
)

// AgentTrigger 负责判断用户请求是否需要进入 Agent 模式，以及附件预处理
// 注意：最终是否调用工具由用户显式开关（enable_tools）决定，本模块只做辅助检测

type AgentTrigger struct{}

// NewAgentTrigger 创建触发器
func NewAgentTrigger() *AgentTrigger {
	return &AgentTrigger{}
}

// TriggerResult 触发检测结果
type TriggerResult struct {
	HasTriggerKeywords bool
	NeedsServerTools   bool
	NeedsOCR           bool
}

// Detect 检测是否需要 Agent 模式相关处理
func (t *AgentTrigger) Detect(input string, attachments []AgentAttachment) TriggerResult {
	result := TriggerResult{}

	if len(attachments) > 0 {
		result.NeedsServerTools = true
		for _, att := range attachments {
			if att.Type == "image" {
				result.NeedsOCR = true
				break
			}
		}
	}

	triggerKeywords := []string{
		"搜索", "查一下", "读取", "修改", "生成视频", "分析图片", "OCR", "运行",
		"最新", "今天", "2026", "多少钱", "官网",
	}
	for _, k := range triggerKeywords {
		if strings.Contains(input, k) {
			result.HasTriggerKeywords = true
			break
		}
	}

	// 明确需要服务器端工具的关键词（非 VIP 用户调用时应提示升级）
	serverToolKeywords := []string{
		"查一下", "读取", "修改", "生成视频", "分析图片", "OCR", "运行",
	}
	for _, k := range serverToolKeywords {
		if strings.Contains(input, k) {
			result.NeedsServerTools = true
			break
		}
	}

	return result
}

// PreprocessAttachments 预处理附件（占位实现）
func (t *AgentTrigger) PreprocessAttachments(ctx context.Context, attachments []AgentAttachment, conversationID string) ([]AgentAttachment, error) {
	// TODO: 实际实现图片 OCR、文本文件读取等预处理
	// 当前仅透传附件
	return attachments, nil
}

// AgentAttachment Agent 工作流附件
type AgentAttachment struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // text / image / video / audio / file
	Name    string `json:"name"`
	Mime    string `json:"mime"`
	Size    int64  `json:"size"`
	URL     string `json:"url,omitempty"`
	Content string `json:"content,omitempty"`
}
