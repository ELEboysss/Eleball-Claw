package model

import "time"

// RechargePackage 充值套餐配置
// 管理员可在 Admin Web 中维护套餐名称、到账弹丸数、价格、排序、上架状态。
// 当 IsCustomMultiplier=true 时，该套餐自身不设定价格和弹丸数，
// 而是基于 BasePackageID 指向的基础套餐按数量倍增（例如“重度依赖”=超大杯×N）。
type RechargePackage struct {
	ID                 string    `gorm:"primaryKey;type:uuid" json:"id"`
	Name               string    `gorm:"not null" json:"name"`                          // 展示名称，如小杯/中杯/超大杯/重度依赖
	Danwan             int64     `json:"danwan"`                                        // 到账弹丸数（自定义数量套餐可为 0）
	PriceFen           int64     `json:"price_fen"`                                     // 售价（人民币分，自定义数量套餐可为 0）
	SortOrder          int       `gorm:"default:0" json:"sort_order"`                   // 排序值，越小越靠前
	IsEnabled          bool      `gorm:"default:true" json:"is_enabled"`                // 是否上架
	IsCustomMultiplier bool      `gorm:"default:false" json:"is_custom_multiplier"`     // 是否支持自定义数量
	BasePackageID      *string   `json:"base_package_id,omitempty"`                     // 自定义数量时关联的基础套餐 ID
	Description        string    `json:"description"`                                   // 套餐描述
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
