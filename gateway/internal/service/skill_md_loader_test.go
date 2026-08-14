package service

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillMD_Valid(t *testing.T) {
	m, err := ParseSkillMD(filepath.Join("testdata", "skillmd", "valid.md"))
	if err != nil {
		t.Fatalf("解析 valid.md 失败: %v", err)
	}
	if m.Name != "copywriting" {
		t.Errorf("Name = %q, want copywriting", m.Name)
	}
	if !strings.Contains(m.Description, "write copy for") {
		t.Errorf("Description 未含触发词: %q", m.Description)
	}
	// body 含标题与末尾 Output 段
	if !strings.Contains(m.Body, "# Copywriting") {
		t.Errorf("Body 未含 # Copywriting: %q", m.Body)
	}
	if !strings.Contains(m.Body, "## Output") {
		t.Errorf("Body 未含 ## Output: %q", m.Body)
	}
	// body 内的 ---（水平分隔线）应保留在 parts[2]，不被误当结束分隔
	if !strings.Contains(m.Body, "---") {
		t.Errorf("Body 内的 --- 水平线未被保留: %q", m.Body)
	}
	// metadata 透传：version 值 yaml 可能解析为 string，统一 %v 比对
	if v, ok := m.Metadata["version"]; !ok {
		t.Errorf("metadata.version 缺失: %v", m.Metadata)
	} else if got := fmt.Sprintf("%v", v); !strings.Contains(got, "1.1.0") {
		t.Errorf("metadata.version = %q, want 含 1.1.0", got)
	}
	if got := fmt.Sprintf("%v", m.Metadata["author"]); !strings.Contains(got, "eleball") {
		t.Errorf("metadata.author = %q, want 含 eleball", got)
	}
}

func TestParseSkillMD_NoFrontmatter(t *testing.T) {
	_, err := ParseSkillMD(filepath.Join("testdata", "skillmd", "no_frontmatter.md"))
	if err == nil {
		t.Fatal("无 frontmatter 应返回 error")
	}
	if !strings.Contains(err.Error(), "frontmatter") {
		t.Errorf("error 未提及 frontmatter: %v", err)
	}
}

func TestParseSkillMD_MissingName(t *testing.T) {
	_, err := ParseSkillMD(filepath.Join("testdata", "skillmd", "missing_name.md"))
	if err == nil {
		t.Fatal("缺 name 应返回 error")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error 未提及 name: %v", err)
	}
}

func TestParseSkillMDContent_Inline(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string // 空串表示不应报错
	}{
		{
			name:    "缺 description",
			content: "---\nname: only-name\n---\nbody",
			wantErr: "description",
		},
		{
			name:    "frontmatter 未闭合",
			content: "---\nname: x\ndescription: y\nbody without closing",
			wantErr: "闭合",
		},
		{
			name:    "前置文本拒绝",
			content: "前置文本\n---\nname: x\ndescription: y\n---\nbody",
			wantErr: "frontmatter",
		},
		{
			name:    "合法最小 SKILL.md",
			content: "---\nname: min-skill\ndescription: min desc\n---\n# Body\n",
			wantErr: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := ParseSkillMDContent([]byte(c.content))
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("不应报错: %v", err)
				}
				if m == nil || m.Name == "" || m.Body == "" {
					t.Fatal("解析结果为空")
				}
				return
			}
			if err == nil {
				t.Fatalf("应报错含 %q", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error 未含 %q: %v", c.wantErr, err)
			}
		})
	}
}

// TestParseSkillMD_StandardOptionalFields 验证 E5 全字段透传：Anthropic 规范
// （license/allowed-tools/compatibility）与 DSH/Claude Code 扩展
// （disable-model-invocation/user-invocable/when-to-use/whenToUse）均解析保留不报错。
func TestParseSkillMD_StandardOptionalFields(t *testing.T) {
	content := `---
name: full-fields
description: 全字段样本
license: MIT
allowed-tools: Bash Read Grep
compatibility: Requires git
disable-model-invocation: true
user-invocable: false
when-to-use: 用户要求写文案时
metadata:
  title: 文案专家
---
# Body
`
	m, err := ParseSkillMDContent([]byte(content))
	if err != nil {
		t.Fatalf("全字段 SKILL.md 不应报错: %v", err)
	}
	if m.License != "MIT" {
		t.Errorf("License = %q, want MIT", m.License)
	}
	if m.AllowedTools != "Bash Read Grep" {
		t.Errorf("AllowedTools = %q", m.AllowedTools)
	}
	if m.Compatibility != "Requires git" {
		t.Errorf("Compatibility = %q", m.Compatibility)
	}
	if m.DisableModelInvocation == nil || !*m.DisableModelInvocation {
		t.Errorf("DisableModelInvocation = %v, want true", m.DisableModelInvocation)
	}
	if m.UserInvocable == nil || *m.UserInvocable {
		t.Errorf("UserInvocable = %v, want false", m.UserInvocable)
	}
	if m.WhenToUse != "用户要求写文案时" {
		t.Errorf("WhenToUse = %q", m.WhenToUse)
	}
	// metadata.title 覆盖展示名（slug name 不可读中文场景）
	if got := m.DisplayName(); got != "文案专家" {
		t.Errorf("DisplayName = %q, want 文案专家", got)
	}
}

// TestParseSkillMD_WhenToUseCamelCase 验证 DSH 风格 camelCase whenToUse 并入 WhenToUse。
func TestParseSkillMD_WhenToUseCamelCase(t *testing.T) {
	content := "---\nname: camel\ndescription: d\nwhenToUse: camel 写法\n---\nbody\n"
	m, err := ParseSkillMDContent([]byte(content))
	if err != nil {
		t.Fatalf("camelCase whenToUse 不应报错: %v", err)
	}
	if m.WhenToUse != "camel 写法" {
		t.Errorf("WhenToUse = %q, want camel 写法", m.WhenToUse)
	}
	// 未声明 title 时 DisplayName 回退 slug name
	if got := m.DisplayName(); got != "camel" {
		t.Errorf("DisplayName = %q, want camel", got)
	}
}
