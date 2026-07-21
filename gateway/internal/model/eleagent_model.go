package model

import "time"

// EleAgentUpstreamProtocol Ele Agent 上游模型协议类型
// 决定网关如何将请求转换为上游厂商原生协议。
// 聊天模型使用 openai_compatible / anthropic_messages；
// 视觉生成模型使用 agnes_image / agnes_video / seedance。
type EleAgentUpstreamProtocol string

const (
	// EleAgentUpstreamOpenAICompatible OpenAI 兼容协议，覆盖 OpenAI / DeepSeek / Moonshot / Qwen / 百炼等
	EleAgentUpstreamOpenAICompatible EleAgentUpstreamProtocol = "openai_compatible"
	// EleAgentUpstreamAnthropicMessages Anthropic Messages API
	EleAgentUpstreamAnthropicMessages EleAgentUpstreamProtocol = "anthropic_messages"
	// EleAgentUpstreamAgnesImage Agnes Image 文生图/图生图协议
	EleAgentUpstreamAgnesImage EleAgentUpstreamProtocol = "agnes_image"
	// EleAgentUpstreamAgnesVideo Agnes Video 文生视频/图生视频协议
	EleAgentUpstreamAgnesVideo EleAgentUpstreamProtocol = "agnes_video"
	// EleAgentUpstreamSeedance 火山引擎 Seedance 视频生成协议
	EleAgentUpstreamSeedance EleAgentUpstreamProtocol = "seedance"
	// EleAgentUpstreamSeedream 火山方舟 Seedream（即梦）图片生成协议（文生图/图生图/多图编辑）
	EleAgentUpstreamSeedream EleAgentUpstreamProtocol = "seedream"
	// EleAgentUpstreamOpenAIImage OpenAI 原生多模态图片生成协议（如 GPT-4o Image / DALL-E 3）
	// 运行时直接透传多轮 messages 历史，不经过 prompt 融合。
	EleAgentUpstreamOpenAIImage EleAgentUpstreamProtocol = "openai_image"
	// EleAgentUpstreamOpenAIVideo OpenAI 原生多模态视频生成协议（预留）
	EleAgentUpstreamOpenAIVideo EleAgentUpstreamProtocol = "openai_video"
)

