package model

import "time"

// ToolDriverType 工具驱动类型
// 定义 SKU 可执行负载的载体，新 driver 需要先在网关注册后才能上架集市。
type ToolDriverType string

const (
	// ToolDriverNone 纯 Prompt 型秘技，不注入工具
	ToolDriverNone ToolDriverType = "none"
	// ToolDriverBuiltin 系统内置工具驱动（ReadFile/WriteFile/Shell/OCR/SearchWeb/FetchURL 等）
	ToolDriverBuiltin ToolDriverType = "builtin"
	// ToolDriverAgentReach Agent-Reach 互联网能力路由器驱动
	ToolDriverAgentReach ToolDriverType = "agent_reach"
	// ToolDriverRemoteURL 远程 HTTP 工具驱动
	ToolDriverRemoteURL ToolDriverType = "remote_url"
	// ToolDriverModule 通用集市模块驱动
	// 通常通过 driver 别名（已在 drivers 表注册）调用独立模块；也兼容 metadata.module 显式指定模块。
	ToolDriverModule ToolDriverType = "module"
	// ToolDriverDocker Docker 容器工具驱动（预留）
	ToolDriverDocker ToolDriverType = "docker"
	// ToolDriverPython Python 脚本工具驱动（预留）
	ToolDriverPython ToolDriverType = "python_script"
)

// ToolPermission 工具所需权限
type ToolPermission string

const (
	// ToolPermissionFileTools 需要服务器文件工具权限
	ToolPermissionFileTools ToolPermission = "file_tools"
	// ToolPermissionNetwork 需要网络访问权限
	ToolPermissionNetwork ToolPermission = "network"
	// ToolPermissionShell 需要执行 shell 权限
	ToolPermissionShell ToolPermission = "shell"
	// ToolPermissionAgentReach 需要 Agent-Reach 高级互联网能力权限
	ToolPermissionAgentReach ToolPermission = "agent_reach"
)

// ToolAction 工具支持的具体操作
type ToolAction struct {
	// Name 动作标识，如 web_read / search / subtitles
	Name string `json:"name"`
	// Description 动作描述，供 LLM 理解
	Description string `json:"description"`
	// Params 参数模板，key 为上游命令占位符，value 为输入参数名
	// 示例：{"query": "query", "limit": "limit"}
	Params map[string]string `json:"params,omitempty"`
}

// ToolPricing 工具按次计费元数据（per_use/per_token/per_minute，展示用）。
// 注意：与下面的 PriceDanwan/PriceElegant 不同——后者是 SKU 的一次性购买价（弹丸/优雅），
// 是 AgentItem.PriceDanwan/PriceElegant 的来源（官方 SKU 从 manifest 文件读，开发者 SKU 从创建请求读）。
type ToolPricing struct {
	// Currency 默认计费币种：danwan / elegant
	Currency string `json:"currency,omitempty"`
	// Unit 计费单位：per_use / per_token / per_minute
	Unit string `json:"unit,omitempty"`
	// UnitPrice 单份价格（人民币分）
	UnitPrice int64 `json:"unit_price,omitempty"`
}

// CredentialType 凭证类型
type CredentialType string

const (
	// CredentialTypeCookie 浏览器 Cookie 字符串
	CredentialTypeCookie CredentialType = "cookie"
	// CredentialTypeAPIKey API Key
	CredentialTypeAPIKey CredentialType = "api_key"
	// CredentialTypeToken 通用 Token
	CredentialTypeToken CredentialType = "token"
)

// CredentialScope 凭证存储粒度：决定同模块多 SKU 是否共享同一份凭证值
type CredentialScope string

const (
	// CredentialScopeModule 模块级共享：同 driver 下所有 SKU 共用一份（存 module:<driver> 桶）
	// 适用于同模块 ≥2 个 SKU 共用的上游 API Key / 平台 Cookie
	CredentialScopeModule CredentialScope = "module"
	// CredentialScopeSKU SKU 专属：仅本 SKU 使用（存 SKU 桶，默认）
	CredentialScopeSKU CredentialScope = "sku"
)

