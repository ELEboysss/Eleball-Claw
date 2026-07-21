package model

import (
	"time"

	"gorm.io/gorm"
)

// UserRole 用户角色
const (
	UserRoleUser  = "user"
	UserRoleAdmin = "admin"
)

// User 用户模型
type User struct {
	ID             string         `gorm:"primaryKey;type:uuid" json:"id"`
	Username       string         `gorm:"uniqueIndex;not null" json:"username"`
	Password       string         `json:"-"` // 不回传密码；可空（邮箱 OTP 用户无密码）
	Nickname       string         `json:"nickname"`
	Email          string         `gorm:"index" json:"email"`           // 邮箱，可空（兼容老用户与 username/password 用户）
	Verified       bool           `gorm:"default:false" json:"verified"` // 邮箱是否已验证
	AvatarURL      string         `json:"avatar_url"`
	Role           string         `gorm:"default:user" json:"role"`         // user / admin
	Balance        int64          `gorm:"default:0" json:"balance"`         // 弹丸余额（分，1 弹丸 = 1 分）
	ElegantBalance int64          `gorm:"default:0" json:"elegant_balance"` // 优雅弹丸余额（分，1 优雅弹丸 = 1 分）
	TotalRecharged int64          `gorm:"default:0" json:"total_recharged"` // 累计充值金额（人民币分）
	Status            int            `gorm:"default:1" json:"status"`               // 1:正常 0:禁用
	AsrQuotaMonthly   int64          `gorm:"default:1000" json:"asr_quota_monthly"` // 语音识别月度额度（次）
	AsrQuotaUsed      int64          `gorm:"default:0" json:"asr_quota_used"`       // 本月已用语音识别次数
	AsrQuotaResetAt   time.Time      `json:"asr_quota_reset_at"`                    // 最近一次额度刷新时间
	VIPLevel          int            `gorm:"column:vip_level;default:0" json:"vip_level"`            // 当前生效 VIP 等级
	VIPExpireAt       time.Time      `gorm:"column:vip_expire_at" json:"vip_expire_at"`              // VIP 到期时间
	VIPPlanID         *string        `gorm:"column:vip_plan_id" json:"vip_plan_id,omitempty"`        // 当前生效套餐 ID
	AgentTrialUsed    int            `gorm:"column:agent_trial_used;default:0" json:"agent_trial_used"` // VIP0 用户已试用 Agent 模式次数
	AgentTrialResetAt time.Time      `gorm:"column:agent_trial_reset_at" json:"agent_trial_reset_at"` // Agent 模式试用次数最近刷新时间
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `gorm:"index" json:"-"`
}

// Device 用户设备绑定
type Device struct {
	ID        string    `gorm:"primaryKey;type:uuid" json:"id"`
	UserID    string    `gorm:"index;not null" json:"user_id"`
	DeviceID  string    `gorm:"uniqueIndex;not null" json:"device_id"`
	Name      string    `json:"name"` // 设备名称，如 "iPhone 15"
	LastLogin time.Time `json:"last_login"`
	CreatedAt time.Time `json:"created_at"`
}
