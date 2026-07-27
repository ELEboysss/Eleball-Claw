package model

import "time"

// Assistant 助手：已激活秘技的命名组合，按会话应用到 Agent 工作流。
// 会话绑定助手后，Agent 执行时仅注入助手包含的秘技工具，而非全部已激活秘技。
type Assistant struct {
	ID          string `gorm:"primaryKey;size:64" json:"id"`
	UserID      string `gorm:"index:idx_assistants_user;not null;size:64" json:"user_id"`
	Name        string `gorm:"not null" json:"name"`
	Description string `gorm:"type:text" json:"description"`
	// Agent Team P3：助手作为子 agent 被 CallAssistant 编排时的人格/指令；空=默认专家模板
	SystemPrompt string `gorm:"type:text" json:"system_prompt"`
	// Agent Team P3：是否对其他编排者暴露能力（进入能力目录）
	Shared bool `gorm:"default:true" json:"shared"`
	// Agent Team P3：空=全局可见（所有组的编排者可见）；非空=仅该组可见
	TeamID string `gorm:"size:32" json:"team_id"`
	// Agent Team P5：助手级 LLM 配置（子 agent 模型覆盖）。follow=跟随当前对话；eleagent=指定 Ele Agent 模型；byok=自带凭据
	LLMMode     string `gorm:"size:16;default:'follow'" json:"llm_mode"`
	LLMProvider string `gorm:"size:32" json:"llm_provider"`   // byok: openai/deepseek/qwen/moonshot/custom
	LLMModel    string `gorm:"type:text" json:"llm_model"`    // byok 模型名 或 eleagent 的 "subProvider/subModel"
	LLMBaseURL  string `gorm:"type:text" json:"llm_base_url"` // byok 自定义端点
	// BYOK api_key：AES-256-GCM 密文（绝不序列化；响应只回 llm_api_key_set 布尔，见 AssistantView）
	LLMAPIKey        string    `gorm:"type:text" json:"-"`
	LLMAPIKeyNonce   string    `gorm:"type:text" json:"-"`
	LLMAPIKeyVersion string    `gorm:"default:''" json:"llm_api_key_version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// AssistantItem 助手包含的秘技条目（同一助手内 agent_id 唯一）
type AssistantItem struct {
	ID          string    `gorm:"primaryKey;size:64" json:"id"`
	AssistantID string    `gorm:"uniqueIndex:uk_assistant_agent;index:idx_assistant_items_assistant;not null;size:64" json:"assistant_id"`
	AgentID     string    `gorm:"uniqueIndex:uk_assistant_agent;not null" json:"agent_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// TableName 指定 Assistant 表名
func (Assistant) TableName() string {
	return "assistants"
}

// TableName 指定 AssistantItem 表名
func (AssistantItem) TableName() string {
	return "assistant_items"
}
