package service

import (
	"context"
	"strings"
	"testing"
)

// skipIfNoShell 无 bash/sh 时跳过依赖 shell 解释器的组合命令测试
func skipIfNoShell(t *testing.T) {
	t.Helper()
	if findShell() == "" {
		t.Skip("跳过：PATH 中无 bash/sh，无法执行组合命令")
	}
}

// skipIfNoExec 指定可执行文件不在 PATH 时跳过
func skipIfNoExec(t *testing.T, name string) {
	t.Helper()
	if findExecutable(name) == "" {
		t.Skipf("跳过：PATH 中无 %s", name)
	}
}

// TestShell_CompoundPipe 管道：echo hello | wc -w（D3：管道不再被元字符禁令拦截）
func TestShell_CompoundPipe(t *testing.T) {
	skipIfNoShell(t)
	runner := &windowsToolRunner{}
	out, err := runner.Shell(context.Background(), "echo hello | wc -w", nil, "")
	if err != nil {
		t.Fatalf("管道命令应可执行: %v", err)
	}
	if !strings.Contains(strings.TrimSpace(out), "1") {
		t.Fatalf("echo hello | wc -w 应输出 1: %q", out)
	}
}

// TestShell_CompoundChain 链式：echo a && echo b（对应验收 make build && make test 的 && 能力）
func TestShell_CompoundChain(t *testing.T) {
	skipIfNoShell(t)
	runner := &windowsToolRunner{}
	out, err := runner.Shell(context.Background(), "echo a && echo b", nil, "")
	if err != nil {
		t.Fatalf("链式命令应可执行: %v", err)
	}
	if !strings.Contains(out, "a") || !strings.Contains(out, "b") {
		t.Fatalf("echo a && echo b 应输出 a 和 b: %q", out)
	}
}

// TestShell_CompoundRedirect 重定向：echo > file && cat file（在 cwd 下操作避免路径转义问题）
func TestShell_CompoundRedirect(t *testing.T) {
	skipIfNoShell(t)
	dir := t.TempDir()
	runner := &windowsToolRunner{}
	out, err := runner.Shell(context.Background(), "echo redirect-data > out.txt && cat out.txt", nil, dir)
	if err != nil {
		t.Fatalf("重定向命令应可执行: %v", err)
	}
	if !strings.Contains(out, "redirect-data") {
		t.Fatalf("重定向写入+读取应返回数据: %q", out)
	}
}

// TestShell_CommandSubstitution 命令替换 $()
func TestShell_CommandSubstitution(t *testing.T) {
	skipIfNoShell(t)
	runner := &windowsToolRunner{}
	out, err := runner.Shell(context.Background(), "echo $(echo substituted)", nil, "")
	if err != nil {
		t.Fatalf("命令替换应可执行: %v", err)
	}
	if !strings.Contains(out, "substituted") {
		t.Fatalf("$(echo substituted) 应输出 substituted: %q", out)
	}
}

// TestShell_InlineExec 内联执行 -c/-e 不再禁止（D3：移除 shellInlineExecFlags）
func TestShell_InlineExec(t *testing.T) {
	skipIfNoShell(t)
	skipIfNoExec(t, "node")
	runner := &windowsToolRunner{}
	out, err := runner.Shell(context.Background(), "node", []string{"-e", "console.log(42)"}, "")
	if err != nil {
		t.Fatalf("node -e 应可执行: %v", err)
	}
	if !strings.Contains(out, "42") {
		t.Fatalf("node -e console.log(42) 应输出 42: %q", out)
	}
}

// TestShell_DangerousBlocked 危险操作黑名单硬拒（D3 危险层，不需 bash）
func TestShell_DangerousBlocked(t *testing.T) {
	runner := &windowsToolRunner{}
	cases := []struct {
		name string
		cmd  string
		args []string
	}{
		{"rm -rf /", "rm", []string{"-rf", "/"}},
		{"rm -rf /*", "rm -rf /*", nil},
		{"sudo cmd", "sudo apt install x", nil},
		{"mkfs", "mkfs.ext4 /dev/sda1", nil},
		{"redirect to dev disk", "echo x > /dev/sda", nil},
		{"dd of=/dev/sda", "dd if=/dev/zero of=/dev/sda", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := runner.Shell(context.Background(), c.cmd, c.args, "")
			if err == nil {
				t.Fatalf("危险操作应被拒绝: %s %v", c.cmd, c.args)
			}
		})
	}
}

// TestShell_ArgsLiteral 含操作符字符的 args 按字面量传递（自动转义，不被 bash 解释）
func TestShell_ArgsLiteral(t *testing.T) {
	skipIfNoShell(t)
	runner := &windowsToolRunner{}
	// arg ">&" 含重定向/后台符，应作为字面量输出而非被解释
	out, err := runner.Shell(context.Background(), "echo", []string{">&", "x"}, "")
	if err != nil {
		t.Fatalf("字面量参数应可执行: %v", err)
	}
	if !strings.Contains(out, ">& x") {
		t.Fatalf("参数 >& x 应按字面量输出: %q", out)
	}
}
