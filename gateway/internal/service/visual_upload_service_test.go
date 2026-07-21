package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestVisualUploadServiceSaveFromURL(t *testing.T) {
	// 创建临时沙箱目录
	tmpDir, err := os.MkdirTemp("", "visual-upload-test")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sandbox := NewFileSandbox(tmpDir, "")
	uploadService := NewVisualUploadService(sandbox)

	// 启动本地图片服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		// 简单的 1x1 PNG 数据
		data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		w.Write(data)
	}))
	defer server.Close()

	result, err := uploadService.SaveFromURL("user-1", server.URL+"/fake.png")
	if err != nil {
		t.Fatalf("SaveFromURL 失败: %v", err)
	}

	if result.URL == "" {
		t.Fatal("URL 不应为空")
	}
	if result.MIMEType != "image/png" {
		t.Fatalf("MIME 类型应为 image/png，实际 %s", result.MIMEType)
	}

	// 验证文件已保存到磁盘
	fileID := filepath.Base(result.URL)
	path, _, err := uploadService.GetPath(fileID)
	if err != nil {
		t.Fatalf("获取文件路径失败: %v", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("文件未保存到磁盘")
	}
}
