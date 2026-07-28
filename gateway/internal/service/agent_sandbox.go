package service

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileSandbox 文件路径沙箱，限制工具只能访问指定目录
type FileSandbox struct {
	basePath      string
	knowledgeBase string
	// AR-06：claw 本地工作目录根（per-session，经 WithProjectRoot 克隆设置）。
	// 空表示未启用 cwd（云端多租户）。ReadFile/WriteFile 据此放行第三根。
	projectRoot string
}

// NewFileSandbox 创建沙箱
func NewFileSandbox(basePath, knowledgeBase string) *FileSandbox {
	return &FileSandbox{
		basePath:      basePath,
		knowledgeBase: knowledgeBase,
	}
}

// WithProjectRoot 返回带 projectRoot 的浅拷贝（AR-06 per-session，避免改共享单例）。
// projectRoot 应为已 EvalSymlinks 的绝对路径。
func (fs *FileSandbox) WithProjectRoot(projectRoot string) *FileSandbox {
	return &FileSandbox{
		basePath:      fs.basePath,
		knowledgeBase: fs.knowledgeBase,
		projectRoot:   projectRoot,
	}
}

// ResolvePath 将用户传入的相对路径解析为绝对路径
// 仅允许：{basePath}/{user_id}/conversations/{conversation_id}/... 或 {knowledgeBase}/...
func (fs *FileSandbox) ResolvePath(userID, conversationID, relPath string) (string, error) {
	if strings.Contains(relPath, "..") {
		return "", errors.New("路径包含非法字符")
	}

	// 优先尝试 conversation 目录
	conversationDir := filepath.Join(fs.basePath, userID, "conversations", conversationID)
	absPath := filepath.Join(conversationDir, relPath)
	if strings.HasPrefix(absPath, conversationDir) {
		return absPath, nil
	}

	// 其次尝试知识库目录
	kbPath := filepath.Join(fs.knowledgeBase, relPath)
	if strings.HasPrefix(kbPath, fs.knowledgeBase) {
		return kbPath, nil
	}

	return "", errors.New("访问路径超出允许范围")
}

