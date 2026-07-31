package handler

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestIsDirtyWorktreeErr 覆盖 git worktree remove 的 dirty 文案识别（LC_ALL=C）。
func TestIsDirtyWorktreeErr(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("fatal: 'path' contains modified or untracked files, use --force to delete it"), true},
		{errors.New("use --force to delete it"), true},
		{errors.New("not a worktree of this repository: /x"), false},
		{errors.New("cannot remove the main worktree"), false},
	}
	for _, c := range cases {
		if got := isDirtyWorktreeErr(c.err); got != c.want {
			t.Errorf("isDirtyWorktreeErr(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// TestAssertDirExists 覆盖存在性校验与「不是目录」分支。
func TestAssertDirExists(t *testing.T) {
	tmp := t.TempDir()
	if err := assertDirExists(tmp); err != nil {
		t.Errorf("existing dir should pass, got %v", err)
	}
	if err := assertDirExists(filepath.Join(tmp, "nope-missing")); err == nil {
		t.Errorf("missing path should error")
	}
	// 指向一个文件而非目录
	f := filepath.Join(tmp, "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := assertDirExists(f); err == nil {
		t.Errorf("file path should fail with not-a-dir")
	}
}
