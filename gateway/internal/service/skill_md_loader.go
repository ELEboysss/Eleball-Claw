package service

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillMDManifest Anthropic 标准 SKILL.md 解析结果。
//
// SKILL.md 是业界 Agent Skills 开放标准（Anthropic 提出，跨 Claude/Cursor 等 17 平台）：
// YAML frontmatter（name/description/可选 metadata）+ Markdown body（能力指令 / 人格 prompt）。
// 本项目把「只有 SKILL.md 无 module.json」的 marketplace 目录注册为 prompt-only 秘技
// （driver=none）：1 SKILL.md = 1 SKU，body 即 SystemPrompt。Anthropic 标准 SKILL.md
// 可直接丢进 marketplace/ 即用，无需适配层（详见 plan: mcp-skill-standard-alignment）。
type SkillMDManifest struct {
	// Name 技能标识（slug），frontmatter.name，必填
	Name string `yaml:"name"`
	// Description 触发条件描述，frontmatter.description，必填；兼作 SKU 描述
	Description string `yaml:"description"`
	// Metadata frontmatter.metadata（version 等自由字段），透传不强约束
	Metadata map[string]interface{} `yaml:"metadata"`
	// Body Markdown body，prompt-only SKU 的 SystemPrompt 来源；非 frontmatter 字段
	Body string `yaml:"-"`
}

// ParseSkillMD 读取并解析 SKILL.md 文件，返回 frontmatter + body。
// frontmatter 必须以 --- 起始分隔，含必填 name/description；缺则返回 error（调用方 warn 跳过）。
// 解析风格对齐 loadPromptTemplate（slash_command_service.go）与 readRuleFrontmatterPaths
// （context_file_service.go）：SplitN(text,"---",3) 切 frontmatter/body，yaml.Unmarshal 前半。
func ParseSkillMD(path string) (*SkillMDManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 SKILL.md 失败: %w", err)
	}
	return ParseSkillMDContent(data)
}

// ParseSkillMDContent 从字节内容解析 SKILL.md（供测试与内存态复用）。
// 要求首部即 --- 起始分隔（对齐 readRuleFrontmatterPaths，不容忍前置文本）；body 为闭合
// --- 之后的全部内容（TrimSpace 首尾）。
func ParseSkillMDContent(data []byte) (*SkillMDManifest, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return nil, fmt.Errorf("SKILL.md 缺少 frontmatter 起始分隔符 ---")
	}
	// SplitN 按 "---" 切 3 段：前(空) / frontmatter / body。body 内的 --- 保留在 parts[2]。
	parts := strings.SplitN(text, "---", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("SKILL.md frontmatter 未闭合（缺少结束 ---）")
	}
	var m SkillMDManifest
	if err := yaml.Unmarshal([]byte(parts[1]), &m); err != nil {
		return nil, fmt.Errorf("SKILL.md frontmatter 解析失败: %w", err)
	}
	m.Body = strings.TrimSpace(parts[2])
	if strings.TrimSpace(m.Name) == "" {
		return nil, fmt.Errorf("SKILL.md frontmatter 缺少必填字段 name")
	}
	if strings.TrimSpace(m.Description) == "" {
		return nil, fmt.Errorf("SKILL.md frontmatter 缺少必填字段 description")
	}
	return &m, nil
}
