package handler

import (
	"testing"
)

// TestParseGitPorcelain 覆盖分支头解析（ahead/behind/detached）与 X/Y 状态归并。
func TestParseGitPorcelain(t *testing.T) {
	t.Run("clean repo", func(t *testing.T) {
		d := parseGitPorcelain("## main\n")
		if !d.IsRepo || d.Branch != "main" || !d.Clean {
			t.Fatalf("expected clean main repo, got %+v", d)
		}
	})

	t.Run("ahead and behind", func(t *testing.T) {
		d := parseGitPorcelain("## main...origin/main [ahead 2, behind 1]\n")
		if d.Branch != "main" || d.Ahead != 2 || d.Behind != 1 {
			t.Fatalf("expected main ahead2 behind1, got %+v", d)
		}
	})

	t.Run("detached", func(t *testing.T) {
		d := parseGitPorcelain("## HEAD (no branch)\n")
		if d.Branch != "(detached)" {
			t.Fatalf("expected detached, got branch=%q", d.Branch)
		}
	})

	t.Run("entries classified", func(t *testing.T) {
		out := "## develop\n" +
			"M  modified.go\n" +
			" A added.go\n" +
			"?? untracked.go\n" +
			"D  deleted.go\n" +
			"R  old.go -> new.go\n" +
			"!! ignored.log\n"
		d := parseGitPorcelain(out)
		if d.Clean {
			t.Fatalf("expected not clean")
		}
		want := map[string]string{
			"modified.go":  "modified",
			"added.go":     "added",
			"untracked.go": "untracked",
			"deleted.go":   "deleted",
			"new.go":       "renamed",
			"ignored.log":  "ignored",
		}
		if len(d.Entries) != len(want) {
			t.Fatalf("expected %d entries, got %d (%+v)", len(want), len(d.Entries), d.Entries)
		}
		for _, e := range d.Entries {
			if want[e.Path] != e.Status {
				t.Errorf("path %s: want %s, got %s", e.Path, want[e.Path], e.Status)
			}
		}
	})
}

// TestClassifyStatus 覆盖状态归并优先级。
func TestClassifyStatus(t *testing.T) {
	cases := []struct{ x, y, want string }{
		{"?", "?", "untracked"},
		{"!", "!", "ignored"},
		{"D", " ", "deleted"},
		{" ", "D", "deleted"},
		{"A", " ", "added"},
		{"R", " ", "renamed"},
		{"C", " ", "renamed"},
		{"M", " ", "modified"},
		{" ", "M", "modified"},
	}
	for _, c := range cases {
		if got := classifyStatus(c.x, c.y); got != c.want {
			t.Errorf("classifyStatus(%q,%q) = %s, want %s", c.x, c.y, got, c.want)
		}
	}
}

// TestMimeTypeByExt 覆盖常见扩展名映射与兜底。
func TestMimeTypeByExt(t *testing.T) {
	cases := []struct{ path, want string }{
		{"a.md", "text/markdown; charset=utf-8"},
		{"a.json", "application/json; charset=utf-8"},
		{"a.png", "image/png"},
		{"a.PDF", "application/pdf"},
		{"a.unknownext", "application/octet-stream"},
	}
	for _, c := range cases {
		if got := mimeTypeByExt(c.path); got != c.want {
			t.Errorf("mimeTypeByExt(%s) = %s, want %s", c.path, got, c.want)
		}
	}
}
