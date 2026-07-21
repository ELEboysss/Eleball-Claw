package repository

import (
	"errors"

	"github.com/eleball/gateway/internal/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AgentCredentialRepo 管理用户为 SKU 填入的凭证
type AgentCredentialRepo struct {
	db *gorm.DB
}

// NewAgentCredentialRepo 创建凭证仓库
func NewAgentCredentialRepo(db *gorm.DB) *AgentCredentialRepo {
	return &AgentCredentialRepo{db: db}
}

// Get 查询单条凭证
func (r *AgentCredentialRepo) Get(userID, agentID, key string) (*model.AgentUserCredential, error) {
	var cred model.AgentUserCredential
	err := r.db.Where("user_id = ? AND agent_id = ? AND key = ?", userID, agentID, key).First(&cred).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &cred, nil
}

// ListByUserAgent 返回某用户某 SKU 的所有凭证
func (r *AgentCredentialRepo) ListByUserAgent(userID, agentID string) ([]*model.AgentUserCredential, error) {
	var creds []*model.AgentUserCredential
	err := r.db.Where("user_id = ? AND agent_id = ?", userID, agentID).Find(&creds).Error
	return creds, err
}

// Save 保存或更新凭证
func (r *AgentCredentialRepo) Save(cred *model.AgentUserCredential) error {
	if cred.ID == "" {
		cred.ID = uuid.New().String()
	}
	existing, err := r.Get(cred.UserID, cred.AgentID, cred.Key)
	if err != nil {
		return err
	}
	if existing == nil {
		return r.db.Create(cred).Error
	}
	return r.db.Model(existing).Update("value", cred.Value).Error
}

// Delete 删除凭证
func (r *AgentCredentialRepo) Delete(userID, agentID, key string) error {
	return r.db.Where("user_id = ? AND agent_id = ? AND key = ?", userID, agentID, key).Delete(&model.AgentUserCredential{}).Error
}

// DeleteByUserAgent 删除某用户某 SKU 的所有凭证
func (r *AgentCredentialRepo) DeleteByUserAgent(userID, agentID string) error {
	return r.db.Where("user_id = ? AND agent_id = ?", userID, agentID).Delete(&model.AgentUserCredential{}).Error
}
