package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// VisualUploadService 处理视觉生成所需的参考图/首帧图上传与生成结果转存
// 文件保存在 FileSandbox 基础路径下的 uploads/ 目录，通过随机 ID 提供公网可访问 URL。
type VisualUploadService struct {
	sandbox *FileSandbox
}

// NewVisualUploadService 创建上传服务
func NewVisualUploadService(sandbox *FileSandbox) *VisualUploadService {
	return &VisualUploadService{sandbox: sandbox}
}

var allowedImageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

var allowedVideoTypes = map[string]string{
	"video/mp4":  ".mp4",
	"video/webm": ".webm",
	"video/quicktime": ".mov",
}

// maxImageSize 单张图片最大 10MB
const maxImageSize = 10 * 1024 * 1024

// maxVideoSize 单个视频最大 100MB
const maxVideoSize = 100 * 1024 * 1024

// UploadResult 上传结果
// URL 为相对路径（如 /v1/visual/files/xxx.png），调用方可按需拼接为绝对地址。
type UploadResult struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	MIMEType string `json:"mime_type"`
}

// Save 保存上传的图片文件
func (s *VisualUploadService) Save(userID string, data []byte, contentType string) (*UploadResult, error) {
	if s.sandbox == nil {
		return nil, errors.New("文件沙箱未初始化")
	}
	if len(data) == 0 {
		return nil, errors.New("上传文件为空")
	}

	contentType = strings.ToLower(contentType)
	ext, maxSize, ok := pickMediaConfig(contentType)
	if !ok {
		return nil, fmt.Errorf("不支持的媒体类型: %s，仅支持 png/jpg/webp/mp4/webm/mov", contentType)
	}
	if len(data) > maxSize {
		return nil, fmt.Errorf("文件大小超过限制 %d MB", maxSize/1024/1024)
	}

	id := uuid.NewString() + ext
	path, err := s.sandbox.UploadPath(id)
	if err != nil {
		return nil, fmt.Errorf("生成上传路径失败: %w", err)
	}

	if err := s.sandbox.WriteFile(path, data); err != nil {
		return nil, fmt.Errorf("保存文件失败: %w", err)
	}

	return &UploadResult{
		ID:       id,
		URL:      "/v1/visual/files/" + id,
		MIMEType: contentType,
	}, nil
}

// SaveFromURL 下载远程媒体并保存到本地，返回相对 URL
// 用于将视觉生成结果（上游直链）转存到 Eleball 本地，避免上游链接过期后无法查看。
func (s *VisualUploadService) SaveFromURL(userID string, rawURL string) (*UploadResult, error) {
	if s.sandbox == nil {
		return nil, errors.New("文件沙箱未初始化")
	}
	if rawURL == "" {
		return nil, errors.New("URL 为空")
	}

	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造下载请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	req.Header.Set("Referer", rawURL)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载远程媒体失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载远程媒体失败，HTTP %d", resp.StatusCode)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	// 部分上游返回 application/octet-stream，尝试从 URL 后缀推断
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = mimeTypeFromURL(rawURL)
	}

	ext, maxSize, ok := pickMediaConfig(contentType)
	if !ok {
		return nil, fmt.Errorf("不支持的远程媒体类型: %s", contentType)
	}

	id := uuid.NewString() + ext
	path, err := s.sandbox.UploadPath(id)
	if err != nil {
		return nil, fmt.Errorf("生成上传路径失败: %w", err)
	}

	if err := s.sandbox.WriteFileReader(path, resp.Body, int64(maxSize)); err != nil {
		return nil, fmt.Errorf("保存远程媒体失败: %w", err)
	}

	return &UploadResult{
		ID:       id,
		URL:      "/v1/visual/files/" + id,
		MIMEType: contentType,
	}, nil
}

// SaveFromBase64 解码 Base64 图片数据并保存到本地
func (s *VisualUploadService) SaveFromBase64(userID string, b64 string) (*UploadResult, error) {
	if s.sandbox == nil {
		return nil, errors.New("文件沙箱未初始化")
	}
	if b64 == "" {
		return nil, errors.New("Base64 数据为空")
	}

	// 支持 data:image/png;base64,xxx 格式
	data := b64
	if idx := strings.Index(b64, ","); idx != -1 {
		data = b64[idx+1:]
	}

	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("解码 Base64 失败: %w", err)
	}

	return s.Save(userID, decoded, "image/png")
}

