package model

// ReleaseVersion 表示单个版本的发布信息
type ReleaseVersion struct {
	Version        string `json:"version"`
	Channel        string `json:"channel"`
	Path           string `json:"path"`
	ReleaseDate    string `json:"releaseDate"`
	Changelog      string `json:"changelog"`
	Size           int64  `json:"size"`
	ChecksumSha256 string `json:"checksumSha256"`
}

// ReleaseManifest 是 releases/{platform}/manifest.json 的内存表示
type ReleaseManifest struct {
	SchemaVersion  string                    `json:"schemaVersion"`
	Platform       string                    `json:"platform"`
	Current        map[string]string         `json:"current"`
	DefaultChannel string                    `json:"defaultChannel"`
	Versions       map[string]ReleaseVersion `json:"versions"`
}

// GetVersionByChannel 根据通道名返回对应版本号；若通道不存在则返回默认通道版本号
func (m *ReleaseManifest) GetVersionByChannel(channel string) string {
	if v, ok := m.Current[channel]; ok && v != "" {
		return v
	}
	if v, ok := m.Current[m.DefaultChannel]; ok && v != "" {
		return v
	}
	// 兜底：返回第一个有值的版本号
	for _, v := range m.Current {
		if v != "" {
			return v
		}
	}
	return ""
}

// GetVersion 根据版本号字符串获取版本详情；空字符串时返回默认通道版本
func (m *ReleaseManifest) GetVersion(version string) (ReleaseVersion, bool) {
	if version == "" || version == "latest" {
		version = m.GetVersionByChannel(m.DefaultChannel)
	}
	v, ok := m.Versions[version]
	return v, ok
}
