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

// ModuleStatus 模块在线状态
type ModuleRuntimeStatus string

const (
	ModuleStatusOnline   ModuleRuntimeStatus = "online"
	ModuleStatusOffline  ModuleRuntimeStatus = "offline"
	ModuleStatusDisabled ModuleRuntimeStatus = "disabled"
)

// ModuleTransportType 模块与网关之间的通信/传输类型
// 注意：这与 ToolManifest.runtime_type（运行时层级 builtin/wasm/sidecar/remote）含义不同。
type ModuleTransportType string

const (
	// ModuleTransportTypeModule 通过 ModuleRegistry 调用模块的 /health + /execute
	ModuleTransportTypeModule ModuleTransportType = "module"
	// ModuleTransportTypeRemoteURL 直接调用远程 HTTP endpoint
	ModuleTransportTypeRemoteURL ModuleTransportType = "remote_url"
	// ModuleTransportTypeMCP 通过 Streamable HTTP JSON-RPC 调用 MCP 服务（tools/call）
	ModuleTransportTypeMCP ModuleTransportType = "mcp"
)

// ModuleRecord 集市模块持久化记录
// 任何实现标准 /health + /execute 接口的插件都作为一条 ModuleRecord 注册到网关。
type ModuleRecord struct {
	ID            string              `gorm:"primaryKey" json:"module_id"`
	Name          string              `gorm:"not null" json:"name"`
	Description   string              `json:"description"`
	URL           string              `gorm:"not null" json:"url"`
	TransportType ModuleTransportType `gorm:"not null" json:"transport_type"`
	Status        ModuleRuntimeStatus `gorm:"default:offline" json:"status"`
	Capabilities  string              `json:"capabilities"` // JSON ["scrape", ...]
	Version       string              `json:"version"`
	AuthToken     string              `json:"auth_token,omitempty"`
	LastHeartbeat *time.Time          `json:"last_heartbeat,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	// ====== P4 第三方模块安装信息（镜像 + 签名）======
	// 官方预置模块（Official=true）这些字段为空，无需拉镜像。
	// 第三方模块从云端拉取 ModuleInstallMeta 后填充，记录已安装的镜像来源与签名。
	Official      bool   `gorm:"default:false" json:"official"` // 是否官方预置模块（免镜像）
	ImageRef      string `json:"image_ref,omitempty"`           // 完整镜像引用 registry/repo@digest 或 registry/repo:tag
	ImageDigest   string `json:"image_digest,omitempty"`        // 镜像 sha256 摘要（内容寻址，防篡改）
	Signature     string `json:"signature,omitempty"`           // cosign/sigstore 签名（校验通过方可激活）
	InstallSource string `json:"install_source,omitempty"`      // 安装来源：cloud-purchased（云端安装，含官方，激活需 VIP1+）/ local；本地扫描预置为空
	// SourceOrigin 模块来源属性（eleball_cloud/eleball_builtin/user/mcp），见 SkillRuntimeSourceOrigin。
	// 视图字段，由 runtimeToModuleRecord 从 SkillRuntime 填充，供管理后台「模块来源」列展示。
	SourceOrigin string `json:"source_origin,omitempty"`
	// SourceActor 来源主体（user=用户名 / mcp=MCP 名）；eleball_* 为空。
	SourceActor string `json:"source_actor,omitempty"`
	// 以下字段不在数据库中，由实时健康探测填充
	HealthError string `gorm:"-" json:"error,omitempty"`
	// RequiredEnv 该模块启动所需本地环境：docker / python / node / process（none/external 为空）。
	// 控制台对离线模块据此给出「需要 Docker/Python 环境」说明。
	RequiredEnv string `gorm:"-" json:"required_env,omitempty"`
	// Activated 是否已被购买激活（claw 单用户任意购买即 true）。控制台据此区分
	// 「未激活 -> 离线+环境说明」与「已激活但离线 -> 探活错误」。
	Activated bool `gorm:"-" json:"activated"`
}

// CapabilitiesList 把 capabilities JSON 解析为字符串切片
func (m *ModuleRecord) CapabilitiesList() []string {
	if m.Capabilities == "" {
		return nil
	}
	var caps []string
	_ = json.Unmarshal([]byte(m.Capabilities), &caps)
	return caps
}

// SetCapabilities 把字符串切片序列化到 capabilities 字段
func (m *ModuleRecord) SetCapabilities(caps []string) {
	if len(caps) == 0 {
		m.Capabilities = "[]"
		return
	}
	b, _ := json.Marshal(caps)
	m.Capabilities = string(b)
}

// ModuleRegisterRequest 插件自助注册请求
// 插件启动后调用集市注册接口上报自身信息。
// module_id 可选：若留空，网关会根据 name 自动生成唯一 slug；
// 若插件自行指定，则作为建议值，冲突时后台可重命名。
type ModuleRegisterRequest struct {
	ModuleID      string   `json:"module_id"`
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
