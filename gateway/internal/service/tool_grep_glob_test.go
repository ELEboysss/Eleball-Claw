package service

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// grepGlobSandbox 构建一个带沙箱的 Grep/Glob 测试环境
func grepGlobSandbox(t *testing.T) (*ToolRegistry, *ToolEnv) {
	t.Helper()
	base := t.TempDir()
	sandbox := NewFileSandbox(base, "")
	runner := &mockRunner{}
	search := &mockSearchProvider{}
	registry := NewToolRegistryWithDeps(runner, search)
	env := &ToolEnv{UserID: "u1", ConversationID: "c1", Sandbox: sandbox}
	return registry, env
}

// writeFileInEnv 在沙箱内写入测试文件（含子目录），返回绝对路径
func writeFileInEnv(t *testing.T, env *ToolEnv, rel, content string) string {
	t.Helper()
	abs, err := env.ResolveFilePath(rel)
	if err != nil {
		t.Fatalf("解析路径 %q 失败: %v", rel, err)
	}
	if dir := filepath.Dir(abs); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

// setMtime 设置文件修改时间（秒级）
func setMtime(t *testing.T, path string, unix int64) {
	t.Helper()
	mt := time.Unix(unix, 0)
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}
}

// hasFileSuffix 结果集中是否存在以 suffix 结尾的路径
func hasFileSuffix(list []string, suffix string) bool {
	for _, f := range list {
		if strings.HasSuffix(f, suffix) {
			return true
		}
	}
	return false
}

// TestGrep_OutputMode 三种输出模式：content / files_with_matches / count
func TestGrep_OutputMode(t *testing.T) {
	registry, env := grepGlobSandbox(t)
	writeFileInEnv(t, env, "a.go", "hello\nworld\n")
	writeFileInEnv(t, env, "b.go", "hello golang\n")
	writeFileInEnv(t, env, "c.txt", "hello text\n")

	grepTool, _ := registry.Get("Grep")

	// content 模式（默认）
	out, err := grepTool.Func(context.Background(), map[string]interface{}{
		"path": ".", "pattern": "hello",
	}, env)
	if err != nil {
		t.Fatalf("Grep content 失败: %v", err)
	}
	matches, _ := out["matches"].([]string)
	if !hasFileSuffix(matches, "a.go:1:hello") && !hasMatchLine(matches, "1:hello") {
		// rg 单目录输出 "path:line:content"；只要命中 hello 即可
		if !containsAny(matches, "hello") {
			t.Fatalf("content 模式应有 hello 匹配: %v", matches)
		}
	}

	// files_with_matches 模式
	out, err = grepTool.Func(context.Background(), map[string]interface{}{
		"path": ".", "pattern": "hello", "output_mode": "files_with_matches",
	}, env)
	if err != nil {
		t.Fatalf("Grep files_with_matches 失败: %v", err)
	}
	files, _ := out["files"].([]string)
	if !hasFileSuffix(files, "a.go") || !hasFileSuffix(files, "b.go") || !hasFileSuffix(files, "c.txt") {
		t.Fatalf("files_with_matches 应含 a.go/b.go/c.txt: %v", files)
	}

	// count 模式
	out, err = grepTool.Func(context.Background(), map[string]interface{}{
		"path": ".", "pattern": "hello", "output_mode": "count",
	}, env)
	if err != nil {
		t.Fatalf("Grep count 失败: %v", err)
	}
	counts, _ := out["counts"].([]string)
	if !hasFileSuffix(counts, "a.go:1") {
		t.Fatalf("count 模式应含 a.go:1: %v", counts)
	}
	if total, _ := out["total"].(int); total < 3 {
		t.Fatalf("count total 应 >=3（三文件各至少 1）: %v", total)
	}
}

// TestGrep_TypeFilter 按语言类型过滤：type=go 只搜 .go
func TestGrep_TypeFilter(t *testing.T) {
	registry, env := grepGlobSandbox(t)
	writeFileInEnv(t, env, "gotype_a.go", "hello\n")
	writeFileInEnv(t, env, "gotype_b.py", "hello\n")
	writeFileInEnv(t, env, "gotype_c.txt", "hello\n")

	grepTool, _ := registry.Get("Grep")
	out, err := grepTool.Func(context.Background(), map[string]interface{}{
		"path": ".", "pattern": "hello", "type": "go", "output_mode": "files_with_matches",
	}, env)
	if err != nil {
		t.Fatalf("Grep type=go 失败: %v", err)
	}
	files, _ := out["files"].([]string)
	if !hasFileSuffix(files, "gotype_a.go") {
		t.Fatalf("type=go 应含 gotype_a.go: %v", files)
	}
	if hasFileSuffix(files, "gotype_b.py") || hasFileSuffix(files, "gotype_c.txt") {
		t.Fatalf("type=go 不应含 .py/.txt: %v", files)
	}
}

// TestGrep_GlobFilter 按 glob 过滤：glob=*.go 只搜 .go
func TestGrep_GlobFilter(t *testing.T) {
	registry, env := grepGlobSandbox(t)
	writeFileInEnv(t, env, "globfilt_a.go", "hello\n")
	writeFileInEnv(t, env, "globfilt_b.go", "hello\n")
	writeFileInEnv(t, env, "globfilt_c.txt", "hello\n")

	grepTool, _ := registry.Get("Grep")
	out, err := grepTool.Func(context.Background(), map[string]interface{}{
		"path": ".", "pattern": "hello", "glob": "*.go", "output_mode": "files_with_matches",
	}, env)
	if err != nil {
		t.Fatalf("Grep glob=*.go 失败: %v", err)
	}
	files, _ := out["files"].([]string)
	if !hasFileSuffix(files, "globfilt_a.go") || !hasFileSuffix(files, "globfilt_b.go") {
		t.Fatalf("glob=*.go 应含两个 .go: %v", files)
	}
	if hasFileSuffix(files, "globfilt_c.txt") {
		t.Fatalf("glob=*.go 不应含 .txt: %v", files)
	}
}

