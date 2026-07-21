package service

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeTestFile 在临时目录写入测试文件并返回完整路径
func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustBuiltin(t *testing.T, command string, args []string) (string, error) {
	t.Helper()
	out, handled, err := builtinShell(context.Background(), command, args)
	if err != nil {
		return "", err
	}
	if !handled {
		t.Fatalf("命令 %s 应有内置实现", command)
	}
	return out, nil
}

// TestBuiltinGrep 内置 grep：单文件、目录递归、-c/-l/-i/-v
func TestBuiltinGrep(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTestFile(t, dir, "a.txt", "hello world\nfoo bar\nHELLO again\n")
	writeTestFile(t, dir, "sub/b.txt", "nothing here\nhello sub\n")

	// 单文件搜索：输出 行号:内容，不前缀文件名
	out, err := mustBuiltin(t, "grep", []string{"hello", f1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1:hello world") || strings.Contains(out, f1+":1") {
		t.Fatalf("单文件 grep 输出格式不对: %q", out)
	}

	// 目录递归：输出 路径:行号:内容，-i 忽略大小写
	out, err = mustBuiltin(t, "grep", []string{"-r", "-i", "hello", dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.txt:1:hello world") || !strings.Contains(out, "a.txt:3:HELLO again") || !strings.Contains(out, "b.txt:2:hello sub") {
		t.Fatalf("目录递归 grep 输出不对: %q", out)
	}

	// -c 统计（大小写敏感只匹配第 1 行 "hello world"）
	out, err = mustBuiltin(t, "grep", []string{"-c", "hello", f1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ":1") {
		t.Fatalf("grep -c 输出不对: %q", out)
	}

	// -l 只列文件
	out, err = mustBuiltin(t, "grep", []string{"-r", "-l", "hello", dir})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ":") && !strings.Contains(out, ".txt") {
		t.Fatalf("grep -l 输出不对: %q", out)
	}

	// -v 反选
	out, err = mustBuiltin(t, "grep", []string{"-v", "hello", f1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2:foo bar") || strings.Contains(out, "hello world") {
		t.Fatalf("grep -v 输出不对: %q", out)
	}
}

// TestBuiltinGrepSkipsBinary 二进制文件被跳过不报错
func TestBuiltinGrepSkipsBinary(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "bin.dat", "abc\x00def hello")
	out, err := mustBuiltin(t, "grep", []string{"hello", filepath.Join(dir, "bin.dat")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hello") {
		t.Fatalf("二进制文件不应输出匹配: %q", out)
	}
}

// TestBuiltinFind 内置 find：-name/-type/-maxdepth
func TestBuiltinFind(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "aider.config", "x")
	writeTestFile(t, dir, "sub/Aider-note.md", "x")
	writeTestFile(t, dir, "sub/deep/other.txt", "x")

	// -name 通配
	out, err := mustBuiltin(t, "find", []string{dir, "-name", "*ider*"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "aider.config") || !strings.Contains(out, "Aider-note.md") {
		t.Fatalf("find -name 输出不对: %q", out)
	}

	// -iname 忽略大小写 + -type f
	out, err = mustBuiltin(t, "find", []string{dir, "-type", "f", "-iname", "*aider*"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Aider-note.md") {
		t.Fatalf("find -iname 输出不对: %q", out)
	}

	// -maxdepth 1 不含更深层文件
	out, err = mustBuiltin(t, "find", []string{dir, "-maxdepth", "1", "-type", "f"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "other.txt") || !strings.Contains(out, "aider.config") {
		t.Fatalf("find -maxdepth 输出不对: %q", out)
	}
}

// TestBuiltinHeadTailWc head/tail/wc
func TestBuiltinHeadTailWc(t *testing.T) {
	dir := t.TempDir()
	f := writeTestFile(t, dir, "lines.txt", "l1\nl2\nl3\nl4\nl5\n")

	out, err := mustBuiltin(t, "head", []string{"-n", "2", f})
	if err != nil || out != "l1\nl2\n" {
		t.Fatalf("head 输出不对: %q, err=%v", out, err)
	}
	out, err = mustBuiltin(t, "tail", []string{"-n", "1", f})
	if err != nil || out != "l5\n" {
		t.Fatalf("tail 输出不对: %q, err=%v", out, err)
	}
	out, err = mustBuiltin(t, "wc", []string{"-l", f})
	if err != nil || !strings.Contains(out, "5 ") {
		t.Fatalf("wc -l 输出不对: %q, err=%v", out, err)
	}
}

// TestBuiltinSortUniqCut sort/uniq/cut
func TestBuiltinSortUniqCut(t *testing.T) {
	dir := t.TempDir()
	f := writeTestFile(t, dir, "data.txt", "b,2\na,1\nb,2\nc,3\n")

	out, err := mustBuiltin(t, "sort", []string{f})
	if err != nil || !strings.HasPrefix(out, "a,1\n") {
		t.Fatalf("sort 输出不对: %q", out)
	}
	out, err = mustBuiltin(t, "sort", []string{"-u", f})
	if err != nil || strings.Count(out, "b,2") != 1 {
		t.Fatalf("sort -u 输出不对: %q", out)
	}
	out, err = mustBuiltin(t, "cut", []string{"-d", ",", "-f", "2", f})
	if err != nil || !strings.Contains(out, "2\n1\n2\n3") {
		t.Fatalf("cut 输出不对: %q", out)
	}
}

// TestBuiltinLsCatEcho ls/cat/echo/pwd
func TestBuiltinLsCatEcho(t *testing.T) {
	dir := t.TempDir()
	f := writeTestFile(t, dir, "x.txt", "content\n")

	out, err := mustBuiltin(t, "ls", []string{dir})
	if err != nil || !strings.Contains(out, "x.txt") {
		t.Fatalf("ls 输出不对: %q", out)
	}
	out, err = mustBuiltin(t, "cat", []string{f})
	if err != nil || out != "content\n" {
		t.Fatalf("cat 输出不对: %q", out)
	}
	out, err = mustBuiltin(t, "echo", []string{"hello", "world"})
	if err != nil || out != "hello world\n" {
		t.Fatalf("echo 输出不对: %q", out)
	}
	if _, err = mustBuiltin(t, "pwd", nil); err != nil {
		t.Fatalf("pwd 失败: %v", err)
	}
}

// TestWindowsRunnerShellUsesBuiltin Windows 运行器优先走内置实现（跨平台可测）
func TestWindowsRunnerShellUsesBuiltin(t *testing.T) {
	dir := t.TempDir()
	f := writeTestFile(t, dir, "note.txt", "aider line\nother\n")
	runner := &windowsToolRunner{}

	// Windows 无 grep/find 二进制时也必须可用
	out, err := runner.Shell(context.Background(), "grep", []string{"aider", f})
	if err != nil || !strings.Contains(out, "1:aider line") {
		t.Fatalf("runner grep 失败: %q, err=%v", out, err)
	}
	out, err = runner.Shell(context.Background(), "find", []string{dir, "-name", "*note*"})
	if err != nil || !strings.Contains(out, "note.txt") {
		t.Fatalf("runner find 失败: %q, err=%v", out, err)
	}

	// 白名单与危险字符拦截仍然生效
	if _, err = runner.Shell(context.Background(), "rm", []string{"-rf", "/"}); err == nil {
		t.Fatal("非白名单命令应被拒绝")
	}
	if _, err = runner.Shell(context.Background(), "echo", []string{"a", "&", "b"}); err == nil {
		t.Fatal("危险字符应被拦截")
	}
}

// TestToolGrepPureGo toolGrep 不再依赖外部 grep 命令
func TestToolGrepPureGo(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "kb/aider.md", "aider intro\nother\naider advanced\n")

	matches, err := searchPattern(context.Background(), dir, mustCompile(t, "aider"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("期望 2 条匹配，实际 %d: %+v", len(matches), matches)
	}
	lines := formatGrepMatches(matches, false)
	if !strings.Contains(lines[0], "aider.md:1:aider intro") {
		t.Fatalf("格式化输出不对: %v", lines)
	}
}

func mustCompile(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return re
}
