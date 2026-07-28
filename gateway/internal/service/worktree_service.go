package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WorktreeService 封装 git worktree 的列出/创建/删除（AR-17 O16，仅 claw）。
//
// 供 claw web WorktreeSwitcher 消费，port 自 pi-web lib/worktree.ts：
//   - ResolveProject(cwd)  解析 cwd 所属项目根（worktree 折叠回主仓库）
//   - ListWorktrees(cwd)   列出仓库全部 worktree（跳过 prunable / 已缺失）
//   - AddWorktree(cwd,branch)  在 <repoRoot>-worktrees/<branch> 创建新 worktree
//   - RemoveWorktree(cwd,path,force)  删除 worktree 检出（保留分支）
//
// claw 本地单用户模型：cwd 即用户选定的工作目录，worktree 是其所属 git 仓库的
// 并行检出。所有 git 命令以 cwd 为 -C 上下文执行，LC_ALL=C 固定错误文案以便识别。
type WorktreeService struct{}

// NewWorktreeService 创建 worktree 服务。
func NewWorktreeService() *WorktreeService {
	return &WorktreeService{}
}

// ProjectInfo cwd 所属项目的解析结果。
type ProjectInfo struct {
	ProjectRoot string `json:"projectRoot"` // 主仓库根（worktree 折叠回主仓库）
	Branch      string `json:"branch"`      // cwd 当前分支，非 git / detached HEAD 为空
	IsWorktree  bool   `json:"isWorktree"`  // cwd 是否为 linked worktree（非主检出）
	// IsTopLevel cwd 是否为某个检出（主或 linked）的顶层目录。
	// 仓库子目录与非 git 目录为 false——worktree 切换器仅在顶层有意义。
	IsTopLevel bool `json:"isTopLevel"`
}

// WorktreeInfo 单个 worktree 检出。
type WorktreeInfo struct {
	Path    string `json:"path"`    // 检出绝对路径
	Branch  string `json:"branch"`  // 分支名（detached 为空）
	IsMain  bool   `json:"isMain"`  // 是否主检出（git list 首条）
}

const worktreeGitTimeout = 10 * time.Second

// gitError 包装 git 命令失败，保留 stderr 供调用方识别具体原因（如 dirty worktree）。
type gitError struct {
	stderr string
	err    error
}

func (e *gitError) Error() string {
	if e.stderr != "" {
		return strings.TrimSpace(e.stderr)
	}
	return e.err.Error()
}

// Stderr 返回 git 命令的 stderr（已 trim），供 dirty 检测等场景使用。
func (e *gitError) Stderr() string { return e.stderr }

// gitRun 在 cwd 内执行 git 子命令，返回 trim 后的 stdout。失败时返回 *gitError。
func gitRun(cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), worktreeGitTimeout)
	defer cancel()
	full := append([]string{"-C", cwd}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", &gitError{stderr: stderr.String(), err: err}
	}
	return strings.TrimSpace(stdout.String()), nil
}

// dirExists 报告 path 是否为存在的目录。
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ResolveProject 解析 cwd 所属项目根（AR-17 O16）。
//
// worktree 的 `git rev-parse --git-common-dir` 指向主仓库的 .git 目录，其父目录即
// 全部 worktree 共享的项目根。非 git 目录解析为自身。cwd 不存在时尝试推断为已删除的
// worktree（父目录以 -worktrees 结尾），回退到主仓库。
func (s *WorktreeService) ResolveProject(cwd string) ProjectInfo {
	if !dirExists(cwd) {
		if inferred := inferRemovedWorktree(cwd); inferred != (ProjectInfo{}) {
			return inferred
		}
		return ProjectInfo{ProjectRoot: cwd}
	}
	out, err := gitRun(cwd,
		"rev-parse", "--path-format=absolute",
		"--git-common-dir", "--git-dir", "--show-toplevel",
		"--abbrev-ref", "HEAD",
	)
	if err != nil {
		// 非 git 仓库：解析为自身，非顶层
		return ProjectInfo{ProjectRoot: cwd}
	}
	lines := strings.Split(out, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	commonDir, gitDir, toplevel, ref := "", "", "", ""
	if len(lines) > 0 {
		commonDir = lines[0]
	}
	if len(lines) > 1 {
		gitDir = lines[1]
	}
	if len(lines) > 2 {
		toplevel = lines[2]
	}
	if len(lines) > 3 {
		ref = lines[3]
	}
	// git 输出已解析软链；cwd 同样方式规范化后再比较
	realCwd := cwd
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		realCwd = filepath.Clean(r)
	}
	isTopLevel := toplevel != "" && toplevel == realCwd
	isWorktreeTopLevel := gitDir != commonDir && gitDir != "" && commonDir != "" && isTopLevel
	projectRoot := cwd
	if isWorktreeTopLevel {
		projectRoot = filepath.Dir(commonDir)
	}
	branch := ""
	if ref != "" && ref != "HEAD" {
		branch = ref
	}
	return ProjectInfo{
		ProjectRoot: projectRoot,
		Branch:      branch,
		IsWorktree:  isWorktreeTopLevel,
		IsTopLevel:  isTopLevel,
	}
}