// CredentialDef 单个凭证字段定义
// SKU 通过 credentials 声明自己需要用户预先填入哪些登录态或密钥。
type CredentialDef struct {
	// Type 凭证类型：cookie / api_key / token
	Type CredentialType `json:"type"`
	// Label 展示标签
	Label string `json:"label,omitempty"`
	// Description 填写说明
	Description string `json:"description,omitempty"`
	// Placeholder 输入框占位文案
	Placeholder string `json:"placeholder,omitempty"`
	// Required 是否必填
	Required bool `json:"required,omitempty"`
	// Scope 存储粒度：module=同模块共享 / sku=仅本 SKU（默认，空值等同 sku）
	Scope CredentialScope `json:"scope,omitempty"`
}

// ToolManifest 工具/秘技标准描述格式
// 作为驱动与 SKU 之间的统一契约，也是第三方创作提交到弹丸集市的入口格式。
type ToolManifest struct {
	// ID 全局唯一标识，反向域名风格或项目内唯一 slug
	// 示例：com.eleball.tools.agent_reach.youtube / com.example.custom_search
	ID string `json:"id" binding:"required"`
	// Name 展示名称
	Name string `json:"name" binding:"required"`
	// Description 功能描述，会拼接到 OpenAI function description 中
	Description string `json:"description" binding:"required"`
	// Driver 驱动类型
	Driver ToolDriverType `json:"driver" binding:"required"`
	// RuntimeType 模块运行时层级：builtin / wasm / sidecar / remote
	// 用于描述工具实际运行环境，与 driver 字段配合决定调用路径。
	RuntimeType string `json:"runtime_type,omitempty"`
	// Category 分类，与 AgentItem.Category 对齐
	Category string `json:"category,omitempty"`
	// Level 秘技等级 1~6，与 AgentItem.Level 对齐
	Level int `json:"level,omitempty"`
	// PriceDanwan SKU 一次性购买价（弹丸）。官方 SKU 以 manifest 文件为准；
	// 缺省 0 表示免费。与 AgentItem.PriceDanwan 对齐。
	PriceDanwan int64 `json:"price_danwan,omitempty"`
	// PriceElegant 优雅币购买价（可选，nil 表示不接受优雅币）。
	PriceElegant *int64 `json:"price_elegant,omitempty"`
	// Permissions 所需权限列表
	Permissions []ToolPermission `json:"permissions,omitempty"`
	// Parameters OpenAI function parameters schema
	Parameters map[string]interface{} `json:"parameters" binding:"required"`
	// Actions 该工具支持的动作列表
	Actions []ToolAction `json:"actions,omitempty"`
	// Pricing 定价元数据
	Pricing ToolPricing `json:"pricing,omitempty"`
	// Metadata 驱动专属配置
	// agent_reach 可存放 {base_path, timeout, default_channels}
	// remote_url 可存放 {endpoint, headers, retry}
	// builtin 可存放 {builtin_tool: "ReadFile"}
	Metadata map[string]string `json:"metadata,omitempty"`
	// Credentials SKU 需要用户预先配置的凭证字段
	// key 为字段名，会随请求参数一并透传给模块/驱动。
	Credentials map[string]CredentialDef `json:"credentials,omitempty"`
	// TimeoutSeconds 工具执行超时，0 表示使用全局默认
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// AgentUserTool 用户已购买/激活的动态工具实例
// 购买 executable 型 AgentItem 时创建，执行 Agent 工作流时按 user_id 加载。
type AgentUserTool struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	UserID    string    `gorm:"index:idx_agent_user_tool_user;not null" json:"user_id"`
	AgentID   string    `gorm:"index:idx_agent_user_tool_agent;not null" json:"agent_id"`
	ToolName  string    `gorm:"index:idx_agent_user_tool_name;not null" json:"tool_name"`
	Active    bool      `gorm:"default:true" json:"active"`
	CreatedAt time.Time `json:"created_at"`
}
