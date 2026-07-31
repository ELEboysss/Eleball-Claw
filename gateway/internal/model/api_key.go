package model

import (
	"time"
)

// ProviderApiKey 后端大模型 API Key 表
// 明文 Key 仅存在于内存，数据库只存储 AES-256-GCM 加密后的密文。
type ProviderApiKey struct {
	ID           string     `gorm:"primaryKey;type:uuid" json:"id"`
	Provider     string     `gorm:"index:idx_provider_enabled;not null" json:"provider"` // openai / deepseek / custom
	Name         string     `json:"name"`                                                 // 管理员可见别名
	BaseURL      string     `json:"base_url"`                                             // 代理 BaseURL，custom 时必填
	EncryptedKey string     `gorm:"not null" json:"-"`                                    // 密文（Base64），不对外序列化
	Nonce        string     `gorm:"not null" json:"-"`                                    // IV（Base64），不对外序列化
	KeyVersion   string     `gorm:"default:'v1'" json:"key_version"`                      // Master Key 版本
	IsEnabled    bool       `gorm:"index:idx_provider_enabled;default:true" json:"is_enabled"`
	Priority     int        `gorm:"default:0" json:"priority"`                            // 优先级，数字小优先
	DailyQuota   int64      `gorm:"default:0" json:"daily_quota"`                         // 日 token 上限，0 表示不限
	UsedTokens       int64      `gorm:"default:0" json:"used_tokens"`                       // 今日已用 token
	FailureCount     int        `gorm:"default:0" json:"failure_count"`                       // 连续失败次数
	LastError        string     `json:"last_error"`                                           // 最近一次错误信息
	LastUsedAt       *time.Time `json:"last_used_at"`                                         // 最近使用时间
	// AR-04 Key 池健康度门控（参考 providers/OmniRoute 三层 resilience）：
	// RateLimitedUntil 非空且未过期时，SelectKey 跳过该 Key；过期后自动重新纳入。
	RateLimitedUntil *time.Time `json:"rate_limited_until"`
	BackoffLevel     int        `gorm:"default:0" json:"backoff_level"`     // 指数退避级别，成功后归零
	LastErrorType    string     `gorm:"default:''" json:"last_error_type"`  // rate_limit / server_error / network / auth
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ApiKeyListItem 管理员列表接口返回的 Key 信息（不含密文）
type ApiKeyListItem struct {
	ID           string     `json:"id"`
	Provider     string     `json:"provider"`
	Name         string     `json:"name"`
	BaseURL      string     `json:"base_url"`
	KeyVersion   string     `json:"key_version"`
	IsEnabled    bool       `json:"is_enabled"`
	Priority     int        `json:"priority"`
	DailyQuota   int64      `json:"daily_quota"`
	UsedTokens       int64      `json:"used_tokens"`
	FailureCount     int        `json:"failure_count"`
	LastError        string     `json:"last_error"`
	LastUsedAt       *time.Time `json:"last_used_at"`
	RateLimitedUntil *time.Time `json:"rate_limited_until"`
	BackoffLevel     int        `json:"backoff_level"`
	LastErrorType    string     `json:"last_error_type"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ProviderStatus 某个 Provider 的 Key 统计信息
type ProviderStatus struct {
	Provider      string `json:"provider"`
	TotalKeys     int64  `json:"total_keys"`
	EnabledKeys   int64  `json:"enabled_keys"`
	AvailableKeys int64  `json:"available_keys"` // 启用且未超配额的 Key 数
}
