package service

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// TestWindowsToolRunner_Shell 验证 Windows 平台下 Shell 工具通过 cmd /c 正确执行
func TestWindowsToolRunner_Shell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows 平台运行")
	}

	runner := &windowsToolRunner{}
	out, err := runner.Shell(context.Background(), "echo", []string{"hello", "world"}, "")
	if err != nil {
		t.Fatalf("Windows Shell 执行失败: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Fatalf("Windows Shell 输出不对: %v", out)
	}
}

// TestWindowsToolRunner_ShellSafety 验证危险操作黑名单拦截（D3 本地模型：rm -rf / 硬拒）
func TestWindowsToolRunner_ShellSafety(t *testing.T) {
	runner := &windowsToolRunner{}
	_, err := runner.Shell(context.Background(), "rm", []string{"-rf", "/"}, "")
	if err == nil {
		t.Fatal("rm -rf / 应被危险操作黑名单拦截")
	}
}

// TestDefaultPlatformRunner_ShellOnNonWindows 验证非 Windows 平台仍使用独立命令执行
func TestDefaultPlatformRunner_ShellOnNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("非 Windows 平台运行")
	}

	runner := &defaultPlatformRunner{}
	out, err := runner.Shell(context.Background(), "echo", []string{"hello"}, "")
	if err != nil {
		t.Fatalf("Linux/macOS Shell 执行失败: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("Linux/macOS Shell 输出不对: %v", out)
	}
}
