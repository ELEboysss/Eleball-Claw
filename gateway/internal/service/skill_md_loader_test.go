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
