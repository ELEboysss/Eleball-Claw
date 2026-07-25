package repository

import (
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AssistantRepo 助手数据访问
type AssistantRepo struct {
	db *gorm.DB
}

// NewAssistantRepo 创建仓库
func NewAssistantRepo(db *gorm.DB) *AssistantRepo {
	return &AssistantRepo{db: db}
}

// Create 创建助手
func (r *AssistantRepo) Create(a *model.Assistant) error {
	return r.db.Create(a).Error
}

// GetByID 根据 ID 查询助手
func (r *AssistantRepo) GetByID(id string) (*model.Assistant, error) {
	var a model.Assistant
	if err := r.db.First(&a, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// ListByUser 查询用户全部助手
func (r *AssistantRepo) ListByUser(userID string) ([]*model.Assistant, error) {
	var items []*model.Assistant
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&items).Error
	return items, err
}

// Update 更新助手
func (r *AssistantRepo) Update(a *model.Assistant) error {
	return r.db.Save(a).Error
}

// Delete 删除助手及其条目
func (r *AssistantRepo) Delete(id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("assistant_id = ?", id).Delete(&model.AssistantItem{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Assistant{}, "id = ?", id).Error
	})
}

// SetItems 事务内全量替换助手条目
func (r *AssistantRepo) SetItems(assistantID string, agentIDs []string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("assistant_id = ?", assistantID).Delete(&model.AssistantItem{}).Error; err != nil {
			return err
		}
		seen := make(map[string]bool, len(agentIDs))
		for _, agentID := range agentIDs {
			if agentID == "" || seen[agentID] {
				continue // 去重，避免唯一索引冲突
			}
			seen[agentID] = true
			if err := tx.Create(&model.AssistantItem{
				ID:          uuid.New().String(),
				AssistantID: assistantID,
				AgentID:     agentID,
				CreatedAt:   time.Now(),
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListAgentIDs 查询助手包含的秘技 ID 列表
func (r *AssistantRepo) ListAgentIDs(assistantID string) ([]string, error) {
	var ids []string
	err := r.db.Model(&model.AssistantItem{}).
		Where("assistant_id = ?", assistantID).
		Order("created_at ASC").
		Pluck("agent_id", &ids).Error
	return ids, err
}

// ClearConversationRefs 清除所有引用该助手的会话绑定
func (r *AssistantRepo) ClearConversationRefs(db *gorm.DB, assistantID string) error {
	return db.Model(&model.ChatConversation{}).
		Where("assistant_id = ?", assistantID).
		Update("assistant_id", "").Error
}