// pickMediaConfig 根据 Content-Type 选择扩展名与大小限制
func pickMediaConfig(contentType string) (ext string, maxSize int, ok bool) {
	contentType = strings.ToLower(contentType)
	if ext, ok = allowedImageTypes[contentType]; ok {
		return ext, maxImageSize, true
	}
	if ext, ok = allowedVideoTypes[contentType]; ok {
		return ext, maxVideoSize, true
	}
	return "", 0, false
}

// mimeTypeFromURL 从 URL 后缀推断 MIME 类型
func mimeTypeFromURL(rawURL string) string {
	idx := strings.LastIndex(rawURL, ".")
	if idx == -1 {
		return "application/octet-stream"
	}
	ext := strings.ToLower(rawURL[idx:])
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	default:
		return "application/octet-stream"
	}
}

// Delete 删除本地文件
func (s *VisualUploadService) Delete(id string) error {
	if s.sandbox == nil {
		return errors.New("文件沙箱未初始化")
	}
	if id == "" {
		return errors.New("文件 ID 为空")
	}
	// 防止路径遍历
	if strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return errors.New("非法文件 ID")
	}

	path, err := s.sandbox.UploadPath(id)
	if err != nil {
		return err
	}
	// 忽略文件不存在的错误，允许幂等删除
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除文件失败: %w", err)
	}
	return nil
}

// ReadFile 读取上传文件内容
func (s *VisualUploadService) ReadFile(path string) ([]byte, error) {
	return s.sandbox.ReadFile(path)
}

// GetBase64DataURL 根据文件 ID 读取文件并返回 Base64 Data URL（如 data:image/png;base64,xxx）。
// 用于把本地镜像文件作为参考图传给上游视觉生成 API。
func (s *VisualUploadService) GetBase64DataURL(id string) (string, error) {
	path, mimeType, err := s.GetPath(id)
	if err != nil {
		return "", err
	}
	data, err := s.sandbox.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取本地文件失败: %w", err)
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, b64), nil
}

// SaveVideoWithCover 下载远程视频并保存到本地，同时提取首帧作为封面图。
// 封面提取失败时不阻断视频保存，cover 返回 nil。
func (s *VisualUploadService) SaveVideoWithCover(userID string, rawURL string) (video *UploadResult, cover *UploadResult, err error) {
	video, err = s.SaveFromURL(userID, rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("保存视频失败: %w", err)
	}

	cover, err = s.extractVideoCover(video.ID)
	if err != nil {
		// 封面提取失败不影响视频保存，由调用方决定是否记录日志
		return video, nil, nil
	}
	return video, cover, nil
}

// extractVideoCover 使用 ffmpeg 从本地视频提取首帧封面图。
func (s *VisualUploadService) extractVideoCover(videoID string) (*UploadResult, error) {
	ffmpegPath := findFFmpeg()
	if ffmpegPath == "" {
		return nil, errors.New("未找到 ffmpeg，无法提取视频封面")
	}

	videoPath, _, err := s.GetPath(videoID)
	if err != nil {
		return nil, err
	}

	ext := filepath.Ext(videoID)
	coverID := strings.TrimSuffix(videoID, ext) + ".cover.jpg"
	coverPath, err := s.sandbox.UploadPath(coverID)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(ffmpegPath,
		"-i", videoPath,
		"-ss", "00:00:00",
		"-vframes", "1",
		"-q:v", "2",
		"-y",
		coverPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg 提取封面失败: %w, output: %s", err, string(output))
	}

	return &UploadResult{
		ID:       coverID,
		URL:      "/v1/visual/files/" + coverID,
		MIMEType: "image/jpeg",
	}, nil
}

// findFFmpeg 查找 ffmpeg 可执行文件路径。
// 优先使用 FFMPEG_PATH 环境变量，其次尝试项目内置路径，最后在 PATH 中查找。
func findFFmpeg() string {
	if v := os.Getenv("FFMPEG_PATH"); v != "" {
		return v
	}
	candidates := []string{
		"../.tools/ffmpeg/ffmpeg.exe", // 从 gateway 目录运行
		".tools/ffmpeg/ffmpeg.exe",    // 从项目根目录运行
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		return path
	}
	return ""
}

// GetPath 根据文件 ID 获取磁盘路径
func (s *VisualUploadService) GetPath(id string) (string, string, error) {
	if s.sandbox == nil {
		return "", "", errors.New("文件沙箱未初始化")
	}
	if id == "" {
		return "", "", errors.New("文件 ID 为空")
	}
	// 防止路径遍历
	if strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return "", "", errors.New("非法文件 ID")
	}

	path, err := s.sandbox.UploadPath(id)
	if err != nil {
		return "", "", err
	}

	mimeType := mime.TypeByExtension(filepath.Ext(id))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return path, mimeType, nil
}
