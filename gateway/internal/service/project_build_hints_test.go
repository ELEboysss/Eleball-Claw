package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectBuildHints 验证按 cwd 项目类型生成构建命令提示（E3）。
func TestProjectBuildHints(t *testing.T) {
	dir := t.TempDir()
	// 空目录 -> 无提示
	if h := projectBuildHints(dir); h != "" {
		t.Errorf("empty dir want empty hints, got %q", h)
	}
	// Go 项目
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := projectBuildHints(dir)
	if !strings.Contains(h, "Go 项目") {
		t.Errorf("go.mod dir want Go hint, got %q", h)
	}
	// 多项目类型共存
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2 := projectBuildHints(dir)
	if !strings.Contains(h2, "Go 项目") || !strings.Contains(h2, "Node 项目") {
		t.Errorf("mixed want Go+Node hints, got %q", h2)
	}
	// Gradle（kts）
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "build.gradle.kts"), []byte("plugins {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h3 := projectBuildHints(dir2); !strings.Contains(h3, "Gradle 项目") {
		t.Errorf("build.gradle.kts dir want Gradle hint, got %q", h3)
	}
}
