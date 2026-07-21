package model

import "time"

// AgentUserCredential 用户为某个 SKU 预先填入的凭证字段
// 按 (user_id, agent_id, key) 唯一索引，值按原文字段存储。
type AgentUserCredential struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	UserID    string    `gorm:"index:idx_agent_user_credential_user_agent_key,unique,priority:1;not null" json:"user_id"`
	AgentID   string    `gorm:"index:idx_agent_user_credential_user_agent_key,unique,priority:2;not null" json:"agent_id"`
	Key       string    `gorm:"index:idx_agent_user_credential_user_agent_key,unique,priority:3;not null" json:"key"`
	Value     string    `gorm:"not null" json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 显式指定表名
func (AgentUserCredential) TableName() string {
	return "agent_user_credentials"
}
