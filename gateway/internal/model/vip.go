package model

import "time"

// VIPPlan 会员套餐（可动态增删，与充值套餐独立；CDK 兑换的月卡也引用该等级配置）
type VIPPlan struct {
	ID               string `gorm:"primaryKey;type:uuid" json:"id"`
	Level            int    `gorm:"uniqueIndex;not null" json:"level"`   // 0/1/2/...
	Name             string `gorm:"not null" json:"name"`                // 展示名：小弹丸/强力弹丸/超级弹丸
	PriceFen         int64  `gorm:"not null" json:"price_fen"`           // 月卡价格（分）
	DurationDays     int    `gorm:"default:30" json:"duration_days"`     // 周期，默认 30 天
	DiscountPercent  int    `gorm:"default:100" json:"discount_percent"` // 计费折扣：80 表示 8 折
	MaxConversations int    `json:"max_conversations"`                   // 历史会话配额
	MaxAgentSessions int    `json:"max_agent_sessions"`                  // Agent Session 配额
	AsrQuotaMonthly  int64  `json:"asr_quota_monthly"`                   // ASR 月度额度
	AgentEnabled     bool   `gorm:"default:false" json:"agent_enabled"`  // 是否允许 Agent 模式
	FileToolsEnabled bool   `gorm:"default:false" json:"file_tools_enabled"` // 是否允许文件类服务器工具
	SortOrder        int    `gorm:"default:0" json:"sort_order"`
	IsEnabled        bool   `gorm:"default:true" json:"is_enabled"`
	Description      string `json:"description"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// VIPSubscription 用户会员订阅记录
type VIPSubscription struct {
	ID           string    `gorm:"primaryKey;type:uuid" json:"id"`
	UserID       string    `gorm:"index;not null" json:"user_id"`
	PlanID       string    `gorm:"index;not null" json:"plan_id"`
	Level        int       `json:"level"`
	PriceFen     int64     `json:"price_fen"`      // 订阅时月卡原价，用于退费时计算
	DurationDays int       `json:"duration_days"`  // 订阅时长（天）
	StartedAt    time.Time `json:"started_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Status       string    `gorm:"default:active" json:"status"` // active / expired / cancelled
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// VIP feature 常量
const (
	VIPFeatureAgentMode = "agent_mode"
	VIPFeatureFileTools = "file_tools"
	VIPFeatureDiscount  = "discount"
)
