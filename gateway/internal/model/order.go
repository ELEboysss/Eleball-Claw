package model

import "time"

// Order 支付订单模型
type Order struct {
	ID          string     `gorm:"primaryKey;type:uuid" json:"id"`
	UserID      string     `gorm:"index;not null" json:"user_id"`
	Channel     string     `gorm:"not null" json:"channel"`          // wechat / alipay
	Amount      int64      `gorm:"not null" json:"amount"`           // 金额（分）
	Currency    string     `gorm:"default:'danwan'" json:"currency"` // 货币类型：danwan / elegant
	Status      string     `gorm:"default:pending" json:"status"`    // pending / paid / refunded / closed（超时未支付关闭）
	TradeNo     string     `gorm:"index" json:"trade_no"`            // 第三方交易号
	PackageID   string     `gorm:"index" json:"package_id"`          // 关联的充值套餐 ID
	ProductType string     `gorm:"default:'recharge'" json:"product_type"` // recharge / vip
	VIPPlanID       string     `gorm:"index" json:"vip_plan_id"`         // VIP 套餐 ID（product_type=vip 时有效）
	ElegantDeducted int64      `json:"elegant_deducted"`                 // VIP 订阅时优雅弹丸抵扣额（分）
	Quantity        int        `gorm:"default:1" json:"quantity"`        // 购买数量
	Danwan      int64      `json:"danwan"`                           // 该订单应到账弹丸数（recharge 时有效）
	CreatedAt   time.Time  `json:"created_at"`
	PaidAt      *time.Time `json:"paid_at"`
}
