package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// ClawWorktreeHandler claw 本地 worktree 列出/创建/删除（AR-17 O16，仅 claw）。
//
// 供 claw web WorktreeSwitcher 消费，对齐 pi-web /api/worktrees：
//   - GET    /v1/claw-console/worktrees?cwd=  列出项目根 + 全部 worktree
//   - POST   /v1/claw-console/worktrees       body {cwd,branch} 创建 worktree
//   - DELETE /v1/claw-console/worktrees       body {cwd,path,force?} 删除 worktree
//
// 仅校验 cwd 为存在目录（与 git/status 一致）：git 命令本身无越权风险，worktree
// 路径由服务层基于仓库根派生并校验归属。dirty worktree 删除失败时返回 {dirty:true}
// 供前端二次确认 force 删除。
type ClawWorktreeHandler struct {
	svc *service.WorktreeService
}

// NewClawWorktreeHandler 创建 worktree 处理器。
func NewClawWorktreeHandler(svc *service.WorktreeService) *ClawWorktreeHandler {
	return &ClawWorktreeHandler{svc: svc}
}

// worktreeDTO 列出接口返回的单条 worktree（对齐 service.WorktreeInfo）。
type worktreeDTO struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	IsMain bool   `json:"isMain"`
}

// ListWorktrees 列出 cwd 所属项目根与全部 worktree（AR-17 O16，GET /v1/claw-console/worktrees）。
// 非 git 仓库返回 isGit=false 与空列表，前端据此隐藏切换器。
func (h *ClawWorktreeHandler) ListWorktrees(c *gin.Context) {
	cwd := c.Query("cwd")
	if cwd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd 不能为空"})
		return
	}
	if err := assertDirExists(cwd); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	project := h.svc.ResolveProject(cwd)
	worktrees, err := h.svc.ListWorktrees(cwd)
	if err != nil {
		// 非 git 仓库：返回 isGit=false，前端隐藏切换器
		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "success",
			"data": gin.H{
				"projectRoot": project.ProjectRoot,
				"isGit":       false,
				"isTopLevel":  false,
				"worktrees":   []worktreeDTO{},
			},
		})
		return
	}
	dtos := make([]worktreeDTO, 0, len(worktrees))
	for _, w := range worktrees {
		dtos = append(dtos, worktreeDTO{Path: w.Path, Branch: w.Branch, IsMain: w.IsMain})
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"projectRoot": project.ProjectRoot,
			"isGit":       true,
			"isTopLevel":  project.IsTopLevel,
			"worktrees":   dtos,
		},
	})
}

// CreateWorktree 创建 worktree（AR-17 O16，POST /v1/claw-console/worktrees）。
// body: {cwd, branch}。返回新 worktree 的 {path, branch}。
func (h *ClawWorktreeHandler) CreateWorktree(c *gin.Context) {
	var req struct {
		Cwd    string `json:"cwd"`
		Branch string `json:"branch"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.Cwd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd 不能为空"})
		return
	}
	if err := assertDirExists(req.Cwd); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	wt, err := h.svc.AddWorktree(req.Cwd, req.Branch)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"path":   wt.Path,
			"branch": wt.Branch,
		},
	})
}

// RemoveWorktree 删除 worktree（AR-17 O16，DELETE /v1/claw-console/worktrees）。
// body: {cwd, path, force?}。dirty worktree 且非 force 时返回 {dirty:true} 供前端确认。
func (h *ClawWorktreeHandler) RemoveWorktree(c *gin.Context) {
	var req struct {
		Cwd   string `json:"cwd"`
		Path  string `json:"path"`
		Force bool   `json:"force"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.Cwd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd 不能为空"})
		return
	}
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "path 不能为空"})
		return
	}
	if err := assertDirExists(req.Cwd); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	if err := h.svc.RemoveWorktree(req.Cwd, req.Path, req.Force); err != nil {
		// dirty worktree：git 拒绝非 force 删除，提示前端二次确认 force
		if isDirtyWorktreeErr(err) && !req.Force {
			c.JSON(http.StatusOK, gin.H{
				"code":    0,
				"message": "success",
				"data":    gin.H{"dirty": true},
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"dirty": false},
	})
}

// assertDirExists 校验 path 为存在的目录，失败返回带友好文案的错误。
func assertDirExists(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errNotDir
	}
	return nil
}

// errNotDir 复用文案，避免在多处硬编码。
var errNotDir = &notDirError{}

type notDirError struct{}

func (e *notDirError) Error() string { return "路径不是目录" }

// isDirtyWorktreeErr 识别 `git worktree remove` 因 dirty 检出被拒的错误。
// git 文案（LC_ALL=C）："contains modified or untracked files, use --force to delete it"。
func isDirtyWorktreeErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "contains modified or untracked files") ||
		strings.Contains(msg, "use --force")
}
