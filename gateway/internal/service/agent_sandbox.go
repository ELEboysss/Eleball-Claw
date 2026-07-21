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
}

// NewFileSandbox 创建沙箱
func NewFileSandbox(basePath, knowledgeBase string) *FileSandbox {
	return &FileSandbox{
		basePath:      basePath,
		knowledgeBase: knowledgeBase,
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
	if !allowed {
		return nil, errors.New("访问路径超出允许范围")
	}
	return os.ReadFile(absPath)
}

// WriteFile 写入沙箱内文件（接收绝对路径，需校验在 basePath 下）
func (fs *FileSandbox) WriteFile(path string, data []byte) error {
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
