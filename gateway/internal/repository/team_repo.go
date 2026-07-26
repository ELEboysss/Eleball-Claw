package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// TeamRepo 对话分组数据访问
type TeamRepo struct {
	db *gorm.DB
}

// NewTeamRepo 创建仓库
func NewTeamRepo(db *gorm.DB) *TeamRepo {
	return &TeamRepo{db: db}
}

// Create 创建分组
func (r *TeamRepo) Create(t *model.Team) error {
	return r.db.Create(t).Error
}

// GetByID 根据 ID 查询分组
func (r *TeamRepo) GetByID(id string) (*model.Team, error) {
	var t model.Team
	if err := r.db.First(&t, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// ListByUser 查询用户全部分组
func (r *TeamRepo) ListByUser(userID string) ([]*model.Team, error) {
	var items []*model.Team
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&items).Error
	return items, err
}

// Update 更新分组
func (r *TeamRepo) Update(t *model.Team) error {
	return r.db.Save(t).Error
}

// Delete 删除分组
func (r *TeamRepo) Delete(id string) error {
	return r.db.Delete(&model.Team{}, "id = ?", id).Error
}

// CountConversations 统计组内对话数（排除已软删除）
func (r *TeamRepo) CountConversations(teamID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ChatConversation{}).
		Where("team_id = ? AND status != ?", teamID, "deleted").
		Count(&count).Error
	return count, err
}

// ListConversations 查询组内对话列表（排除已软删除，按 updated_at 倒序）
func (r *TeamRepo) ListConversations(teamID string) ([]model.ChatConversation, error) {
	var items []model.ChatConversation
	err := r.db.Where("team_id = ? AND status != ?", teamID, "deleted").
		Order("updated_at DESC").
		Find(&items).Error
	return items, err
}

// ClearConversationRefs 清除组内所有对话的 team_id 归属（删除组时调用，不删对话）
func (r *TeamRepo) ClearConversationRefs(db *gorm.DB, teamID string) error {
	return db.Model(&model.ChatConversation{}).
		Where("team_id = ?", teamID).
		Update("team_id", "").Error
}
