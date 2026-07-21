package model

import "time"

// SystemSetting 系统设置（键值对存储，支持布尔/字符串/整数等常见类型）
type SystemSetting struct {
	Key       string    `gorm:"primaryKey;type:text" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}
