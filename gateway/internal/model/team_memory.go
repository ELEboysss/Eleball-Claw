package model

// TeamMemory 组共享记忆条目（Agent Team P2，scope = user + team）。
// 组内对话执行前检索注入、执行后异步提取沉淀；严格按 user_id + team_id 隔离。
type TeamMemory struct {
	ID                   string `gorm:"primaryKey;size:32" json:"id"`
	TeamID               string `gorm:"index:idx_team_memory_team;not null;size:32" json:"team_id"`
	UserID               string `gorm:"index;not null;size:32" json:"user_id"`
	Content              string `gorm:"type:text;not null" json:"content"`                     // 事实/偏好/结论的一句话条目
	Tags                 string `gorm:"size:256" json:"tags,omitempty"`                        // 逗号分隔，便于检索过滤
	SourceConversationID string `gorm:"size:32" json:"source_conversation_id,omitempty"`       // 来源对话（provenance），手动新增为空
	CreatedAt            int64  `gorm:"not null" json:"created_at"`
	UpdatedAt            int64  `gorm:"not null" json:"updated_at"`
}

// TableName 指定 TeamMemory 表名
func (TeamMemory) TableName() string {
	return "team_memories"
}
