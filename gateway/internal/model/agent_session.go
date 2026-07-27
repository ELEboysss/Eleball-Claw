package model

// AgentSession Agent 工作流会话
type AgentSession struct {
	ID             string `gorm:"primaryKey;size:32" json:"id"`
	UserID         string `gorm:"index:idx_agent_session_user_created;not null;size:32" json:"user_id"`
	ConversationID string `gorm:"index;size:32" json:"conversation_id,omitempty"`
	// Agent Team P3：子调用 provenance——编排者触发 CallAssistant 时子 session 记录父 session
	ParentSessionID string `gorm:"size:32;index" json:"parent_session_id,omitempty"`
	Title           string `gorm:"type:text" json:"title"`
	Status          string `gorm:"index:idx_agent_session_user_status;size:32;not null" json:"status"` // queued / running / succeeded / failed
	ToolChain       string `gorm:"type:text" json:"tool_chain,omitempty"`                              // JSON 数组，记录实际 tool_calls
	Permissions     string `gorm:"type:text" json:"permissions,omitempty"`                             // JSON 数组
	DiskPath        string `gorm:"type:text" json:"-"`
	// AR-06：claw 本地工作目录（用户授权的项目目录绝对路径，EvalSymlinks 后）。
	// 仅 claw（unrestricted=true）装配；云端多租户保持空，不启用 cwd 解析。
	Cwd string `gorm:"type:text" json:"cwd,omitempty"`
	// AR-06：项目根（git repo root，可空，P1-1 worktree 隔离用）。当前 P0 仅记录，不消费。
	ProjectRoot string `gorm:"type:text" json:"project_root,omitempty"`
	// AR-07：用量统计--累计 token / 工具步数 / 估算成本（弹丸），供用量可见性展示。
	// CostAmount 为 EstimateCost 估算值（未计 VIP 折扣）；claw 无 billing 时为 0（裁剪成本）。
	TotalTokens int64  `json:"total_tokens,omitempty"`
	StepCount   int    `json:"step_count,omitempty"`
	CostAmount  int64  `json:"cost_amount,omitempty"`
	CreatedAt   int64  `gorm:"index:idx_agent_session_user_created;not null" json:"created_at"`
	UpdatedAt   int64  `gorm:"not null" json:"updated_at"`
	CompletedAt *int64 `json:"completed_at,omitempty"`
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
