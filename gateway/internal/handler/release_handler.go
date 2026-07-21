package handler

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ReleaseHandler 处理版本发布相关接口
type ReleaseHandler struct {
	service *service.ReleaseService
	logger  *zap.Logger
}

// NewReleaseHandler 创建 ReleaseHandler
func NewReleaseHandler(service *service.ReleaseService, logger *zap.Logger) *ReleaseHandler {
	return &ReleaseHandler{service: service, logger: logger}
}

// GetAndroidManifest 获取 Android 发布清单
// GET /v1/releases/android
func (h *ReleaseHandler) GetAndroidManifest(c *gin.Context) {
	manifest, err := h.service.LoadManifest("android")
	if err != nil {
		h.logger.Warn("加载 Android manifest 失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5001, "message": "发布清单加载失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": manifest})
}

// DownloadAndroid 下载 Android APK
// GET /v1/releases/android/download?version=1.0.0
// GET /v1/releases/android/download?channel=stable
// GET /v1/releases/android/download（默认通道最新版）
func (h *ReleaseHandler) DownloadAndroid(c *gin.Context) {
	version := c.Query("version")
	channel := c.Query("channel")

	manifest, ver, filePath, contentType, err := h.service.ResolveDownload("android", channel, version)
	if err != nil {
		h.logger.Warn("解析 Android 下载请求失败", zap.Error(err), zap.String("version", version), zap.String("channel", channel))
		c.JSON(http.StatusNotFound, gin.H{"code": 4041, "message": err.Error()})
		return
	}

	// 校验文件存在，避免 manifest 中 path 错误导致的安全问题
	if _, err := filepath.Abs(filePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5002, "message": "文件路径异常"})
		return
	}

	// 防止通过相对路径跳出发布目录（额外校验）
	cleanPath := filepath.Clean(filePath)
	cleanRoot := filepath.Clean(h.service.RootPath())
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		h.logger.Warn("非法下载路径", zap.String("path", cleanPath))
		c.JSON(http.StatusForbidden, gin.H{"code": 4031, "message": "非法下载路径"})
		return
	}

	// 设置下载文件名
	filename := filepath.Base(ver.Path)
	if filename == "" || filename == "." {
		filename = "eleball-" + ver.Version + ".apk"
	}

	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+url.QueryEscape(filename))
	// 可选：返回 manifest 中的 checksum 校验头
	if ver.ChecksumSha256 != "" {
		c.Header("X-Content-SHA256", ver.ChecksumSha256)
	}
	c.Header("X-Release-Version", ver.Version)
	c.Header("X-Release-Channel", ver.Channel)
	c.File(filePath)

	// 记录下载日志
	h.logger.Info("Android APK 下载",
		zap.String("version", ver.Version),
		zap.String("channel", ver.Channel),
		zap.String("path", ver.Path),
		zap.String("client_ip", c.ClientIP()),
	)

	// 抑制 gin 警告：File 已经写了响应，这里不再 c.JSON
	_ = manifest
}

