package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eleball/gateway/internal/model"
)

// ReleaseService 负责管理版本化发布产物（APK 等）的清单读取与文件下载
type ReleaseService struct {
	rootPath string
}

// NewReleaseService 创建 ReleaseService
// rootPath 为 releases/ 目录所在绝对路径，例如 /app/releases 或项目根目录下的 releases
func NewReleaseService(rootPath string) *ReleaseService {
	return &ReleaseService{rootPath: rootPath}
}

// RootPath 返回发布产物根目录
func (s *ReleaseService) RootPath() string {
	return s.rootPath
}

// manifestPath 返回指定平台 manifest.json 的绝对路径
func (s *ReleaseService) manifestPath(platform string) string {
	return filepath.Join(s.rootPath, platform, "manifest.json")
}

// LoadManifest 加载指定平台的 manifest.json
func (s *ReleaseService) LoadManifest(platform string) (*model.ReleaseManifest, error) {
	path := s.manifestPath(platform)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 manifest 失败: %w", err)
	}

	var manifest model.ReleaseManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("解析 manifest 失败: %w", err)
	}

	if manifest.Platform == "" {
		manifest.Platform = platform
	}
	if manifest.DefaultChannel == "" {
		// 没有默认通道时，尝试取 current 中的 stable
		if _, ok := manifest.Current["stable"]; ok {
			manifest.DefaultChannel = "stable"
		} else {
			// 否则取第一个 key
			for k := range manifest.Current {
				manifest.DefaultChannel = k
				break
			}
		}
	}

	return &manifest, nil
}

// ResolveDownload 解析下载请求，返回 manifest、版本信息、文件绝对路径和 Content-Type
func (s *ReleaseService) ResolveDownload(platform, channel, version string) (*model.ReleaseManifest, model.ReleaseVersion, string, string, error) {
	manifest, err := s.LoadManifest(platform)
	if err != nil {
		return nil, model.ReleaseVersion{}, "", "", err
	}

	var ver model.ReleaseVersion
	var ok bool

	if version != "" {
		ver, ok = manifest.GetVersion(version)
		if !ok {
			return nil, model.ReleaseVersion{}, "", "", fmt.Errorf("版本 %s 不存在", version)
		}
	} else if channel != "" {
		ver, ok = manifest.GetVersion(manifest.GetVersionByChannel(channel))
		if !ok {
			return nil, model.ReleaseVersion{}, "", "", fmt.Errorf("通道 %s 没有可用版本", channel)
		}
	} else {
		ver, ok = manifest.GetVersion("")
		if !ok {
			return nil, model.ReleaseVersion{}, "", "", fmt.Errorf("没有可用版本")
		}
	}

	filePath := filepath.Join(s.rootPath, platform, ver.Path)
	contentType := "application/vnd.android.package-archive"
	if filepath.Ext(filePath) == ".json" {
		contentType = "application/json"
	}

	return manifest, ver, filePath, contentType, nil
}
