package model

import "time"

// CDKType 兑换码类型
const (
	CDKTypeRecharge = "recharge" // 弹丸充值码
	CDKTypeVIP      = "vip"      // VIP 月卡兑换码
)

// CDK 兑换码库存模型
// 兑换码与主业务库分离，存放在 gateway/data/cdk.db 中。
type CDK struct {
	ID              string     `gorm:"primaryKey;type:uuid" json:"id"`
	Code            string     `gorm:"uniqueIndex;not null;size:32" json:"code"`      // 标准化后的兑换码（无横杠、大写）
	Type            string     `gorm:"default:'recharge'" json:"type"`                // recharge / vip
	Value           int64      `gorm:"not null" json:"value"`                         // 面值：recharge 时对应弹丸数（分）；vip 时可传 0
	VIPLevel        int        `json:"vip_level"`                                     // VIP 等级（type=vip 时有效）
	VIPDurationDays int        `gorm:"default:30" json:"vip_duration_days"`           // VIP 时长（天，type=vip 时有效）
	Used            bool       `gorm:"default:false;index" json:"used"`               // 是否已使用
	UsedBy          *string    `json:"used_by,omitempty"`                             // 使用人用户 ID
	UsedAt          *time.Time `json:"used_at,omitempty"`                             // 使用时间
	BatchID         string     `gorm:"index;not null;size:36" json:"batch_id"`        // 批次号，同一批生成的码共享
	Note            string     `gorm:"size:255" json:"note"`                          // 备注
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
