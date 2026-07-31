package handler

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// ClawFilesHandler claw 本地文件浏览器/预览后端（AR-11 P1-2，仅 claw）。
//
// 供 claw web FileExplorer/FileViewer 消费，复用 AR-06 的 FileSandbox 沙箱：
//   - GET /v1/claw-console/files?cwd=&path=&type=list|download
//     list 复用 ListDir 列出 cwd 下直接子条目；download 读取文件字节并按扩展名设 Content-Type。
//     路径经 ResolveProjectPath 校验必须落在 cwd 内（Clean + 拒 .. + EvalSymlinks 防软链逃逸）。
//   - GET /v1/claw-console/git/status?cwd=  在 cwd 内跑 git status --porcelain=v1 --branch，
//     解析为结构化状态供 FileExplorer 对变更文件色标。
type ClawFilesHandler struct {
	fs *service.FileSandbox
}

// NewClawFilesHandler 创建文件浏览器处理器。fs 用于路径解析与列目录（复用 AR-06 沙箱）。
func NewClawFilesHandler(fs *service.FileSandbox) *ClawFilesHandler {
	return &ClawFilesHandler{fs: fs}
}

// Files 列出/下载工作目录内文件（AR-11，GET /v1/claw-console/files）。
func (h *ClawFilesHandler) Files(c *gin.Context) {
	cwd := c.Query("cwd")
	if cwd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd 不能为空"})
		return
	}
	rel := c.Query("path")
	if rel == "" {
		rel = "."
	}
	op := c.DefaultQuery("type", "list")

	absPath, err := h.fs.ResolveProjectPath(cwd, rel)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}

	if op == "download" {
		h.serveFile(c, absPath)
		return
	}
	// 默认 list：列目录条目
	entries, err := h.fs.ListDir(cwd, rel)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	// 目录优先、再按名称排序，便于文件浏览器展示
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"path":    absPath,
			"entries": entries,
		},
	})
}

// CreateDir 在工作目录下创建目录（AR-21，POST /v1/claw-console/files/mkdir）。
// 复用 MkdirInCwd：ResolveProjectPath 校验落在 cwd 内（拒 .. + EvalSymlinks 防软链）。
func (h *ClawFilesHandler) CreateDir(c *gin.Context) {
	var req struct {
		Cwd  string `json:"cwd"`
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.Cwd == "" || req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd 和 path 不能为空"})
		return
	}
	abs, err := h.fs.MkdirInCwd(req.Cwd, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"path": abs}})
}

