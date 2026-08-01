package service

import (
	"testing"

	"github.com/eleball/gateway/internal/model"
)

// permTool 构造测试用 Tool（避免依赖完整 registerDefaults）。
func permTool(name string, readOnly bool) *Tool {
	return &Tool{Name: name, ReadOnly: readOnly}
}

func TestDecide_PlanMode(t *testing.T) {
	ps := NewPermissionService("")
	// plan 模式：只读放行，非只读拒绝
	if d := ps.Decide(model.PermissionModePlan, permTool("ReadFile", true), nil); d != model.PermissionDecisionAllow {
		t.Errorf("plan+readonly want allow, got %s", d)
	}
	if d := ps.Decide(model.PermissionModePlan, permTool("WriteFile", false), nil); d != model.PermissionDecisionDeny {
		t.Errorf("plan+write want deny, got %s", d)
	}
	if d := ps.Decide(model.PermissionModePlan, permTool("Shell", false), nil); d != model.PermissionDecisionDeny {
		t.Errorf("plan+shell want deny, got %s", d)
	}
}

func TestDecide_DefaultMode(t *testing.T) {
	ps := NewPermissionService("")
	// default：只读放行，非只读 ask
	if d := ps.Decide(model.PermissionModeDefault, permTool("ReadFile", true), nil); d != model.PermissionDecisionAllow {
		t.Errorf("default+readonly want allow, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeDefault, permTool("WriteFile", false), map[string]interface{}{"path": "a.go"}); d != model.PermissionDecisionAsk {
		t.Errorf("default+write want ask, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeDefault, permTool("Shell", false), map[string]interface{}{"command": "ls"}); d != model.PermissionDecisionAsk {
		t.Errorf("default+shell want ask, got %s", d)
	}
}

func TestDecide_AcceptEditsMode(t *testing.T) {
	ps := NewPermissionService("")
	// acceptEdits：文件编辑放行，Shell 仍 ask
	if d := ps.Decide(model.PermissionModeAcceptEdits, permTool("WriteFile", false), map[string]interface{}{"path": "a.go"}); d != model.PermissionDecisionAllow {
		t.Errorf("acceptEdits+write want allow, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeAcceptEdits, permTool("StrReplaceFile", false), map[string]interface{}{"path": "a.go"}); d != model.PermissionDecisionAllow {
		t.Errorf("acceptEdits+strreplace want allow, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeAcceptEdits, permTool("Shell", false), map[string]interface{}{"command": "ls"}); d != model.PermissionDecisionAsk {
		t.Errorf("acceptEdits+shell want ask, got %s", d)
	}
}

func TestDecide_AlwaysAllowRule(t *testing.T) {
	ps := NewPermissionService("")
	// 总是允许 Bash(git status) -> Shell 执行 git status 时放行
	ps.AddAlwaysAllow("Bash(git status *)", model.PermissionDecisionAllow)
	if d := ps.Decide(model.PermissionModeDefault, permTool("Shell", false), map[string]interface{}{"command": "git", "args": []interface{}{"status", "--short"}}); d != model.PermissionDecisionAllow {
		t.Errorf("always-allow git status want allow, got %s", d)
	}
	// 其他 git 命令仍 ask
	if d := ps.Decide(model.PermissionModeDefault, permTool("Shell", false), map[string]interface{}{"command": "git", "args": []interface{}{"push"}}); d != model.PermissionDecisionAsk {
		t.Errorf("git push (not in allow rule) want ask, got %s", d)
	}
}

func TestDecide_DenyRuleOverridesReadOnly(t *testing.T) {
	ps := NewPermissionService("")
	// deny ReadFile(secret/*) 即使只读也拒绝
	ps.AddAlwaysAllow("ReadFile(secret/**)", model.PermissionDecisionDeny)
	if d := ps.Decide(model.PermissionModeDefault, permTool("ReadFile", true), map[string]interface{}{"path": "secret/config.yaml"}); d != model.PermissionDecisionDeny {
		t.Errorf("deny rule on readonly want deny, got %s", d)
	}
	// secret 外的路径仍放行
	if d := ps.Decide(model.PermissionModeDefault, permTool("ReadFile", true), map[string]interface{}{"path": "public/readme.md"}); d != model.PermissionDecisionAllow {
		t.Errorf("readonly outside deny rule want allow, got %s", d)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"*", "anything", true},
		{"src/**", "src/a/b.go", true},
		{"src/**", "src/x.go", true},
		{"src/**", "other/x.go", false},
		{"*.go", "main.go", true},
		{"*.go", "main.py", false},
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/sub/a.go", false}, // * 不跨目录
		{"git status *", "git status --short", true},
		{"git status *", "git push", false},
		{"exact.txt", "exact.txt", true},
		{"exact.txt", "other.txt", false},
	}
	for _, c := range cases {
		got := matchGlob(c.pattern, c.value)
		if got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.value, got, c.want)
		}
	}
}

func TestParseToolSpec(t *testing.T) {
	tool, pattern := parseToolSpec("Bash(git commit *)")
	if tool != "Bash" || pattern != "git commit *" {
		t.Errorf("parseToolSpec Bash(git commit *) = (%q,%q), want (Bash, git commit *)", tool, pattern)
	}
	tool, pattern = parseToolSpec("ReadFile")
	if tool != "ReadFile" || pattern != "" {
		t.Errorf("parseToolSpec ReadFile = (%q,%q), want (ReadFile, )", tool, pattern)
	}
	tool, pattern = parseToolSpec("WriteFile(src/**)  ")
	if tool != "WriteFile" || pattern != "src/**" {
		t.Errorf("parseToolSpec WriteFile(src/**) = (%q,%q), want (WriteFile, src/**)", tool, pattern)
	}
}

func TestAlwaysAllowSpec(t *testing.T) {
	// 路径工具取目录前缀通配
	if s := alwaysAllowSpec("WriteFile", map[string]interface{}{"path": "src/a/b.go"}); s != "WriteFile(src/a/**)" {
		t.Errorf("alwaysAllowSpec path with dir = %q, want WriteFile(src/a/**)", s)
	}
	// 根级文件 -> 工具名(文件名)
	if s := alwaysAllowSpec("ReadFile", map[string]interface{}{"path": "readme.md"}); s != "ReadFile(readme.md)" {
		t.Errorf("alwaysAllowSpec root file = %q, want ReadFile(readme.md)", s)
	}
	// Shell 取 command 前缀
	if s := alwaysAllowSpec("Shell", map[string]interface{}{"command": "git"}); s != "Bash(git *)" {
		t.Errorf("alwaysAllowSpec shell git = %q, want Bash(git *)", s)
	}
}
