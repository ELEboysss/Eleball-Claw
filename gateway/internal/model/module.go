package model

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var moduleIDInvalidChars = regexp.MustCompile(`[^a-z0-9_-]+`)

// GenerateModuleID 根据模块名生成一个合法的 module_id slug。
// 若名称为空或无法生成有效 slug，则返回短 UUID。
func GenerateModuleID(name string) string {
	if name == "" {
		return "mod-" + uuid.New().String()[:8]
	}
	s := strings.ToLower(strings.TrimSpace(name))
	// 把常见分隔符统一成 -
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = moduleIDInvalidChars.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	if s == "" {
		return "mod-" + uuid.New().String()[:8]
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// PluginRegisterRequest 插件自助注册请求（替代旧 ModuleRegisterRequest）。
// 插件启动后调用集市注册接口上报自身信息；模块在统一模型中即一条 SkillRuntime。
// id 可选：若留空，网关会根据 name 自动生成唯一 slug；若插件自行指定，则作为建议值。
type PluginRegisterRequest struct {
	ID            string   `json:"module_id"`
	Name          string   `json:"name" binding:"required"`
	Description   string   `json:"description"`
	URL           string   `json:"url" binding:"required,url"`
	TransportType string   `json:"transport_type" binding:"required,oneof=module remote_url mcp"`
	Capabilities  []string `json:"capabilities"`
	Version       string   `json:"version"`
	AuthToken     string   `json:"auth_token"` // 预共享注册令牌
}

// ModuleShareRequest 分享本地模块到云端审核的请求（T8）。
// UI 仅传 module_id；handler 据此查本地记录取权威元数据并打 tarball。
// 去掉旧 auth_token 鸡生蛋流程：改为先提交、审核后下发。
type ModuleShareRequest struct {
	ModuleID string `json:"module_id" binding:"required"`
}

// ModuleSubmissionMeta 发往云端的审核元数据（T8）。
// 仅供云端审核列表展示（T10），避免为列展示而逐个解压 tarball；
// 权威注册信息以 tarball 内 module.json 为准（T11 解压发布时读取）。
// 与云端 T9 submission 记录字段镜像；source_origin/source_actor 跨端保留 provenance。
type ModuleSubmissionMeta struct {
	ModuleID     string   `json:"module_id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	SourceOrigin string   `json:"source_origin"`
	SourceActor  string   `json:"source_actor"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// ModulePackage cloud->claw 下载的模块元数据包（GET /v1/market/modules/:id/package）。
// 含 module.json + skus/*.json + claw 版 docker-compose.yml（image 引用，走 ACR pull_first），
// 不含 main.py/Dockerfile（社区模块产物走 ACR 镜像，见 plan cloud-claw-module-download-sync D2）。
type ModulePackage struct {
	PackageVersion int                `json:"package_version"`         // 打包格式版本（当前 1）
	ModuleID       string             `json:"module_id"`
	ModuleJSON     json.RawMessage    `json:"module_json"`             // module.json 原文
	SKUs           []ModulePackageSKU `json:"skus"`                    // skus/*.json
	ComposeContent string             `json:"compose_content"`         // claw 版 docker-compose.yml（image 引用）
	Version        string             `json:"version,omitempty"`       // 模块语义版本（可选，claw 比对更新用）
	SourceOrigin   string             `json:"source_origin,omitempty"` // 归属（eleball_cloud/user/...）
	UpdatedAt      time.Time          `json:"updated_at"`              // 云端最后更新时间（claw 比对本地决定是否更新）
}

// ModulePackageSKU 打包包内的单个 SKU 文件条目。
type ModulePackageSKU struct {
	FileName string          `json:"file_name"` // 如 "github"（不含 .json）
	Content  json.RawMessage `json:"content"`   // SKU manifest 原文
}

// ModuleCatalogItem claw 云端模块目录单项（GET /v1/market/modules/catalog）。
// claw 据此比对本地（module_id + updated_at）决定下载/更新。见 plan cloud-claw-module-download-sync C2。
type ModuleCatalogItem struct {
	ModuleID     string    `json:"module_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Version      string    `json:"version,omitempty"`
	SourceOrigin string    `json:"source_origin,omitempty"` // 归属（eleball_cloud/user/...）
	SourceActor  string    `json:"source_actor,omitempty"`  // 来源主体（user=用户名）
	Transport    string    `json:"transport"`
	Capabilities []string  `json:"capabilities,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"` // 云端最后更新时间（claw 比对本地决定是否更新）
}
