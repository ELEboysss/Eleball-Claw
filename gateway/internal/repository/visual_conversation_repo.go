package repository

import (
	"time"

	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// VisualConversationRepo 视觉创作会话仓库
type VisualConversationRepo struct {
	db *gorm.DB
}

// NewVisualConversationRepo 创建仓库
func NewVisualConversationRepo(db *gorm.DB) *VisualConversationRepo {
	return &VisualConversationRepo{db: db}
}

// Create 创建会话
func (r *VisualConversationRepo) Create(conv *model.VisualConversation) error {
	if conv.Status == "" {
		conv.Status = "active"
	}
	now := time.Now()
	conv.CreatedAt = now
	conv.UpdatedAt = now
	return r.db.Create(conv).Error
}

// GetByIDAndUser 根据 ID 和用户 ID 查询会话
func (r *VisualConversationRepo) GetByIDAndUser(id, userID string) (*model.VisualConversation, error) {
	var conv model.VisualConversation
	err := r.db.Where("id = ? AND user_id = ? AND status != ?", id, userID, "deleted").First(&conv).Error
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

// ListByUser 查询用户的视觉会话列表，可按 media_type 过滤
func (r *VisualConversationRepo) ListByUser(userID, mediaType string, page, pageSize int) ([]*model.VisualConversation, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := r.db.Model(&model.VisualConversation{}).Where("user_id = ? AND status != ?", userID, "deleted")
	if mediaType != "" {
		query = query.Where("media_type = ?", mediaType)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	findQuery := r.db.Where("user_id = ? AND status != ?", userID, "deleted")
	if mediaType != "" {
		findQuery = findQuery.Where("media_type = ?", mediaType)
	}
	var items []*model.VisualConversation
	err := findQuery.Order("updated_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&items).Error
	return items, total, err
}

// Update 更新会话（目前主要用于更新标题/更新时间）
func (r *VisualConversationRepo) Update(conv *model.VisualConversation) error {
	conv.UpdatedAt = time.Now()
	return r.db.Save(conv).Error
}

// Delete 软删除会话
func (r *VisualConversationRepo) Delete(id, userID string) error {
	return r.db.Model(&model.VisualConversation{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", "deleted").Error
}
