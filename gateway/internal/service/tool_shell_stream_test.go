package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

// newStreamEnv 构造一个最小 ToolEnv（仅需 Cwd，BackgroundShell/Shell 不依赖 Sandbox）。
func newStreamEnv() *ToolEnv {
	return &ToolEnv{UserID: "u1", ConversationID: "c1"}
}

// streamRegistry 构造使用真实 windowsToolRunner 的 registry（含 BackgroundShell 工具）。
func streamRegistry(t *testing.T) *ToolRegistry {
	t.Helper()
	return NewToolRegistryWithDeps(&windowsToolRunner{}, &mockSearchProvider{})
}

// TestShellStream_Output 流式执行合并 stdout，默认不截断（替换 CombinedOutput 的等价语义）。
func TestShellStream_Output(t *testing.T) {
	skipIfNoShell(t)
	runner := &windowsToolRunner{}
	out, truncated, exitCode, err := runner.ShellStream(context.Background(), "echo hello | cat", nil, "", 0)
	if err != nil {
		t.Fatalf("流式执行应成功: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("输出应含 hello: %q", out)
	}
	if truncated {
		t.Fatalf("headLimit=0 不应截断")
	}
	if exitCode != 0 {
		t.Fatalf("退出码应为 0，实际 %d", exitCode)
	}
}

// TestShellStream_HeadLimit 按 head_limit 行截断，置 truncated=true。
func TestShellStream_HeadLimit(t *testing.T) {
	skipIfNoShell(t)
	runner := &windowsToolRunner{}
	out, truncated, exitCode, err := runner.ShellStream(context.Background(),
		`for i in {1..30}; do echo "n$i"; done`, nil, "", 5)
	if err != nil {
		t.Fatalf("流式执行应成功: %v", err)
	}
	if !truncated {
		t.Fatalf("30 行截断到 5 应置 truncated=true，输出: %q", out)
	}
	if lines := strings.Count(out, "\n"); lines > 5 {
		t.Fatalf("截断后应 <=5 行，实际 %d 行: %q", lines, out)
	}
	if !strings.Contains(out, "n1") {
		t.Fatalf("从头截断应保留首行 n1: %q", out)
	}
	if exitCode != 0 {
		t.Fatalf("退出码应为 0，实际 %d", exitCode)
	}
}

// TestShellStream_ExitCode 非零退出回带退出码与输出（ExitError）。
func TestShellStream_ExitCode(t *testing.T) {
	skipIfNoShell(t)
	runner := &windowsToolRunner{}
	out, _, exitCode, err := runner.ShellStream(context.Background(), "echo before; exit 3", nil, "", 0)
	if err == nil {
		t.Fatalf("非零退出应返回 ExitError")
	}
	if !isExitError(err) {
		t.Fatalf("err 应为 ExitError: %v", err)
	}
	if exitCode != 3 {
		t.Fatalf("退出码应为 3，实际 %d", exitCode)
	}
	if !strings.Contains(out, "before") {
		t.Fatalf("非零退出仍应回带输出 before: %q", out)
	}
}

// TestShellStream_ToolShell toolShell 返回 truncated/exit_code 字段；非零退出返回 result+nil。
func TestShellStream_ToolShell(t *testing.T) {
	skipIfNoShell(t)
	registry := streamRegistry(t)
	env := newStreamEnv()
	shellTool, _ := registry.Get("Shell")

	t.Run("success", func(t *testing.T) {
		out, err := shellTool.Func(context.Background(), map[string]interface{}{
			"command":   "echo ok | cat",
			"head_limit": 10,
		}, env)
		if err != nil {
			t.Fatalf("成功执行不应返回 err: %v", err)
		}
		if out["output"] == nil || !strings.Contains(out["output"].(string), "ok") {
			t.Fatalf("应回带 output 含 ok: %v", out["output"])
		}
		if out["truncated"] != false {
			t.Fatalf("应 truncated=false: %v", out["truncated"])
		}
		if ec, _ := out["exit_code"].(int); ec != 0 {
			t.Fatalf("应 exit_code=0: %v", out["exit_code"])
		}
		if _, hasErr := out["error"]; hasErr {
			t.Fatalf("成功不应有 error 字段: %v", out["error"])
		}
	})

	t.Run("nonzero_exit_returns_result_no_err", func(t *testing.T) {
		out, err := shellTool.Func(context.Background(), map[string]interface{}{
			"command": "echo before; exit 3",
		}, env)
		if err != nil {
			t.Fatalf("非零退出应返回 result+nil err（让模型看到输出/退出码），got err: %v", err)
		}
		if out["exit_code"] != 3 {
			t.Fatalf("应 exit_code=3: %v", out["exit_code"])
		}
		if out["output"] == nil || !strings.Contains(out["output"].(string), "before") {
			t.Fatalf("应回带 output 含 before: %v", out["output"])
		}
		if out["error"] == nil {
			t.Fatalf("应回带 error 字段描述失败: %v", out["error"])
		}
	})
}

// --- BackgroundShell ---

// pollUntilDone 轮询直至后台 shell 完成（或超时失败）。
func pollUntilDone(t *testing.T, tool *Tool, env *ToolEnv, sid string, timeout time.Duration) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := tool.Func(context.Background(), map[string]interface{}{"shell_id": sid}, env)
		if err != nil {
			t.Fatalf("轮询失败: %v", err)
		}
		if out["status"] == "done" {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("后台 shell 未在 %s 内完成", timeout)
	return nil
}

// TestBackground_StartPollDone 启动后台命令并轮询至完成，校验输出与退出码。
func TestBackground_StartPollDone(t *testing.T) {
	skipIfNoShell(t)
	registry := streamRegistry(t)
	env := newStreamEnv()
	bg, _ := registry.Get("BackgroundShell")

	start, err := bg.Func(context.Background(), map[string]interface{}{
		"command": `for i in {1..5}; do echo "tick$i"; sleep 0.05; done`,
		"args":    []interface{}{},
	}, env)
	if err != nil {
		t.Fatalf("后台启动失败: %v", err)
	}
	sid, _ := start["shell_id"].(string)
	if sid == "" {
		t.Fatalf("应返回 shell_id: %v", start)
	}

	done := pollUntilDone(t, bg, env, sid, 3*time.Second)
	if done["status"] != "done" {
		t.Fatalf("应 status=done: %v", done["status"])
	}
	if ec, _ := done["exit_code"].(int); ec != 0 {
		t.Fatalf("正常完成应 exit_code=0，实际 %d", ec)
	}
	output, _ := done["output"].(string)
	if !strings.Contains(output, "tick1") || !strings.Contains(output, "tick5") {
		t.Fatalf("输出应含 tick1 与 tick5: %q", output)
	}
}

// TestBackground_Stop 启动长任务后 stop 终止，校验进程被杀死（status 非 running）。
func TestBackground_Stop(t *testing.T) {
	skipIfNoShell(t)
	registry := streamRegistry(t)
	env := newStreamEnv()
	bg, _ := registry.Get("BackgroundShell")

	start, err := bg.Func(context.Background(), map[string]interface{}{
		"command": `for i in {1..100}; do echo "running$i"; sleep 0.1; done`,
	}, env)
	if err != nil {
		t.Fatalf("后台启动失败: %v", err)
	}
	sid, _ := start["shell_id"].(string)

	// 确认仍在运行
	poll, err := bg.Func(context.Background(), map[string]interface{}{"shell_id": sid}, env)
	if err != nil {
		t.Fatalf("轮询失败: %v", err)
	}
	if poll["status"] != "running" {
		t.Fatalf("长任务启动后应 running，实际 %v（输出 %v）", poll["status"], poll["output"])
	}

	// 停止
	stop, err := bg.Func(context.Background(), map[string]interface{}{
		"shell_id": sid,
		"action":   "stop",
	}, env)
	if err != nil {
		t.Fatalf("stop 失败: %v", err)
	}
	status, _ := stop["status"].(string)
	if status == "running" {
		t.Fatalf("stop 后应不再 running（done/stopped），实际 %s", status)
	}
}

// TestBackground_PollTruncate 轮询输出按 head_limit 截断（取尾部行）。
func TestBackground_PollTruncate(t *testing.T) {
	skipIfNoShell(t)
	registry := streamRegistry(t)
	env := newStreamEnv()
	bg, _ := registry.Get("BackgroundShell")

	start, err := bg.Func(context.Background(), map[string]interface{}{
		"command": `for i in {1..100}; do echo "line$i"; done`,
	}, env)
	if err != nil {
		t.Fatalf("后台启动失败: %v", err)
	}
	sid, _ := start["shell_id"].(string)

	// 等命令跑完（100 行极快，轮询至 done）
	done := pollUntilDone(t, bg, env, sid, 2*time.Second)
	if done["status"] != "done" {
		t.Fatalf("应 done: %v", done["status"])
	}

	// head_limit=5 取尾部 5 行
	out, err := bg.Func(context.Background(), map[string]interface{}{
		"shell_id":   sid,
		"head_limit": 5,
	}, env)
	if err != nil {
		t.Fatalf("轮询失败: %v", err)
	}
	if out["truncated"] != true {
		t.Fatalf("100 行截断到 5 应 truncated=true: %v", out["truncated"])
	}
	output, _ := out["output"].(string)
	if lines := strings.Count(output, "\n"); lines > 5 {
		t.Fatalf("应 <=5 行，实际 %d: %q", lines, output)
	}
	if !strings.Contains(output, "line100") {
		t.Fatalf("尾部截断应保留最后一行 line100: %q", output)
	}
	if strings.Contains(output, "line1\n") {
		t.Fatalf("尾部截断不应保留首行 line1: %q", output)
	}
}

// TestBackground_NotFound 轮询/停止未知 shell_id 应报错。
func TestBackground_NotFound(t *testing.T) {
	registry := streamRegistry(t)
	env := newStreamEnv()
	bg, _ := registry.Get("BackgroundShell")

	if _, err := bg.Func(context.Background(), map[string]interface{}{
		"shell_id": "nope",
	}, env); err == nil {
		t.Fatalf("轮询未知 shell_id 应报错")
	}
	if _, err := bg.Func(context.Background(), map[string]interface{}{
		"shell_id": "nope",
		"action":   "stop",
	}, env); err == nil {
		t.Fatalf("停止未知 shell_id 应报错")
	}
}
