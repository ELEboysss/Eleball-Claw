package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eleball/gateway/internal/config"
)

// tempTestRoot 创建隔离的临时目录并在其根目录放置 .git，使向上遍历不会泄漏到外层仓库。
func tempTestRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0755)
	return dir
}

// TestLoadContextFiles_EmptyCwd cwd 为空时返回空切片
func TestLoadContextFiles_EmptyCwd(t *testing.T) {
	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true})
	files, err := svc.LoadContextFiles("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected empty files, got %d", len(files))
	}
}

// TestLoadContextFiles_UpwardTraversal 验证从 cwd 向上遍历并按父->子顺序加载
func TestLoadContextFiles_UpwardTraversal(t *testing.T) {
	root := tempTestRoot(t)
	sub := filepath.Join(root, "sub")
	proj := filepath.Join(sub, "project")
	_ = os.MkdirAll(proj, 0755)

	_ = os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("root claude"), 0644)
	_ = os.WriteFile(filepath.Join(proj, "AGENTS.md"), []byte("project agents"), 0644)

	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true})
	files, err := svc.LoadContextFiles(proj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Source != "CLAUDE.md" || files[0].Content != "root claude" {
		t.Fatalf("expected root CLAUDE.md first, got %+v", files[0])
	}
	if files[1].Source != "AGENTS.md" || files[1].Content != "project agents" {
		t.Fatalf("expected project AGENTS.md second, got %+v", files[1])
	}
}

// TestLoadContextFiles_CaseInsensitive 验证大小写不敏感匹配
func TestLoadContextFiles_CaseInsensitive(t *testing.T) {
	root := tempTestRoot(t)
	_ = os.WriteFile(filepath.Join(root, "agents.md"), []byte("lowercase agents"), 0644)

	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true})
	files, err := svc.LoadContextFiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Source != "AGENTS.md" {
		t.Fatalf("expected canonical source AGENTS.md, got %s", files[0].Source)
	}
}

// TestLoadContextFiles_PerFileLimit 验证单文件字符上限与截断提示
func TestLoadContextFiles_PerFileLimit(t *testing.T) {
	root := tempTestRoot(t)
	content := strings.Repeat("a", 100)
	_ = os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(content), 0644)

	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true, MaxFileChars: 50})
	files, err := svc.LoadContextFiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Size <= 50 {
		t.Fatalf("expected size > 50 after adding hint, got %d", files[0].Size)
	}
	if !strings.Contains(files[0].Content, "已按 50 字符截断") {
		t.Fatalf("expected truncation hint, got %q", files[0].Content)
	}
}

// TestLoadContextFiles_TotalBudget 验证总预算截断：子目录文件超出预算时不加载
func TestLoadContextFiles_TotalBudget(t *testing.T) {
	root := tempTestRoot(t)
	sub := filepath.Join(root, "sub")
	_ = os.MkdirAll(sub, 0755)

	_ = os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(strings.Repeat("a", 60)), 0644)
	_ = os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("child agents"), 0644)

	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true, MaxTotalChars: 60, MaxFileChars: 100})
	files, err := svc.LoadContextFiles(sub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file within budget, got %d", len(files))
	}
	if files[0].Source != "CLAUDE.md" {
		t.Fatalf("expected parent CLAUDE.md within budget, got %s", files[0].Source)
	}
}

// TestListLoadedFiles_AppliedFlag 验证 /memory 列表中 applied 字段反映预算状态
func TestListLoadedFiles_AppliedFlag(t *testing.T) {
	root := tempTestRoot(t)
	sub := filepath.Join(root, "sub")
	_ = os.MkdirAll(sub, 0755)

	_ = os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(strings.Repeat("a", 60)), 0644)
	_ = os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("child agents"), 0644)

	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true, MaxTotalChars: 60, MaxFileChars: 100})
	files, err := svc.ListLoadedFiles(sub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 discovered files, got %d", len(files))
	}
	if !files[0].Applied {
		t.Fatalf("expected first file applied=true")
	}
	if files[1].Applied {
		t.Fatalf("expected second file applied=false due to budget")
	}
}

// TestLoadContextFiles_Disabled 验证 enabled=false 时 LoadContextFiles 仍返回文件（调用方决定是否注入）
func TestLoadContextFiles_Disabled(t *testing.T) {
	root := tempTestRoot(t)
	_ = os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("content"), 0644)

	svc := NewContextFileService(config.ContextFilesConfig{Enabled: false})
	files, err := svc.LoadContextFiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file even when disabled, got %d", len(files))
	}
}

// TestLoadRuleFiles_Matching 验证 .claw/rules/*.md 按 paths glob 匹配
func TestLoadRuleFiles_Matching(t *testing.T) {
	root := tempTestRoot(t)
	rulesDir := filepath.Join(root, ".claw", "rules")
	_ = os.MkdirAll(rulesDir, 0755)

	frontmatter := "---\npaths:\n  - '*.go'\n  - 'src/**/*.go'\n---\nUse gofmt.\n"
	_ = os.WriteFile(filepath.Join(rulesDir, "go.md"), []byte(frontmatter), 0644)

	frontmatter2 := "---\npaths:\n  - '*.py'\n---\nUse black.\n"
	_ = os.WriteFile(filepath.Join(rulesDir, "py.md"), []byte(frontmatter2), 0644)

	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true, RulesEnabled: true})
	files, err := svc.LoadRuleFiles(root, []string{"main.go", "src/pkg/util.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 matching rule, got %d", len(files))
	}
	if files[0].Source != "go.md" || !strings.Contains(files[0].Content, "Use gofmt") {
		t.Fatalf("expected go.md rule, got %+v", files[0])
	}
}

// TestLoadRuleFiles_Disabled 验证 rules_enabled=false 时不加载规则
func TestLoadRuleFiles_Disabled(t *testing.T) {
	root := tempTestRoot(t)
	rulesDir := filepath.Join(root, ".claw", "rules")
	_ = os.MkdirAll(rulesDir, 0755)
	_ = os.WriteFile(filepath.Join(rulesDir, "go.md"), []byte("---\npaths:\n  - '*.go'\n---\n"), 0644)

	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true, RulesEnabled: false})
	files, err := svc.LoadRuleFiles(root, []string{"main.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 rules when disabled, got %d", len(files))
	}
}

// TestFormatInjectionBlock 验证注入块 XML 格式与路径属性
func TestFormatInjectionBlock(t *testing.T) {
	svc := NewContextFileService(config.ContextFilesConfig{Enabled: true})
	block := svc.FormatInjectionBlock([]ContextFile{
		{Path: "/tmp/CLAUDE.md", Source: "CLAUDE.md", Content: "hello <world>", Applied: true},
	})
	if !strings.Contains(block, `<project_instructions path="/tmp/CLAUDE.md" source="CLAUDE.md">`) {
		t.Fatalf("expected opening tag with attributes, got %q", block)
	}
	if !strings.Contains(block, "hello &lt;world&gt;") {
		t.Fatalf("expected XML-escaped content, got %q", block)
	}
	if !strings.Contains(block, "</project_instructions>") {
		t.Fatalf("expected closing tag, got %q", block)
	}
}

// TestLoadRuleFiles_GlobCases 复用 permission_service 的 matchGlob 验证规则匹配
func TestLoadRuleFiles_GlobCases(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.py", "main.go", false},
		{"src/**", "src/pkg/util.go", true},
		{"src/**", "util.go", false},
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/sub/main.go", false},
	}
	for _, c := range cases {
		got := matchGlob(c.pattern, c.path)
		if got != c.want {
			t.Fatalf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}
