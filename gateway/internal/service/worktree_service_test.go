package service

import (
	"testing"
)

// TestParseWorktreePorcelain 覆盖 worktree/branch/prunable 解析、
// prunable 与缺失路径跳过、isMain 首条标记（port 自 pi-web listWorktrees）。
func TestParseWorktreePorcelain(t *testing.T) {
	t.Run("main only", func(t *testing.T) {
		out := "worktree /repo\nbranch refs/heads/main\n"
		wts := parseWorktreePorcelain(out)
		// /repo 大概率不存在于测试环境 -> 被 dirExists 跳过，列表为空
		if len(wts) != 0 {
			t.Fatalf("expected empty (path missing), got %+v", wts)
		}
	})

	t.Run("parses branch and main flag when paths exist", func(t *testing.T) {
		// 用真实存在的临时目录验证 isMain/branch 解析（路径存在才不被跳过）
		tmp := t.TempDir()
		tmp2 := t.TempDir()
		out := "worktree " + tmp + "\nbranch refs/heads/main\n\n" +
			"worktree " + tmp2 + "\nbranch refs/heads/feature/x\n"
		wts := parseWorktreePorcelain(out)
		if len(wts) != 2 {
			t.Fatalf("expected 2 worktrees, got %d (%+v)", len(wts), wts)
		}
		if !wts[0].IsMain {
			t.Errorf("first should be main, got %+v", wts[0])
		}
		if wts[0].Branch != "main" {
			t.Errorf("branch: want main, got %s", wts[0].Branch)
		}
		if wts[1].IsMain {
			t.Errorf("second should not be main, got %+v", wts[1])
		}
		if wts[1].Branch != "feature/x" {
			t.Errorf("branch: want feature/x, got %s", wts[1].Branch)
		}
	})

	t.Run("skips prunable", func(t *testing.T) {
		tmp := t.TempDir()
		out := "worktree " + tmp + "\nbranch refs/heads/main\n\n" +
			"worktree /gone\nprunable\n"
		wts := parseWorktreePorcelain(out)
		// /gone 标记 prunable 被跳过；主检出 tmp 保留
		if len(wts) != 1 {
			t.Fatalf("expected 1 (prunable skipped + missing skipped), got %d (%+v)", len(wts), wts)
		}
		if wts[0].Path != tmp {
			t.Errorf("expected main at %s, got %s", tmp, wts[0].Path)
		}
	})

	t.Run("detached head has empty branch", func(t *testing.T) {
		tmp := t.TempDir()
		// porcelain 对 detached HEAD 不输出 branch 行
		out := "worktree " + tmp + "\nHEAD\n"
		wts := parseWorktreePorcelain(out)
		if len(wts) != 1 {
			t.Fatalf("expected 1, got %d", len(wts))
		}
		if wts[0].Branch != "" {
			t.Errorf("detached should have empty branch, got %s", wts[0].Branch)
		}
	})
}

// TestSanitizeBranchForDir 覆盖非法字符替换与首尾连字符裁剪。
func TestSanitizeBranchForDir(t *testing.T) {
	cases := []struct{ in, want string }{
		{"feature/x", "feature-x"},
		{"  leading", "leading"},
		{"trailing  ", "trailing"},
		{"a/b:c*d?e", "a-b-c-d-e"},
		{"--already-clean--", "already-clean"},
		{"feature branch", "feature-branch"},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeBranchForDir(c.in); got != c.want {
			t.Errorf("sanitizeBranchForDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
