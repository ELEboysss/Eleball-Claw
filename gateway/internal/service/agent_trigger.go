package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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
		if hasImage(attachments) {
			result.NeedsOCR = true
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

// PreprocessAttachments 预处理附件：模型无关的物化与校验。
// 图片确保 DataURL 存在并校验大小；文本类文件若无 Text 则从 data:text URI 解码；
// 超出大小限制或缺少必要数据时返回错误。OCR 降级延迟到 buildInitialMessages（需 vision 信息）。
func (t *AgentTrigger) PreprocessAttachments(ctx context.Context, attachments []AgentAttachment, conversationID string) ([]AgentAttachment, error) {
	if len(attachments) == 0 {
		return attachments, nil
	}
	out := make([]AgentAttachment, len(attachments))
	for i, att := range attachments {
		switch att.Type {
		case "image":
			if att.DataURL == "" {
				return nil, fmt.Errorf("图片附件 %q 缺少 dataUrl", att.Name)
			}
			if size := dataURIDecodedSize(att.DataURL); size > MaxAttachmentImageBytes {
				return nil, fmt.Errorf("图片附件 %q 过大（%d 字节，上限 %d）", att.Name, size, MaxAttachmentImageBytes)
			}
		case "file":
			// 文本类文件：Text 已由前端填充；缺失时尝试从 data:text URI 解码
			if att.Text == "" && strings.HasPrefix(att.DataURL, "data:text") {
				if decoded, _, err := decodeDataURI(att.DataURL); err == nil {
					att.Text = string(decoded)
				}
			}
			if att.Text != "" && len(att.Text) > MaxAttachmentTextBytes {
				return nil, fmt.Errorf("文本附件 %q 过大（%d 字节，上限 %d）", att.Name, len(att.Text), MaxAttachmentTextBytes)
			}
		}
		out[i] = att
	}
	return out, nil
}

const (
	// MaxAttachmentImageBytes 单张图片 data URI 解码后上限（20MB；前端已对大图压缩，此为兜底）
	MaxAttachmentImageBytes = 20 * 1024 * 1024
	// MaxAttachmentTextBytes 单个文本附件内容上限（1MB，避免 token 爆炸）
	MaxAttachmentTextBytes = 1024 * 1024
)

// decodeDataURI 解析 data:[<mediatype>][;base64],<data> 形式的 data URI。
// 返回解码后的字节与 MIME 类型。非 data URI 或格式错误返回错误。
// PreprocessAttachments（文本解码）与 ToolRegistry.OCRDataURI（图片落盘 OCR）共用。
func decodeDataURI(dataURI string) (data []byte, mimeType string, err error) {
	const prefix = "data:"
	if !strings.HasPrefix(dataURI, prefix) {
		return nil, "", errors.New("不是 data URI")
	}
	rest := dataURI[len(prefix):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return nil, "", errors.New("data URI 缺少逗号分隔符")
	}
	meta := rest[:comma]
	dataPart := rest[comma+1:]
	isBase64 := false
	mimeType = "text/plain"
	for _, seg := range strings.Split(meta, ";") {
		if seg == "base64" {
			isBase64 = true
		} else if strings.Contains(seg, "/") {
			mimeType = seg
		}
	}
	if isBase64 {
		data, err = base64.StdEncoding.DecodeString(dataPart)
		if err != nil {
			return nil, "", fmt.Errorf("base64 解码失败: %w", err)
		}
	} else {
		data = []byte(dataPart)
	}
	return data, mimeType, nil
}

// dataURIDecodedSize 估算 data URI 解码后的字节数（避免完整解码大图做大小校验）。
func dataURIDecodedSize(dataURI string) int {
	comma := strings.LastIndexByte(dataURI, ',')
	if comma < 0 {
		return len(dataURI)
	}
	enc := dataURI[comma+1:]
	if strings.Contains(dataURI[:comma], "base64") {
		return len(enc) * 3 / 4 // base64 解码后约为编码长度的 3/4
	}
	return len(enc)
}

// AgentAttachment Agent 工作流附件。
// 字段对齐前端 fileToContentPart 实际产出（C7 修复：原 mime/content 与前端 mimeType/dataUrl/text 不匹配致内容丢失）。
type AgentAttachment struct {
	ID       string `json:"id"`
	Type     string `json:"type"`                // image / file（文本类文件统一为 file，内容置 Text）
	Name     string `json:"name"`
	MimeType string `json:"mimeType,omitempty"`  // MIME 类型
	Size     int64  `json:"size,omitempty"`
	DataURL  string `json:"dataUrl,omitempty"`   // 图片/二进制 data URI（data:image/...;base64,...），或 claw 本地路径
	Text     string `json:"text,omitempty"`      // 文本类文件内容
	URL      string `json:"url,omitempty"`       // 公开 URL（未来扩展）
}

// hasImage 判断附件列表中是否含图片（Detect / 降级判断复用）
func hasImage(attachments []AgentAttachment) bool {
	for _, att := range attachments {
		if att.Type == "image" {
			return true
		}
	}
	return false
}
