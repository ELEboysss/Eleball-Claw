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
	if err == nil || !strings.Contains(err.Error(), "不应包含参数") {
		t.Fatalf("应返回格式提示错误: %v", err)
	}

	// 空命令 → 错误
	if _, _, err = normalizeShellInput("  ", nil); err == nil {
		t.Fatal("空命令应报错")
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

	// 日志场景3（D3）：管道/重定向/链式现在可执行（经 bash -c），不再被元字符禁令拦截
	skipIfNoShell(t)
	_, err = runner.Shell(context.Background(), "pip show aider-chat 2>/dev/null || echo not installed", nil, "")
	if err != nil {
		t.Fatalf("管道/重定向/链式应可执行: %v", err)
	}

	// 日志场景4（D3）：内联执行 -c/-e 不再禁止
	skipIfNoExec(t, "node")
	out, err = runner.Shell(context.Background(), "node", []string{"-e", "console.log(1)"}, "")
	if err != nil || !strings.Contains(out, "1") {
		t.Fatalf("node -e 应可执行: %q err=%v", out, err)
	}
}
