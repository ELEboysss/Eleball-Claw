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
// 与云端 T9 submission 记录字段镜像；origin/actor 跨端保留 provenance。
type ModuleSubmissionMeta struct {
	ModuleID     string   `json:"module_id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Origin       string   `json:"origin"`
	Actor        string   `json:"actor"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// PackageCatalogItem claw 云端秘技包目录单项（GET /v1/market/modules/catalog）。
// claw 据此比对本地（package_id + version）决定下载/更新。契约见 specs/api-schema.yml PackageCatalogItem。
type PackageCatalogItem struct {
	PackageID   string    `json:"package_id"` // 包 ID（package.json name，slug，目录名）
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`         // 包语义版本，claw 比对更新主键
	Origin      string    `json:"origin"`          // builtin/cloud/user（builtin 不出现在云端 catalog）
	Official    bool      `json:"official"`        // 是否官方包（据 origin 推断；cloud 包以官方维护列表判定，user 恒 false）
	Actor       string    `json:"actor,omitempty"` // 来源主体；origin=user 时为作者
	Category    string    `json:"category,omitempty"`
	Level       int       `json:"level,omitempty"` // 使用等级要求 1-6
	UpdatedAt   time.Time `json:"updated_at"`      // 云端最后更新时间（claw 比对本地决定是否更新）
}

// PackageBundleSkill 整包内的 skill 文件条目（skills/<name>/SKILL.md）。
type PackageBundleSkill struct {
	Name    string `json:"name"`
	Content string `json:"content"` // SKILL.md 原文（含 frontmatter）
}

// PackageBundleSKU 整包内的手写 SKU 文件条目（skus/*.json；能力派生 SKU 由 package_json 在 RescanPackage 时再生成）。
type PackageBundleSKU struct {
	FileName string          `json:"file_name"` // 如 "github"（不含 .json）
	Content  json.RawMessage `json:"content"`   // SKU manifest 原文
}

// PackageBundle cloud->claw 秘技包下载包（GET /v1/market/modules/{id}/package），完整 package：
// package.json + skills/*/SKILL.md + 工具实现脚本 + skus/*.json + mcp 配置。claw 解包落盘
// marketplace/<id>/ 后触发 RescanPackage 物化。契约见 specs/api-schema.yml PackageBundle。
// 注：provenance 经 tools_files[".origin"] 侧车下发（package.json 不声明 origin 防伪造，T1.3）。
type PackageBundle struct {
	PackageVersion int                         `json:"package_version"` // 打包格式版本（当前 2）
	PackageID      string                      `json:"package_id"`
	PackageJSON    json.RawMessage             `json:"package_json"` // package.json 原文（claw 解包写入 marketplace/<id>/package.json）
	Skills         []PackageBundleSkill        `json:"skills,omitempty"`
	ToolsFiles     map[string]string           `json:"tools_files,omitempty"` // 工具实现脚本/资源文件（相对路径: 文本内容），含 .origin 侧车
	SKUs           []PackageBundleSKU          `json:"skus,omitempty"`
	MCPConfigs     map[string]PackageMCPServer `json:"mcp_configs,omitempty"` // mcpServers 各 server 连接配置（与 package_json.mcpServers 对应）
	Version        string                      `json:"version"`               // 包语义版本
	UpdatedAt      time.Time                   `json:"updated_at"`
}
