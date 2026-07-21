package model

import (
	"time"
)

// Conversation 对话记录（服务端仅存储密文，E2EE）
type Conversation struct {
	ID             string    `gorm:"primaryKey;type:uuid" json:"id"`
	UserID         string    `gorm:"index;not null" json:"user_id"`
	DeviceID       string    `gorm:"index;not null" json:"device_id"`
	EntityType     string    `gorm:"not null" json:"entity_type"`     // conversation / message / config / prompt
	EntityID       string    `gorm:"not null" json:"entity_id"`       // 业务实体 ID
	Operation      string    `gorm:"not null" json:"operation"`       // create / update / delete
	SyncVersion    int64     `gorm:"not null" json:"sync_version"`    // 单调递增版本号
	PayloadCiphertext string `json:"payload_ciphertext"`              // AES 加密后的 JSON 密文
	CreatedAt      time.Time `json:"created_at"`
}

// TokenUsage 模型调用用量记录
type TokenUsage struct {
	ID             string    `gorm:"primaryKey;type:uuid" json:"id"`
	UserID         string    `gorm:"index;not null" json:"user_id"`
	ConversationID string    `gorm:"index" json:"conversation_id"`
	ModelID        string    `gorm:"not null" json:"model_id"`
	Provider       string    `gorm:"not null" json:"provider"`
	InputTokens    int       `json:"input_tokens"`
	OutputTokens   int       `json:"output_tokens"`
	CostAmount     int64     `json:"cost_amount"` // 实际扣费（分）
	Currency       string    `gorm:"default:'danwan'" json:"currency"` // 扣费货币：danwan / elegant
	CreatedAt      time.Time `json:"created_at"`
}

// BalanceTransaction 余额流水
type BalanceTransaction struct {
	ID           string    `gorm:"primaryKey;type:uuid" json:"id"`
	UserID       string    `gorm:"index;not null" json:"user_id"`
	Type         string    `gorm:"not null" json:"type"`                  // recharge / consume / refund
	Amount       int64     `gorm:"not null" json:"amount"`                // 正值充值，负值消费
	Currency     string    `gorm:"default:'danwan'" json:"currency"`      // 交易货币：danwan / elegant
	BalanceAfter int64     `json:"balance_after"`
	Description  string    `json:"description"`
	CreatedAt    time.Time `json:"created_at"`
}
