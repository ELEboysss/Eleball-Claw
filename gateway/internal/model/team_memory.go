package model

// TeamMemory 组共享记忆条目（Agent Team P2，scope = user + team）。
// 组内对话执行前检索注入、执行后异步提取沉淀；严格按 user_id + team_id 隔离。
type TeamMemory struct {
	ID                   string `gorm:"primaryKey;size:32" json:"id"`
	TeamID               string `gorm:"index:idx_team_memory_team;not null;size:32" json:"team_id"`
	UserID               string `gorm:"index;not null;size:32" json:"user_id"`
	Content              string `gorm:"type:text;not null" json:"content"`               // 事实/偏好/结论的一句话条目
	Tags                 string `gorm:"size:256" json:"tags,omitempty"`                  // 逗号分隔，便于检索过滤
	SourceConversationID string `gorm:"size:32" json:"source_conversation_id,omitempty"` // 来源对话（provenance），手动新增为空
	// Embedding AR-09：记忆内容的向量（float32 小端 raw BLOB，可空）。为空时检索退化为 LIKE。
	Embedding []byte `gorm:"type:blob" json:"-"`
	// Status AR-09：记忆状态--active（默认）/ superseded（合并后被取代）/ archived（TTL 归档）。
	Status string `gorm:"size:16;default:active;index:idx_team_memory_status" json:"status,omitempty"`
	// LastHitAt AR-09：最近一次检索命中时间（Unix 秒），Forget 据此归档长期未命中条目。
	LastHitAt int64 `gorm:"index" json:"last_hit_at,omitempty"`
	CreatedAt int64 `gorm:"not null" json:"created_at"`
	UpdatedAt int64 `gorm:"not null" json:"updated_at"`
}

// TableName 指定 TeamMemory 表名
func (TeamMemory) TableName() string {
	return "team_memories"
}
