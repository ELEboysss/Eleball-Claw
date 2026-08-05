package service

import (
	"context"
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

func TestPermission_AutoMode(t *testing.T) {
	ps := NewPermissionService("")
	// auto 全自动：非只读工具放行（危险操作由 runner 黑名单 + 审批闸前置检查兜底）
	if d := ps.Decide(model.PermissionModeAuto, permTool("Shell", false), map[string]interface{}{"command": "ls"}); d != model.PermissionDecisionAllow {
		t.Errorf("auto+shell want allow, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeAuto, permTool("WriteFile", false), map[string]interface{}{"path": "a.go"}); d != model.PermissionDecisionAllow {
		t.Errorf("auto+write want allow, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeAuto, permTool("ReadFile", true), nil); d != model.PermissionDecisionAllow {
		t.Errorf("auto+readonly want allow, got %s", d)
	}
	// auto 仍受用户 deny 规则约束
	ps.AddAlwaysAllow("ReadFile(secret/**)", model.PermissionDecisionDeny)
	if d := ps.Decide(model.PermissionModeAuto, permTool("ReadFile", true), map[string]interface{}{"path": "secret/x"}); d != model.PermissionDecisionDeny {
		t.Errorf("auto+deny-rule want deny, got %s", d)
	}
}

func TestPermission_StrictMode(t *testing.T) {
	ps := NewPermissionService("")
	// strict 全确认：只读工具也需审批（不自动放行）
	if d := ps.Decide(model.PermissionModeStrict, permTool("ReadFile", true), nil); d != model.PermissionDecisionAsk {
		t.Errorf("strict+readonly want ask, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeStrict, permTool("Shell", false), map[string]interface{}{"command": "ls"}); d != model.PermissionDecisionAsk {
		t.Errorf("strict+shell want ask, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeStrict, permTool("WriteFile", false), map[string]interface{}{"path": "a.go"}); d != model.PermissionDecisionAsk {
		t.Errorf("strict+write want ask, got %s", d)
	}
	// strict 下用户 allow 规则仍可放行（git status --short 命中 Bash(git status *) 通配）
	ps.AddAlwaysAllow("Bash(git status *)", model.PermissionDecisionAllow)
	if d := ps.Decide(model.PermissionModeStrict, permTool("Shell", false), map[string]interface{}{"command": "git", "args": []interface{}{"status", "--short"}}); d != model.PermissionDecisionAllow {
		t.Errorf("strict+allow-rule want allow, got %s", d)
	}
}

func TestPermission_ShellInputDangerous(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]interface{}
		want  bool
	}{
		{"safe ls", map[string]interface{}{"command": "ls", "args": []interface{}{"-la"}}, false},
		{"safe pipe", map[string]interface{}{"command": "git log | head"}, false},
		{"rm -rf /", map[string]interface{}{"command": "rm", "args": []interface{}{"-rf", "/"}}, true},
		{"rm -rf /*", map[string]interface{}{"command": "rm", "args": []interface{}{"-rf", "/*"}}, true},
		{"sudo", map[string]interface{}{"command": "sudo", "args": []interface{}{"apt", "update"}}, true},
		{"mkfs", map[string]interface{}{"command": "mkfs.ext4", "args": []interface{}{"/dev/sda1"}}, true},
		{"dd of=/dev/sda", map[string]interface{}{"command": "dd", "args": []interface{}{"if=/dev/zero", "of=/dev/sda"}}, true},
		{"redirect to block dev", map[string]interface{}{"command": "echo x > /dev/sda"}, true},
	}
	for _, c := range cases {
		got := shellInputDangerous(c.input)
		if got != c.want {
			t.Errorf("%s: shellInputDangerous = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPermission_DangerNotAllowlistable(t *testing.T) {
	ps := NewPermissionService("")
	// 用户已「总是允许」sudo 命令（模拟误加的危险 allowlist 规则）
	ps.AddAlwaysAllow("Bash(sudo *)", model.PermissionDecisionAllow)
	shellTool := permTool("Shell", false)
	// auto 全自动 + allow 规则命中，但危险命令仍被审批闸前置拒绝（不可被 allowlist 绕过）
	env := &ToolEnv{SessionID: "s1", PermissionMode: model.PermissionModeAuto, PermissionSvc: ps}
	rec := &ToolCallRecord{Step: 1}
	input := map[string]interface{}{"command": "sudo", "args": []interface{}{"apt", "update"}}
	got := requestToolApproval(context.Background(), env, shellTool, "Shell", input, "tc1", rec)
	if got {
		t.Errorf("dangerous sudo should be denied even with allow rule + auto mode")
	}
	if rec.Error == "" {
		t.Errorf("expected error message on danger deny")
	}
}

func TestPermission_GitReadOnlyAutoAllow(t *testing.T) {
	ps := NewPermissionService("")
	shellTool := permTool("Shell", false)
	// default 模式：git 读操作（status/diff/log/blame/show）自动放行
	if d := ps.Decide(model.PermissionModeDefault, shellTool, map[string]interface{}{"command": "git", "args": []interface{}{"status"}}); d != model.PermissionDecisionAllow {
		t.Errorf("default git status want allow, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeDefault, shellTool, map[string]interface{}{"command": "git", "args": []interface{}{"log", "--oneline"}}); d != model.PermissionDecisionAllow {
		t.Errorf("default git log want allow, got %s", d)
	}
	if d := ps.Decide(model.PermissionModeDefault, shellTool, map[string]interface{}{"command": "git diff"}); d != model.PermissionDecisionAllow {
		t.Errorf("default git diff (inline) want allow, got %s", d)
	}
	// git 写操作仍需确认
	if d := ps.Decide(model.PermissionModeDefault, shellTool, map[string]interface{}{"command": "git", "args": []interface{}{"commit", "-m", "x"}}); d != model.PermissionDecisionAsk {
		t.Errorf("default git commit want ask, got %s", d)
	}
	// 含 shell 操作符的 git 命令不自动放行（走确认）
	if d := ps.Decide(model.PermissionModeDefault, shellTool, map[string]interface{}{"command": "git log | head"}); d != model.PermissionDecisionAsk {
		t.Errorf("default git log | head want ask, got %s", d)
	}
	// strict 模式：git 读操作也需确认（全确认）
	if d := ps.Decide(model.PermissionModeStrict, shellTool, map[string]interface{}{"command": "git", "args": []interface{}{"status"}}); d != model.PermissionDecisionAsk {
		t.Errorf("strict git status want ask, got %s", d)
	}
	// plan 模式：git 读操作放行（只读）
	if d := ps.Decide(model.PermissionModePlan, shellTool, map[string]interface{}{"command": "git", "args": []interface{}{"status"}}); d != model.PermissionDecisionAllow {
		t.Errorf("plan git status want allow, got %s", d)
	}
	// deny 规则优先于只读自动放行
	ps.AddAlwaysAllow("Bash(git log *)", model.PermissionDecisionDeny)
	if d := ps.Decide(model.PermissionModeDefault, shellTool, map[string]interface{}{"command": "git", "args": []interface{}{"log", "--oneline"}}); d != model.PermissionDecisionDeny {
		t.Errorf("deny git log want deny (overrides read-only auto-allow), got %s", d)
	}
}
