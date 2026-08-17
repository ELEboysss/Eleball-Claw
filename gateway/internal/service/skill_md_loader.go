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
// YAML frontmatter（name/description/可选 metadata 等）+ Markdown body（能力指令 / 人格 prompt）。
// 本项目把「只有 SKILL.md 无 module.json」的 marketplace 目录注册为 prompt-only 秘技
// （driver=none）：1 SKILL.md = 1 SKU，body 即 SystemPrompt。Anthropic 标准 SKILL.md
// 可直接丢进 marketplace/ 即用，无需适配层（详见 plan: mcp-skill-standard-alignment）。
//
// 解析策略 = 全字段透传保留：除必填 name/description 外，Anthropic Agent Skills 规范
// 与 DSH/Claude Code 扩展的可选字段（license/allowed-tools/compatibility/when-to-use/
// disable-model-invocation/user-invocable）全部解析保留，暂不接入调用语义（注入策略
// 仍由购买/激活开关决定）；未识别字段由 yaml 忽略，保证任何标准 SKILL.md 吃进不报错。
type SkillMDManifest struct {
	// Name 技能标识（slug），frontmatter.name，必填
	Name string `yaml:"name"`
	// Description 触发条件描述，frontmatter.description，必填；兼作 SKU 描述
	Description string `yaml:"description"`
	// License 许可证（Anthropic 规范可选字段），原样保留
	License string `yaml:"license"`
	// AllowedTools 允许工具清单（Anthropic 规范可选字段，空格分隔串），原样保留
	AllowedTools string `yaml:"allowed-tools"`
	// Compatibility 运行环境要求（Anthropic 规范可选字段），原样保留
	Compatibility string `yaml:"compatibility"`
	// DisableModelInvocation 禁用模型自动调用（DSH/Claude Code 扩展）；nil=未声明。
	// 解析保留，注入语义待接入（当前激活即注入）。
	DisableModelInvocation *bool `yaml:"disable-model-invocation"`
	// UserInvocable 用户可显式调用（DSH 扩展，对应 user-invocable）；nil=未声明，解析保留。
	UserInvocable *bool `yaml:"user-invocable"`
	// WhenToUse 使用时机提示（DSH 扩展 whenToUse / 连字符变体 when-to-use 均接受），原样保留。
	WhenToUse string `yaml:"when-to-use"`
	// WhenToUseCamel 接受 camelCase 写法（DSH skill-filesystem 的 whenToUse），
	// 解析后并入 WhenToUse（连字符写法优先），调用方应只读 WhenToUse。
	// 注意 yaml.v3 的 tag 按字面大小写匹配（实测），且未导出字段不会被 yaml 写入，
	// 故该合并源字段必须导出并使用 camelCase tag。
	WhenToUseCamel string `yaml:"whenToUse"`
	// Metadata frontmatter.metadata（version/category/title 等自由字段），透传不强约束
	Metadata map[string]interface{} `yaml:"metadata"`
	// Body Markdown body，prompt-only SKU 的 SystemPrompt 来源；非 frontmatter 字段
	Body string `yaml:"-"`
}

// DisplayName 返回 SKU 展示名：frontmatter metadata.title 非空时优先（SKILL.md
// 的 name 必须是 slug，中文展示名经 metadata.title 表达），否则回退 Name。
func (m *SkillMDManifest) DisplayName() string {
	if m.Metadata != nil {
		if t, ok := m.Metadata["title"].(string); ok && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	return m.Name
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
// --- 之后的全部内容（TrimSpace 首尾）。camelCase 的 whenToUse 在连字符写法缺省时并入 WhenToUse。
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
	if m.WhenToUse == "" {
		m.WhenToUse = m.WhenToUseCamel
	}
	if strings.TrimSpace(m.Name) == "" {
		return nil, fmt.Errorf("SKILL.md frontmatter 缺少必填字段 name")
	}
	if strings.TrimSpace(m.Description) == "" {
		return nil, fmt.Errorf("SKILL.md frontmatter 缺少必填字段 description")
	}
	return &m, nil
}
