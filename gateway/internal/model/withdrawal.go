package model

import (
	"time"
)

// WithdrawalStatus 提现状态
type WithdrawalStatus string

const (
	WithdrawalStatusPending    WithdrawalStatus = "pending"    // 待审核
	WithdrawalStatusApproved   WithdrawalStatus = "approved"   // 已通过（待付款）
	WithdrawalStatusCompleted  WithdrawalStatus = "completed"  // 已完成（已付款）
	WithdrawalStatusRejected   WithdrawalStatus = "rejected"   // 已拒绝
	WithdrawalStatusFailed     WithdrawalStatus = "failed"     // 付款失败
)

// WithdrawalRecord 提现记录
type WithdrawalRecord struct {
	ID           string           `gorm:"primaryKey" json:"id"`
	UserID       string           `gorm:"index" json:"user_id"`
	UserName     string           `json:"user_name"`
	Amount       int64            `json:"amount"`          // 提现金额（优雅弹丸 = 分 = 人民币分）
	Channel      string           `json:"channel"`         // wechat / alipay
	AccountInfo  string           `json:"account_info"`    // 收款账号（微信号/支付宝账号）
	RealName     string           `json:"real_name"`       // 收款人真实姓名
	Status       WithdrawalStatus `gorm:"default:pending" json:"status"`
	AdminNote    string           `json:"admin_note"`      // 管理员备注
	TxID         string           `json:"tx_id"`           // 第三方支付流水号
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// WithdrawalConfig 提现配置
type WithdrawalConfig struct {
	MinAmount     int64  `json:"min_amount"`     // 最小提现金额（分）
	MaxAmount     int64  `json:"max_amount"`     // 最大提现金额（分）
	DailyLimit    int64  `json:"daily_limit"`    // 每日限额（分）
	PlatformFeeRate int64 `json:"platform_fee_rate"` // 平台手续费率（万分之几，如 50 = 0.5%）
}

// DefaultWithdrawalConfig 默认配置
var DefaultWithdrawalConfig = WithdrawalConfig{
	MinAmount:       1000,  // 10 元
	MaxAmount:       50000, // 500 元
	DailyLimit:      100000,// 1000 元
	PlatformFeeRate: 50,    // 0.5%
}
