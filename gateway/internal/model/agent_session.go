package model

// AgentSession Agent 工作流会话
type AgentSession struct {
	ID           string    `gorm:"primaryKey;size:32" json:"id"`
	UserID       string    `gorm:"index:idx_agent_session_user_created;not null;size:32" json:"user_id"`
	ConversationID string  `gorm:"index;size:32" json:"conversation_id,omitempty"`
	Title        string    `gorm:"type:text" json:"title"`
	Status       string    `gorm:"index:idx_agent_session_user_status;size:32;not null" json:"status"` // queued / running / succeeded / failed
	ToolChain    string    `gorm:"type:text" json:"tool_chain,omitempty"`            // JSON 数组，记录实际 tool_calls
	Permissions  string    `gorm:"type:text" json:"permissions,omitempty"`           // JSON 数组
	DiskPath     string    `gorm:"type:text" json:"-"`
	CreatedAt    int64     `gorm:"index:idx_agent_session_user_created;not null" json:"created_at"`
	UpdatedAt    int64     `gorm:"not null" json:"updated_at"`
	CompletedAt  *int64    `json:"completed_at,omitempty"`
}

// AgentSessionOutput Agent 工作流输出资源元数据
type AgentSessionOutput struct {
	ID         string `gorm:"primaryKey;size:32" json:"id"`
	SessionID  string `gorm:"index;not null;size:32" json:"session_id"`
	ResourceID string `gorm:"uniqueIndex;not null;size:32" json:"resource_id"`
	FileName   string `gorm:"type:text" json:"file_name"`
	MimeType   string `gorm:"type:text" json:"mime_type"`
	FileSize   int64  `json:"file_size"`
	DiskPath   string `gorm:"type:text" json:"-"`
	CreatedAt  int64  `gorm:"not null" json:"created_at"`
}

// TableName 指定 AgentSession 表名
func (AgentSession) TableName() string {
	return "agent_sessions"
}

// TableName 指定 AgentSessionOutput 表名
func (AgentSessionOutput) TableName() string {
	return "agent_session_outputs"
}