// inferRemovedWorktree 当 cwd 已不存在但其父目录以 -worktrees 结尾时，回退到主仓库。
// 对应 pi-web：删除 worktree 后将其会话归回主仓库而非悬空为幽灵项目。
func inferRemovedWorktree(cwd string) ProjectInfo {
	parent := filepath.Dir(cwd)
	if !strings.HasSuffix(parent, "-worktrees") {
		return ProjectInfo{}
	}
	repoRoot := strings.TrimSuffix(parent, "-worktrees")
	if repoRoot == "" || !dirExists(filepath.Join(repoRoot, ".git")) {
		return ProjectInfo{}
	}
	return ProjectInfo{
		ProjectRoot: repoRoot,
		Branch:      filepath.Base(cwd),
		IsWorktree:  true,
		IsTopLevel:  true,
	}
}

// ListWorktrees 列出 cwd 所属仓库的全部 worktree（AR-17 O16）。
// 跳过 prunable 与路径已缺失的 worktree。首条即主检出（isMain=true）。
// 非 git 仓库返回空列表与错误。
func (s *WorktreeService) ListWorktrees(cwd string) ([]WorktreeInfo, error) {
	out, err := gitRun(cwd, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktreePorcelain(out), nil
}

// parseWorktreePorcelain 解析 `git worktree list --porcelain` 输出为 WorktreeInfo 列表。
// 跳过 prunable 与路径不存在的条目；首条标记 isMain。
func parseWorktreePorcelain(out string) []WorktreeInfo {
	var worktrees []WorktreeInfo
	type wt struct {
		path    string
		branch  string
		prunable bool
	}
	var cur *wt
	flush := func() {
		if cur != nil && cur.path != "" && !cur.prunable && dirExists(cur.path) {
			worktrees = append(worktrees, WorktreeInfo{
				Path:   cur.path,
				Branch: cur.branch,
				IsMain: len(worktrees) == 0,
			})
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &wt{path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))}
		case strings.HasPrefix(line, "branch ") && cur != nil:
			b := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			cur.branch = strings.TrimPrefix(b, "refs/heads/")
		case strings.HasPrefix(line, "prunable") && cur != nil:
			cur.prunable = true
		case strings.TrimSpace(line) == "":
			flush()
		}
	}
	flush()
	return worktrees
}

// sanitizeBranchForDir 将分支名净化为可用的目录名（替换非法字符与空白）。
func sanitizeBranchForDir(branch string) string {
	repl := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "-", "\"", "-", "<", "-", ">", "-", "|", "-",
		" ", "-", "\t", "-",
	)
	s := repl.Replace(branch)
	s = strings.Trim(s, "-")
	return s
}

// repoRootFromCwd 取 cwd 所属主仓库根（git-common-dir 的父目录）。非 git 仓库返回错误。
func (s *WorktreeService) repoRootFromCwd(cwd string) (string, error) {
	commonDir, err := gitRun(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", &gitError{stderr: "not a git repository: " + cwd}
	}
	return filepath.Dir(commonDir), nil
}

// AddWorktree 在 <repoRoot>-worktrees/<sanitized-branch> 创建新 worktree（AR-17 O16）。
// 分支已存在则复用，否则在 HEAD 新建。返回新 worktree 的路径与分支。
func (s *WorktreeService) AddWorktree(cwd, branch string) (WorktreeInfo, error) {
	trimmed := strings.TrimSpace(branch)
	if trimmed == "" {
		return WorktreeInfo{}, errors.New("branch name is required")
	}
	dirName := sanitizeBranchForDir(trimmed)
	if dirName == "" {
		return WorktreeInfo{}, fmt.Errorf("invalid branch name: %s", branch)
	}
	repoRoot, err := s.repoRootFromCwd(cwd)
	if err != nil {
		return WorktreeInfo{}, err
	}
	baseDir := repoRoot + "-worktrees"
	worktreePath := filepath.Join(baseDir, dirName)
	if dirExists(worktreePath) {
		return WorktreeInfo{}, fmt.Errorf("directory already exists: %s", worktreePath)
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return WorktreeInfo{}, fmt.Errorf("create worktree base dir: %w", err)
	}
	// 分支已存在则复用，否则 -b 新建
	branchExists := false
	if _, err := gitRun(repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+trimmed); err == nil {
		branchExists = true
	}
	if branchExists {
		if _, err := gitRun(repoRoot, "worktree", "add", "--", worktreePath, trimmed); err != nil {
			return WorktreeInfo{}, err
		}
	} else {
		if _, err := gitRun(repoRoot, "worktree", "add", "-b", trimmed, "--", worktreePath); err != nil {
			return WorktreeInfo{}, err
		}
	}
	return WorktreeInfo{Path: worktreePath, Branch: trimmed, IsMain: false}, nil
}

// RemoveWorktree 删除 worktree 检出（保留分支，AR-17 O16）。
// 校验 target 确为 cwd 仓库的 worktree 且非主检出。force=true 时强制删除（dirty 场景）。
func (s *WorktreeService) RemoveWorktree(cwd, worktreePath string, force bool) error {
	worktrees, err := s.ListWorktrees(cwd)
	if err != nil {
		return err
	}
	var target *WorktreeInfo
	for i := range worktrees {
		if worktrees[i].Path == worktreePath {
			target = &worktrees[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("not a worktree of this repository: %s", worktreePath)
	}
	if target.IsMain {
		return errors.New("cannot remove the main worktree")
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, worktreePath)
	_, err = gitRun(cwd, args...)
	return err
}
