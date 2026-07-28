package model

// ChatConversation Web 端对话历史（服务端明文存储，支持多设备同步）
type ChatConversation struct {
	ID           string    `gorm:"primaryKey;size:32" json:"id"`
	UserID       string    `gorm:"index:idx_chat_conv_user_updated;not null;size:32" json:"user_id"`
	Title        string    `gorm:"type:text" json:"title"`
	Model        string    `gorm:"size:128" json:"model"`
	Provider     string    `gorm:"size:32" json:"provider"`
	Status       string    `gorm:"index:idx_chat_conv_user_status;size:32;default:'active'" json:"status"` // active / archived / deleted
	EnableTools     bool   `gorm:"default:false" json:"enable_tools"`
	EnableWebSearch bool   `gorm:"default:false" json:"enable_web_search"`
	SearchProvider  string `gorm:"size:32;default:'baidu'" json:"search_provider"`
	// AssistantID 会话绑定的助手 ID（空 = 不指定助手，Agent 执行注入全部已激活秘技）
	AssistantID string `gorm:"size:64" json:"assistant_id,omitempty"`
	// TeamID 会话所属的对话分组 ID（空 = 未分组，行为同现状）
	TeamID          string `gorm:"size:32;index" json:"team_id,omitempty"`
	// Cwd AR-06：claw 本地工作目录（会话级持久，跨 execute 保持）。仅 claw（unrestricted）启用；云端恒空。
	Cwd             string `gorm:"type:text" json:"cwd,omitempty"`
	DiskPath        string `gorm:"type:text" json:"-"`
	CreatedAt    int64     `gorm:"not null" json:"created_at"`
	UpdatedAt    int64     `gorm:"index:idx_chat_conv_user_updated;not null" json:"updated_at"`
}

// ChatMessage 对话消息（明文存储）
type ChatMessage struct {
	ID                string `gorm:"primaryKey;size:32" json:"id"`
	ConversationID    string `gorm:"index:idx_chat_msg_conv_created;not null;size:32" json:"conversation_id"`
	Role              string `gorm:"size:32;not null" json:"role"` // system / user / assistant / tool
	Content           string `gorm:"type:text" json:"content"`
	ReasoningContent  string `gorm:"type:text" json:"reasoning_content,omitempty"` // 模型思考过程
	ToolResults       string `gorm:"type:text" json:"tool_results,omitempty"`      // JSON
	Attachments       string `gorm:"type:text" json:"attachments,omitempty"`       // JSON
	ClientMessageID   string `gorm:"index:idx_chat_msg_client_id;size:64" json:"client_message_id,omitempty"`
	CreatedAt         int64  `gorm:"index:idx_chat_msg_conv_created;not null" json:"created_at"`
}

// TableName 指定 ChatConversation 表名
func (ChatConversation) TableName() string {
	return "chat_conversations"
}

// TableName 指定 ChatMessage 表名
func (ChatMessage) TableName() string {
	return "chat_messages"
}