// FileEntry 目录条目（AR-06，对齐 pi-web FileEntry，供 /cwd/browse 与文件浏览器用）
type FileEntry struct {
	Name     string `json:"name"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // unix seconds
}

// underDir 严格判断 path 是否在 dir 内（防前缀碰撞：/a/b 不算在 /a/bc 内）
func underDir(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// ResolveProjectPath 将相对路径解析为 cwd 下的绝对路径（AR-06 claw cwd）。
// 安全：filepath.Clean 规范化 + 拒绝 .. + EvalSymlinks 解析软链后必须仍在 cwd 下（防软链逃逸）。
// cwd 应为已 EvalSymlinks 的绝对路径（由 /cwd/validate 校验）。
func (fs *FileSandbox) ResolveProjectPath(cwd, relPath string) (string, error) {
	if cwd == "" {
		return "", errors.New("工作目录未配置")
	}
	if strings.Contains(relPath, "..") {
		return "", errors.New("路径包含非法字符")
	}
	cwdClean := filepath.Clean(cwd)
	absPath := filepath.Clean(filepath.Join(cwdClean, relPath))
	// EvalSymlinks 解析软链；文件不存在时返回错误，用 Clean 后路径继续前缀校验（写场景文件尚未创建）
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = filepath.Clean(resolved)
	}
	if !underDir(absPath, cwdClean) {
		return "", errors.New("访问路径超出工作目录范围")
	}
	return absPath, nil
}

// ListDir 列出 cwd 下子目录条目（AR-06，供 /cwd/browse 与文件浏览器用）。
// relPath 为空或 "." 表示 cwd 自身。
func (fs *FileSandbox) ListDir(cwd, relPath string) ([]FileEntry, error) {
	absPath, err := fs.ResolveProjectPath(cwd, relPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}
	result := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		var size, modified int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
			modified = info.ModTime().Unix()
		}
		result = append(result, FileEntry{
			Name:     e.Name(),
			IsDir:    e.IsDir(),
			Size:     size,
			Modified: modified,
		})
	}
	return result, nil
}

// ConversationDir 获取 conversation 目录，若不存在则创建
func (fs *FileSandbox) ConversationDir(userID, conversationID string) (string, error) {
	dir := filepath.Join(fs.basePath, userID, "conversations", conversationID)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("创建 conversation 目录失败: %w", err)
	}
	return dir, nil
}

// SessionDir 获取 Agent Session 目录，若不存在则创建
func (fs *FileSandbox) SessionDir(userID, sessionID string) (string, error) {
	dir := filepath.Join(fs.basePath, userID, "sessions", sessionID)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("创建 session 目录失败: %w", err)
	}
	return dir, nil
}

// RemoveSessionDir 删除 Session 磁盘目录（接收绝对路径，需校验在 basePath 下）
func (fs *FileSandbox) RemoveSessionDir(dir string) error {
	if dir == "" {
		return nil
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	baseAbs, err := filepath.Abs(fs.basePath)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(absDir, baseAbs) {
		return errors.New("待删除目录不在沙箱内")
	}
	return os.RemoveAll(absDir)
}

// ReadFile 读取沙箱内文件内容（接收绝对路径，需校验在 basePath 或 knowledgeBase 下）
func (fs *FileSandbox) ReadFile(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("路径为空")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	allowed := false
	if fs.basePath != "" {
		baseAbs, _ := filepath.Abs(fs.basePath)
		if strings.HasPrefix(absPath, baseAbs) {
			allowed = true
		}
	}
	if !allowed && fs.knowledgeBase != "" {
		kbAbs, _ := filepath.Abs(fs.knowledgeBase)
		if strings.HasPrefix(absPath, kbAbs) {
			allowed = true
		}
	}
	// AR-06：claw cwd 第三根（per-session 克隆时设置）
	if !allowed && fs.projectRoot != "" {
		if underDir(absPath, filepath.Clean(fs.projectRoot)) {
			allowed = true
		}
	}
	if !allowed {
		return nil, errors.New("访问路径超出允许范围")
	}
	return os.ReadFile(absPath)
}

// WriteFile 写入沙箱内文件（接收绝对路径，需校验在 basePath 或 projectRoot 下）
func (fs *FileSandbox) WriteFile(path string, data []byte) error {
	if path == "" {
		return errors.New("路径为空")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	allowed := false
	if fs.basePath != "" {
		baseAbs, _ := filepath.Abs(fs.basePath)
		if strings.HasPrefix(absPath, baseAbs) {
			allowed = true
		}
	}
	// AR-06：claw cwd 第三根
	if !allowed && fs.projectRoot != "" {
		if underDir(absPath, filepath.Clean(fs.projectRoot)) {
			allowed = true
		}
	}
	if !allowed {
		return errors.New("访问路径超出允许范围")
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0750); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	return os.WriteFile(absPath, data, 0640)
}

// WriteFileReader 从 io.Reader 流式写入沙箱内文件（接收绝对路径，需校验在 basePath 下）
func (fs *FileSandbox) WriteFileReader(path string, reader io.Reader, maxBytes int64) error {
	if path == "" {
		return errors.New("路径为空")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if fs.basePath == "" {
		return errors.New("沙箱基础路径未配置")
	}
	baseAbs, _ := filepath.Abs(fs.basePath)
	if !strings.HasPrefix(absPath, baseAbs) {
		return errors.New("访问路径超出允许范围")
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0750); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	file, err := os.Create(absPath)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	// 校验实际大小
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("读取文件信息失败: %w", err)
	}
	if info.Size() > maxBytes {
		os.Remove(absPath)
		return fmt.Errorf("文件大小超过限制 %d MB", maxBytes/1024/1024)
	}
	return nil
}

// UploadPath 获取用户上传文件保存路径，并确保 uploads 目录存在
func (fs *FileSandbox) UploadPath(filename string) (string, error) {
	if filename == "" {
		return "", errors.New("文件名为空")
	}
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return "", errors.New("文件名包含非法字符")
	}
	if fs.basePath == "" {
		return "", errors.New("沙箱基础路径未配置")
	}
	dir := filepath.Join(fs.basePath, "uploads")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("创建上传目录失败: %w", err)
	}
	return filepath.Join(dir, filename), nil
}

// EnsureWithinBase 校验给定绝对路径是否在 basePath 下
func (fs *FileSandbox) EnsureWithinBase(path string) error {
	if path == "" {
		return errors.New("路径为空")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if fs.basePath == "" {
		return errors.New("沙箱基础路径未配置")
	}
	baseAbs, _ := filepath.Abs(fs.basePath)
	if !strings.HasPrefix(absPath, baseAbs) {
		return errors.New("访问路径超出允许范围")
	}
	return nil
}

// withinSandbox 校验绝对路径在沙箱允许范围内（basePath / knowledgeBase / projectRoot 三根）。
// 供 Mkdir/Move/Remove/RemoveAll/ListDirAbs 等文件管理工具复用，统一三根放行逻辑。
// 用 underDir 严格判断（防前缀碰撞：/a/b 不算在 /a/bc 内）。
func (fs *FileSandbox) withinSandbox(absPath string) error {
	if absPath == "" {
		return errors.New("路径为空")
	}
	allowed := false
	if fs.basePath != "" {
		if baseAbs, err := filepath.Abs(fs.basePath); err == nil && underDir(absPath, baseAbs) {
			allowed = true
		}
	}
	if !allowed && fs.knowledgeBase != "" {
		if kbAbs, err := filepath.Abs(fs.knowledgeBase); err == nil && underDir(absPath, kbAbs) {
			allowed = true
		}
	}
	if !allowed && fs.projectRoot != "" {
		if underDir(absPath, filepath.Clean(fs.projectRoot)) {
			allowed = true
		}
	}
	if !allowed {
		return errors.New("访问路径超出允许范围")
	}
	return nil
}

// isSandboxRoot 判断路径是否为沙箱根自身（basePath / knowledgeBase / projectRoot）。
// RemoveAll 据此拒绝删根，防误删整个工作区 / 会话存储根。
func (fs *FileSandbox) isSandboxRoot(absPath string) bool {
	absClean := filepath.Clean(absPath)
	if fs.basePath != "" {
		if baseAbs, err := filepath.Abs(fs.basePath); err == nil && absClean == filepath.Clean(baseAbs) {
			return true
		}
	}
	if fs.knowledgeBase != "" {
		if kbAbs, err := filepath.Abs(fs.knowledgeBase); err == nil && absClean == filepath.Clean(kbAbs) {
			return true
		}
	}
	if fs.projectRoot != "" && absClean == filepath.Clean(fs.projectRoot) {
		return true
	}
	return false
}

// Mkdir 在沙箱内递归创建目录。接收绝对路径，需校验在沙箱内。
func (fs *FileSandbox) Mkdir(absPath string) error {
	if err := fs.withinSandbox(absPath); err != nil {
		return err
	}
	return os.MkdirAll(absPath, 0750)
}

// Move 在沙箱内移动/重命名文件或目录。src、dst 都须在沙箱内。
func (fs *FileSandbox) Move(srcAbs, dstAbs string) error {
	if err := fs.withinSandbox(srcAbs); err != nil {
		return err
	}
	if err := fs.withinSandbox(dstAbs); err != nil {
		return err
	}
	return os.Rename(srcAbs, dstAbs)
}

// Remove 删除沙箱内单个文件或空目录。接收绝对路径，需校验在沙箱内。
// 非空目录会报错（os.Remove 语义）；递归删目录用 RemoveAll。
func (fs *FileSandbox) Remove(absPath string) error {
	if err := fs.withinSandbox(absPath); err != nil {
		return err
	}
	return os.Remove(absPath)
}

// RemoveAll 递归删除沙箱内目录。拒删沙箱根自身（防误删整个工作区）。
// 接收绝对路径，需校验在沙箱内且非根。
func (fs *FileSandbox) RemoveAll(absPath string) error {
	if err := fs.withinSandbox(absPath); err != nil {
		return err
	}
	if fs.isSandboxRoot(absPath) {
		return errors.New("禁止删除工作目录根")
	}
	return os.RemoveAll(absPath)
}

// ListDirAbs 列出绝对路径下的直接子条目（供 LLM ListDir 工具用）。
// 区别于 ListDir(cwd, relPath) 的 cwd 相对路径签名；接收绝对路径，需校验在沙箱内。
func (fs *FileSandbox) ListDirAbs(absPath string) ([]FileEntry, error) {
	if err := fs.withinSandbox(absPath); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}
	result := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		var size, modified int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
			modified = info.ModTime().Unix()
		}
		result = append(result, FileEntry{
			Name:     e.Name(),
			IsDir:    e.IsDir(),
			Size:     size,
			Modified: modified,
		})
	}
	return result, nil
}
