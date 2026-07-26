package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// TeamMemoryRepo 组共享记忆数据访问（Agent Team P2）
type TeamMemoryRepo struct {
	db *gorm.DB
}

// NewTeamMemoryRepo 创建仓库
func NewTeamMemoryRepo(db *gorm.DB) *TeamMemoryRepo {
	return &TeamMemoryRepo{db: db}
}

// Create 创建记忆条目
func (r *TeamMemoryRepo) Create(m *model.TeamMemory) error {
	return r.db.Create(m).Error
}

// GetByID 根据 ID 查询记忆条目
func (r *TeamMemoryRepo) GetByID(id string) (*model.TeamMemory, error) {
	var m model.TeamMemory
	if err := r.db.First(&m, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// ListByTeam 分页查询组内记忆（按 created_at 倒序），返回条目与总数
func (r *TeamMemoryRepo) ListByTeam(teamID string, page, pageSize int) ([]model.TeamMemory, int64, error) {
	var total int64
	if err := r.db.Model(&model.TeamMemory{}).Where("team_id = ?", teamID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.TeamMemory
	err := r.db.Where("team_id = ?", teamID).
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&items).Error
	return items, total, err
}

// ListRecent 查询组内最近 N 条记忆（检索评分候选集用，按 created_at 倒序）
func (r *TeamMemoryRepo) ListRecent(teamID string, limit int) ([]model.TeamMemory, error) {
	var items []model.TeamMemory
	err := r.db.Where("team_id = ?", teamID).
		Order("created_at DESC").
		Limit(limit).
		Find(&items).Error
	return items, err
}

// Delete 删除记忆条目
func (r *TeamMemoryRepo) Delete(id string) error {
	return r.db.Delete(&model.TeamMemory{}, "id = ?", id).Error
}

// DeleteByTeam 删除组内全部记忆（删除组时级联清理）
func (r *TeamMemoryRepo) DeleteByTeam(teamID string) error {
	return r.db.Delete(&model.TeamMemory{}, "team_id = ?", teamID).Error
}

// CountByTeam 统计组内记忆条数
func (r *TeamMemoryRepo) CountByTeam(teamID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.TeamMemory{}).Where("team_id = ?", teamID).Count(&count).Error
	return count, err
}

// SearchByKeyword 按关键词检索组内记忆（Content 或 Tags LIKE，按 created_at 倒序，limit 截断）
func (r *TeamMemoryRepo) SearchByKeyword(teamID, keyword string, limit int) ([]model.TeamMemory, error) {
	var items []model.TeamMemory
	like := "%" + keyword + "%"
	err := r.db.Where("team_id = ? AND (content LIKE ? OR tags LIKE ?)", teamID, like, like).
		Order("created_at DESC").
		Limit(limit).
		Find(&items).Error
	return items, err
}
