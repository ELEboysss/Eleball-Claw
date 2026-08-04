package service

import (
	"strings"
	"testing"
)

// TestLocateCommand_MissingReturnsReadableError 缺失命令时返回带安装指引的可读错误（D3）。
func TestLocateCommand_MissingReturnsReadableError(t *testing.T) {
	const missing = "definitely-not-a-real-interpreter-xyz123"
	_, err := locateCommand(missing)
	if err == nil {
		t.Fatal("期望返回错误，实际为 nil")
	}
	ime, ok := err.(*InterpreterMissingError)
	if !ok {
		t.Fatalf("期望 *InterpreterMissingError，实际 %T: %v", err, err)
	}
	if ime.Command != missing {
		t.Errorf("Command = %q，期望 %q", ime.Command, missing)
	}
	if ime.Hint == "" {
		t.Error("Hint 不应为空")
	}
	if !strings.Contains(ime.Error(), "未找到命令") {
		t.Errorf("错误信息缺少可读前缀: %s", ime.Error())
	}
}

// TestLocateCommand_UnknownCommandUsesGenericHint 未知命令（不在 hint 映射）用通用指引。
func TestLocateCommand_UnknownCommandUsesGenericHint(t *testing.T) {
	const missing = "totally-unknown-tool-abc"
	_, err := locateCommand(missing)
	ime, ok := err.(*InterpreterMissingError)
	if !ok {
		t.Fatalf("期望 *InterpreterMissingError，实际 %T", err)
	}
	if !strings.Contains(ime.Hint, "PATH") {
		t.Errorf("未知命令应用通用指引（提及 PATH），实际: %s", ime.Hint)
	}
}

// TestLocateCommand_KnownInterpreterHints 校验已知解释器安装指引内容。
func TestLocateCommand_KnownInterpreterHints(t *testing.T) {
	cases := map[string]string{
		"python":  "Python",
		"python3": "Python",
		"node":    "Node",
		"npx":     "Node",
		"uv":      "uv",
		"uvx":     "uv",
	}
	for cmd, want := range cases {
		hint, ok := interpreterHints[cmd]
		if !ok {
			t.Errorf("%q 应在 interpreterHints 中", cmd)
			continue
		}
		if !strings.Contains(hint, want) {
			t.Errorf("%q hint 应提及 %q，实际: %s", cmd, want, hint)
		}
	}
}

// TestLocateCommand_EmptyCommand 空命令应返回错误。
func TestLocateCommand_EmptyCommand(t *testing.T) {
	if _, err := locateCommand(""); err == nil {
		t.Error("空 command 应返回错误")
	}
}
