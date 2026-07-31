package model

// Team 对话分组（Agent Team）：组仍严格按 user_id 隔离。
// P1 分组基座：组内对话通过 chat_conversations.team_id 归属；
// 后续 P2/P3 将在组作用域上叠加共享记忆与跨 Agent 协作能力。
type Team struct {
	ID          string `gorm:"primaryKey;size:32" json:"id"`
	UserID      string `gorm:"index:idx_team_user;not null;size:32" json:"user_id"`
	Name        string `gorm:"size:128;not null" json:"name"`
	Description string `gorm:"type:text" json:"description"`
	CreatedAt   int64  `gorm:"not null" json:"created_at"`
	UpdatedAt   int64  `gorm:"not null" json:"updated_at"`
}

// TableName 指定 Team 表名
func (Team) TableName() string {
	return "teams"
}