// EleAgentModelConfig Ele Agent 模型配置表
// 由管理员后台配置，决定客户端可选择的平台-模型选项以及后端实际调用参数。
type EleAgentModelConfig struct {
	ID            string                   `gorm:"primaryKey;type:uuid" json:"id"`
	Provider      string                   `gorm:"index:idx_provider_model;not null" json:"provider"`              // 平台品牌，如 qwen / openai / deepseek / anthropic
	Protocol      EleAgentUpstreamProtocol `gorm:"default:'openai_compatible';not null" json:"protocol"`           // 上游协议/DTO 标准
	ModelName     string                   `gorm:"index:idx_provider_model;not null" json:"model_name"`            // 模型名，如 Qwen/Qwen3-8B
	DisplayName   string     `json:"display_name"`                                                   // 展示名称，如 通义千问 Qwen3-8B
	BaseURL       string     `json:"base_url"`                                                       // 实际调用 BaseURL
	EncryptedKey  string     `gorm:"not null" json:"-"`                                              // API Key 密文（Base64），不对外序列化
	Nonce         string     `gorm:"not null" json:"-"`                                              // 加密 IV（Base64），不对外序列化
	KeyVersion    string     `gorm:"default:'v1'" json:"key_version"`                                // Master Key 版本
	IsEnabled     bool       `gorm:"index:idx_provider_model_enabled;default:true" json:"is_enabled"` // 是否启用
	SupportsChat             bool                     `gorm:"default:false" json:"supports_chat"`              // 是否支持文字对话（对话页）
	SupportsVision           bool                     `gorm:"default:false" json:"supports_vision"`            // 是否支持视觉理解（图片输入）
	SupportsImage            bool                     `gorm:"default:false" json:"supports_image"`             // 是否支持图片生成
	SupportsVideo            bool                     `gorm:"default:false" json:"supports_video"`             // 是否支持视频生成
	SupportsImageInput       bool                     `gorm:"default:false" json:"supports_image_input"`       // 是否支持上传图片作为生成输入（图生图/图生视频）
	SupportsContinuousContext bool                    `gorm:"default:false" json:"supports_continuous_context"` // 是否支持基于会话历史连续创作（预留）
	SupportsTools            bool                     `gorm:"default:false" json:"supports_tools"`             // 是否支持函数/工具调用
	Priority         int        `gorm:"default:0" json:"priority"`                                      // 同平台同模型下的优先级
	InputPricePerCall  int64    `gorm:"default:0" json:"input_price_per_call"`                          // 输入 token 单价（弹丸 / 1M tokens）
	PricePerCall       int64    `gorm:"default:0" json:"price_per_call"`                                // 输出 token 单价（弹丸 / 1M tokens）
	PricePerGeneration int64    `gorm:"default:0" json:"price_per_generation"`                           // 按次附加费（弹丸/次），与 token 费用相加
	// 视频模型时长配置：单位秒，0 表示不限制
	VideoMinDuration  int       `gorm:"default:0" json:"video_min_duration"`                            // 最小时长（秒）
	VideoMaxDuration  int       `gorm:"default:0" json:"video_max_duration"`                            // 最大时长（秒）
	VideoDurationStep int       `gorm:"default:1" json:"video_duration_step"`                           // 时长步长（秒）
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// EleAgentModelOption 下发给客户端的模型选项（不含敏感信息）
type EleAgentModelOption struct {
	Provider       string                   `json:"provider"`
	ModelName      string                   `json:"model_name"`
	DisplayName    string                   `json:"display_name"`
	Protocol       EleAgentUpstreamProtocol `json:"protocol"`
	SupportsChat              bool   `json:"supports_chat"`
	SupportsVision            bool   `json:"supports_vision"`
	SupportsImage             bool   `json:"supports_image"`
	SupportsVideo             bool   `json:"supports_video"`
	SupportsImageInput        bool   `json:"supports_image_input"`
	SupportsContinuousContext bool   `json:"supports_continuous_context"`
	SupportsTools             bool   `json:"supports_tools"`
	InputPricePerCall int64 `json:"input_price_per_call"`
	PricePerCall     int64  `json:"price_per_call"`
	PricePerGeneration int64 `json:"price_per_generation"`
	Priority         int    `json:"priority"`
	// 视频模型时长配置：单位秒，0 表示不限制
	VideoMinDuration  int `json:"video_min_duration"`
	VideoMaxDuration  int `json:"video_max_duration"`
	VideoDurationStep int `json:"video_duration_step"`
}

// EleAgentModelListItem 管理员列表返回项（不含密文）
type EleAgentModelListItem struct {
	ID             string                   `json:"id"`
	Provider       string                   `json:"provider"`
	Protocol       EleAgentUpstreamProtocol `json:"protocol"`
	ModelName      string                   `json:"model_name"`
	DisplayName    string                   `json:"display_name"`
	BaseURL        string                   `json:"base_url"`
	KeyVersion     string                   `json:"key_version"`
	IsEnabled      bool                     `json:"is_enabled"`
	SupportsChat              bool                  `json:"supports_chat"`
	SupportsVision            bool                  `json:"supports_vision"`
	SupportsImage             bool                  `json:"supports_image"`
	SupportsVideo             bool                  `json:"supports_video"`
	SupportsImageInput        bool                  `json:"supports_image_input"`
	SupportsContinuousContext bool                  `json:"supports_continuous_context"`
	SupportsTools             bool                  `json:"supports_tools"`
	Priority          int                   `json:"priority"`
	InputPricePerCall int64                 `json:"input_price_per_call"`
	PricePerCall      int64                 `json:"price_per_call"`
	PricePerGeneration int64                `json:"price_per_generation"`
	// 视频模型时长配置：单位秒，0 表示不限制
	VideoMinDuration  int                   `json:"video_min_duration"`
	VideoMaxDuration  int                   `json:"video_max_duration"`
	VideoDurationStep int                   `json:"video_duration_step"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
}