// Move 移动/重命名工作目录内文件或目录（AR-21，POST /v1/claw-console/files/move）。
// src_path、dst_path 都经 ResolveProjectPath 校验落在 cwd 内。
func (h *ClawFilesHandler) Move(c *gin.Context) {
	var req struct {
		Cwd     string `json:"cwd"`
		SrcPath string `json:"src_path"`
		DstPath string `json:"dst_path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.Cwd == "" || req.SrcPath == "" || req.DstPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd/src_path/dst_path 不能为空"})
		return
	}
	abs, err := h.fs.MoveInCwd(req.Cwd, req.SrcPath, req.DstPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"path": abs}})
}

// Delete 删除工作目录内文件或目录（AR-21，DELETE /v1/claw-console/files）。
// 文件/目录统删（os.RemoveAll 语义），拒删 cwd 根。
func (h *ClawFilesHandler) Delete(c *gin.Context) {
	var req struct {
		Cwd  string `json:"cwd"`
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.Cwd == "" || req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd 和 path 不能为空"})
		return
	}
	abs, err := h.fs.RemoveAllInCwd(req.Cwd, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"path": abs}})
}

// serveFile 读取文件字节并以内联方式返回（供 FileViewer 预览，非强制下载）。
func (h *ClawFilesHandler) serveFile(c *gin.Context, absPath string) {
	info, err := os.Stat(absPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 4004, "message": "文件不存在: " + err.Error()})
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "不能下载目录"})
		return
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "读取文件失败: " + err.Error()})
		return
	}
	mimeType := mimeTypeByExt(absPath)
	name := filepath.Base(absPath)
	c.Header("Content-Type", mimeType)
	c.Header("Content-Disposition", `inline; filename="`+name+`"`)
	c.Data(http.StatusOK, mimeType, data)
}

// mimeTypeByExt 按扩展名推断 Content-Type，覆盖常见文本/图片类型，其余交给 mime 推断。
func mimeTypeByExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt", ".log":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".js", ".mjs", ".jsx":
		return "text/javascript; charset=utf-8"
	case ".ts", ".tsx":
		return "text/typescript; charset=utf-8"
	case ".go", ".rs", ".java", ".kt", ".swift", ".py", ".rb", ".php", ".sh", ".yml", ".yaml", ".toml", ".ini", ".cfg", ".xml", ".html", ".css", ".sql", ".c", ".cc", ".cpp", ".h", ".hpp":
		return "text/plain; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	}
	// 兜底：二进制流（浏览器对未知类型通常触发下载）
	return "application/octet-stream"
}

// gitStatusEntry porcelain v1 解析后的单条变更
type gitStatusEntry struct {
	Path   string `json:"path"`
	X      string `json:"x"`
	Y      string `json:"y"`
	Status string `json:"status"`
}

// gitStatusData GitStatusResponse 数据体
type gitStatusData struct {
	IsRepo  bool             `json:"is_repo"`
	Branch  string           `json:"branch"`
	Ahead   int              `json:"ahead"`
	Behind  int              `json:"behind"`
	Clean   bool             `json:"clean"`
	Entries []gitStatusEntry `json:"entries"`
}

// GitStatus 查询工作目录 Git 状态（AR-11，GET /v1/claw-console/git/status?cwd=）。
func (h *ClawFilesHandler) GitStatus(c *gin.Context) {
	cwd := c.Query("cwd")
	if cwd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd 不能为空"})
		return
	}
	// cwd 须为存在的目录（不强制沙箱：用户可自由选 cwd；git 命令本身无越权风险）
	info, err := os.Stat(cwd)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1001, "message": "路径不存在: " + err.Error()})
		return
	}
	if !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd 不是目录"})
		return
	}

	out, err := exec.Command("git", "-C", cwd, "status", "--porcelain=v1", "--branch").Output()
	if err != nil {
		// 非 Git 仓库或 git 未安装：返回 is_repo=false，前端据此隐藏色标
		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "success",
			"data":    gitStatusData{IsRepo: false},
		})
		return
	}
	data := parseGitPorcelain(string(out))
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    data,
	})
}

// parseGitPorcelain 解析 `git status --porcelain=v1 --branch` 输出为结构化状态。
func parseGitPorcelain(out string) gitStatusData {
	data := gitStatusData{IsRepo: true, Clean: true}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			parseBranchHeader(&data, strings.TrimPrefix(line, "## "))
			continue
		}
		if len(line) < 3 {
			continue
		}
		x := string(line[0])
		y := string(line[1])
		path := strings.TrimSpace(line[3:])
		// 重命名：`R  old -> new`，取新路径展示
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		path = strings.Trim(path, `"`)
		data.Entries = append(data.Entries, gitStatusEntry{
			Path:   path,
			X:      x,
			Y:      y,
			Status: classifyStatus(x, y),
		})
	}
	data.Clean = len(data.Entries) == 0
	return data
}

// parseBranchHeader 解析 `## branch...origin/branch [ahead N, behind M]` / `## HEAD (no branch)`。
func parseBranchHeader(data *gitStatusData, header string) {
	// 分离 [ahead/behind] 标记
	bracket := strings.Index(header, "[")
	branchPart := header
	if bracket >= 0 {
		branchPart = header[:bracket]
		tail := header[bracket:]
		data.Ahead = parseCount(tail, "ahead ")
		data.Behind = parseCount(tail, "behind ")
	}
	branchPart = strings.TrimSpace(branchPart)
	if strings.HasPrefix(branchPart, "No commits yet") || branchPart == "No branch" {
		data.Branch = "(no branch)"
		return
	}
	if strings.HasPrefix(branchPart, "HEAD (no branch)") {
		data.Branch = "(detached)"
		return
	}
	// branch...origin/branch 或 branch
	if idx := strings.IndexAny(branchPart, ".~"); idx >= 0 && branchPart[idx] == '.' {
		// 形如 "main...origin/main"
		data.Branch = branchPart[:idx]
		return
	}
	data.Branch = branchPart
}

// parseCount 从 "[ahead 2, behind 1]" 中提取指定标记后的数字。
func parseCount(s, marker string) int {
	idx := strings.Index(s, marker)
	if idx < 0 {
		return 0
	}
	rest := s[idx+len(marker):]
	end := 0
	for end < len(rest) {
		ch := rest[end]
		if ch < '0' || ch > '9' {
			break
		}
		end++
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}

// classifyStatus 归并 X/Y 状态码为展示状态（供前端色标）。
func classifyStatus(x, y string) string {
	switch {
	case x == "?" && y == "?":
		return "untracked"
	case x == "!" && y == "!":
		return "ignored"
	case x == "D" || y == "D":
		return "deleted"
	case x == "A" || y == "A":
		return "added"
	case x == "R" || y == "R" || x == "C" || y == "C":
		return "renamed"
	default:
		return "modified"
	}
}
