package model

import "time"

// ActivityEventType 动态事件类型
const (
	ActivityEventUserRegistered = "user_registered" // 用户注册
	ActivityEventUserRecharged  = "user_recharged"  // 用户充值
	ActivityEventModelUsage     = "model_usage"     // 模型调用扣费
)

// ActivityEvent 账户/平台动态事件
// 用于管理后台 Dashboard 的「最近动态」feed，以及后续审计追溯。
type ActivityEvent struct {
	ID          string    `gorm:"primaryKey;type:uuid" json:"id"`
	UserID      string    `gorm:"index;not null" json:"user_id"` // 关联用户
	Type        string    `gorm:"index;not null" json:"type"`    // 事件类型
	Title       string    `json:"title"`                         // 动态标题（可直接展示）
	Description string    `json:"description"`                   // 动态详细说明
	Metadata    string    `json:"metadata"`                      // JSON 扩展字段
	CreatedAt   time.Time `json:"created_at"`
}
