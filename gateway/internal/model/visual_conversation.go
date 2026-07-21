package model

import "time"

// VisualConversation 视觉创作会话
// 用于把同一主题下的多次图片/视频生成任务组织在一起，形成可继续创作的多轮上下文。
type VisualConversation struct {
	ID        string    `gorm:"primaryKey;type:uuid" json:"id"`
	UserID    string    `gorm:"index:idx_visual_conv_user;not null" json:"user_id"`
	Title     string    `gorm:"not null" json:"title"`
	MediaType string    `gorm:"default:'image';not null" json:"media_type"` // image / video
	Status    string    `gorm:"default:'active';not null" json:"status"`    // active / archived / deleted
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