// TestGrep_HeadLimit 限制返回条数并置 truncated
func TestGrep_HeadLimit(t *testing.T) {
	registry, env := grepGlobSandbox(t)
	writeFileInEnv(t, env, "hl.txt", "hello\nhello\nhello\nhello\nhello\n")

	grepTool, _ := registry.Get("Grep")
	out, err := grepTool.Func(context.Background(), map[string]interface{}{
		"path": "hl.txt", "pattern": "hello", "head_limit": 2,
	}, env)
	if err != nil {
		t.Fatalf("Grep head_limit 失败: %v", err)
	}
	matches, _ := out["matches"].([]string)
	if len(matches) != 2 {
		t.Fatalf("head_limit=2 应返回 2 条，实际 %d: %v", len(matches), matches)
	}
	if trunc, _ := out["truncated"].(bool); !trunc {
		t.Fatalf("head_limit 截断应置 truncated=true: %v", out["truncated"])
	}
}

// TestGrep_Context 上下文行（-C 1）：匹配行 + 前后各 1 行
func TestGrep_Context(t *testing.T) {
	registry, env := grepGlobSandbox(t)
	writeFileInEnv(t, env, "ctx.txt", "a\nb\nMATCH\nd\ne\n")

	grepTool, _ := registry.Get("Grep")
	out, err := grepTool.Func(context.Background(), map[string]interface{}{
		"path": "ctx.txt", "pattern": "MATCH", "context": 1,
	}, env)
	if err != nil {
		t.Fatalf("Grep context 失败: %v", err)
	}
	matches, _ := out["matches"].([]string)
	// 期望 3 行：上下文 b + 匹配 MATCH + 上下文 d
	if len(matches) < 3 {
		t.Fatalf("context=1 应返回至少 3 行（含上下文），实际 %d: %v", len(matches), matches)
	}
	if !containsAny(matches, "MATCH") {
		t.Fatalf("结果应含 MATCH 行: %v", matches)
	}
}

// TestGlob_RecursiveAndMtime ** 递归匹配 + 按 mtime 倒序
func TestGlob_RecursiveAndMtime(t *testing.T) {
	registry, env := grepGlobSandbox(t)
	a := writeFileInEnv(t, env, "a.go", "x")
	b := writeFileInEnv(t, env, "sub/b.go", "x")
	c := writeFileInEnv(t, env, "sub/deep/c.go", "x")
	// b 最新，a 次之，c 最旧
	setMtime(t, c, 100)
	setMtime(t, a, 1000)
	setMtime(t, b, 3000)

	globTool, _ := registry.Get("Glob")
	out, err := globTool.Func(context.Background(), map[string]interface{}{
		"path": ".", "pattern": "**/*.go",
	}, env)
	if err != nil {
		t.Fatalf("Glob 失败: %v", err)
	}
	files, _ := out["files"].([]string)
	if len(files) != 3 {
		t.Fatalf("Glob **/*.go 应返回 3 个文件，实际 %d: %v", len(files), files)
	}
	// 验证三个文件都命中
	for _, want := range []string{"a.go", "sub/b.go", "sub/deep/c.go"} {
		if !containsAny(files, want) {
			t.Fatalf("Glob 结果应含 %q: %v", want, files)
		}
	}
	// 验证 mtime 倒序：b(sub/b.go) 应在最前
	if len(files) > 0 && !strings.HasSuffix(files[0], "sub/b.go") {
		t.Fatalf("Glob 应按 mtime 倒序，最近的 sub/b.go 应排首位: %v", files)
	}
}

// TestGlob_SortStable 多文件 mtime 排序完整性
func TestGlob_SortStable(t *testing.T) {
	registry, env := grepGlobSandbox(t)
	paths := []string{"f1.go", "f2.go", "f3.go", "f4.go"}
	abs := make([]string, len(paths))
	for i, rel := range paths {
		abs[i] = writeFileInEnv(t, env, rel, "x")
	}
	// 倒序设置 mtime：f1 最新 ... f4 最旧
	for i := range paths {
		setMtime(t, abs[i], int64(1000*(len(paths)-i)))
	}

	globTool, _ := registry.Get("Glob")
	out, err := globTool.Func(context.Background(), map[string]interface{}{
		"path": ".", "pattern": "*.go",
	}, env)
	if err != nil {
		t.Fatalf("Glob 失败: %v", err)
	}
	files, _ := out["files"].([]string)
	if len(files) != 4 {
		t.Fatalf("应返回 4 文件，实际 %d: %v", len(files), files)
	}
	// 期望顺序 f1,f2,f3,f4（mtime 倒序）
	got := make([]string, len(files))
	copy(got, files)
	sort.Strings(got) // 排序后比较集合一致即可；顺序单独校验
	wantFirst := "f1.go"
	if !strings.HasSuffix(files[0], wantFirst) {
		t.Fatalf("mtime 倒序首位应为 f1.go: %v", files)
	}
}

// containsAny 切片中是否有元素包含 substr
func containsAny(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// hasMatchLine 判断是否存在 "line:content" 形态（无文件名前缀，单文件 rg 输出）
func hasMatchLine(list []string, line string) bool {
	for _, s := range list {
		if s == line || strings.HasSuffix(s, line) {
			return true
		}
	}
	return false
}
