package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/gin-gonic/gin"
)

// ClawCwdHandler claw 本地工作目录（cwd）选择与校验（AR-06 P0-1，仅 claw）。
//
// 提供目录浏览与校验端点，供 claw web DirectoryPicker 选择项目根：
//   - GET  /v1/claw-console/cwd/browse?path=  列出目录条目（path 空默认用户主目录）
//   - POST /v1/claw-console/cwd/validate      校验路径存在且为目录，返回 EvalSymlinks 后的绝对路径
type ClawCwdHandler struct{}

// NewClawCwdHandler 创建 cwd 处理器
func NewClawCwdHandler() *ClawCwdHandler {
	return &ClawCwdHandler{}
}

// cwdEntry 目录条目（对齐 service.FileEntry，前端文件浏览器消费）
type cwdEntry struct {
	Name     string `json:"name"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

// BrowseCwd 列出目录条目（AR-06，GET /v1/claw-console/cwd/browse?path=）。
// path 为空时默认用户主目录；目录不存在或不可读返回错误。仅列直接子条目，目录优先排序。
func (h *ClawCwdHandler) BrowseCwd(c *gin.Context) {
	rel := c.Query("path")
	dir := rel
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home = string(filepath.Separator)
		}
		dir = home
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "路径无效: " + err.Error()})
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1001, "message": "路径不存在: " + err.Error()})
		return
	}
	if !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "路径不是目录"})
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "读取目录失败: " + err.Error()})
		return
	}
	result := make([]cwdEntry, 0, len(entries))
	for _, e := range entries {
		var size, modified int64
		if fi, err := e.Info(); err == nil {
			size = fi.Size()
			modified = fi.ModTime().Unix()
		}
		result = append(result, cwdEntry{
			Name:     e.Name(),
			IsDir:    e.IsDir(),
			Size:     size,
			Modified: modified,
		})
	}
	// 目录优先，再按名称排序，便于 DirectoryPicker 浏览
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return result[i].Name < result[j].Name
	})
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"path":    abs,
			"entries": result,
		},
	})
}

// ValidateCwd 校验路径存在且为目录（AR-06，POST /v1/claw-console/cwd/validate）。
// 返回 EvalSymlinks 解析后的绝对路径（防软链），供 AgentExecuteRequest.cwd 使用。
func (h *ClawCwdHandler) ValidateCwd(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "path 不能为空"})
		return
	}
	abs, err := filepath.Abs(req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "路径无效: " + err.Error()})
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1001, "message": "路径不存在: " + err.Error()})
		return
	}
	if !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "路径不是目录"})
		return
	}
	// EvalSymlinks 解析软链，返回真实绝对路径（防软链逃逸的校验基线）
	resolved := abs
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		resolved = filepath.Clean(r)
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"cwd":  resolved,
			"path": req.Path,
		},
	})
}
