package service

import (
	"context"
	"strings"
	"testing"
)

// TestNormalizeShellInput 入参归一化：整行命令自动分词
func TestNormalizeShellInput(t *testing.T) {
	// 整行命令（无 args）→ 分词
	cmd, args, err := normalizeShellInput("which aider", nil)
	if err != nil || cmd != "which" || len(args) != 1 || args[0] != "aider" {
		t.Fatalf("分词失败: cmd=%q args=%v err=%v", cmd, args, err)
	}

	// 多参数整行 → 分词
	cmd, args, err = normalizeShellInput("pip show aider-chat", nil)
	if err != nil || cmd != "pip" || len(args) != 2 || args[0] != "show" || args[1] != "aider-chat" {
		t.Fatalf("分词失败: cmd=%q args=%v err=%v", cmd, args, err)
	}

	// 无空格 → 原样透传
	cmd, args, err = normalizeShellInput("ls", []string{"-l"})
	if err != nil || cmd != "ls" || len(args) != 1 || args[0] != "-l" {
		t.Fatalf("透传失败: cmd=%q args=%v err=%v", cmd, args, err)
	}

	// command 含空格且 args 非空 → 带格式提示的错误
	_, _, err = normalizeShellInput("ls -l", []string{"/tmp"})
	if err == nil || !strings.Contains(err.Error(), "正确格式") {
		t.Fatalf("应返回格式提示错误: %v", err)
	}

	// 空命令 → 错误
	if _, _, err = normalizeShellInput("  ", nil); err == nil {
		t.Fatal("空命令应报错")
	}
}

// TestShellWhitelistAdditions pip/pip3/where 已在白名单
func TestShellWhitelistAdditions(t *testing.T) {
	for _, name := range []string{"pip", "pip3", "where", "which"} {
		if !shellCommandWhitelist[name] {
			t.Fatalf("%s 应在白名单中", name)
		}
	}
}

// TestBuiltinWhich which 内置实现：内置命令 / PATH 查找 / 未找到
func TestBuiltinWhich(t *testing.T) {
	out, err := mustBuiltin(t, "which", []string{"grep", "definitely-not-exist-xyz-123"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "grep: 内置命令") {
		t.Fatalf("应识别 grep 为内置命令: %q", out)
	}
	if !strings.Contains(out, "definitely-not-exist-xyz-123: 未找到") {
		t.Fatalf("应报告未找到: %q", out)
	}
}

// TestRunnerShellSplitIntegration 整行命令经 runner 归一化后可执行（复现 debugs/log 中的失败场景）
func TestRunnerShellSplitIntegration(t *testing.T) {
	runner := &windowsToolRunner{}

	// 日志场景1：command 塞整行 "which aider"（归一化后 which 内置，输出可用性）
	out, err := runner.Shell(context.Background(), "which aider", nil, "")
	if err != nil {
		t.Fatalf("整行 which 应可执行: %v", err)
	}
	if !strings.Contains(out, "aider:") {
		t.Fatalf("输出不对: %q", out)
	}

	// 日志场景2：command 塞 "grep <pattern> <file>"（归一化后走内置 grep）
	dir := t.TempDir()
	f := writeTestFile(t, dir, "n.txt", "aider here\n")
	out, err = runner.Shell(context.Background(), "grep aider "+f, nil, "")
	if err != nil || !strings.Contains(out, "1:aider here") {
		t.Fatalf("整行 grep 应可执行: %q err=%v", out, err)
	}

	// 日志场景3：管道/重定向仍然拒绝，但错误带格式提示
	_, err = runner.Shell(context.Background(), "pip show aider-chat 2>/dev/null || echo not installed", nil, "")
	if err == nil {
		t.Fatal("管道/重定向应被拒绝")
	}
	if !strings.Contains(err.Error(), "正确格式") {
		t.Fatalf("错误应带格式提示: %v", err)
	}

	// 日志场景4：python3 -c 仍然禁止（安全底线）
	_, err = runner.Shell(context.Background(), "python3", []string{"-c", "print(1)"}, "")
	if err == nil || !strings.Contains(err.Error(), "内联执行") {
		t.Fatalf("-c 应被禁止: %v", err)
	}
}
