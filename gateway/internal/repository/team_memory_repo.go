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

// ListActiveByTeam 查询组内 active 状态的最近 N 条记忆（AR-09 检索候选集，按 created_at 倒序）。
// superseded/archived 不参与检索注入。
func (r *TeamMemoryRepo) ListActiveByTeam(teamID string, limit int) ([]model.TeamMemory, error) {
	var items []model.TeamMemory
	err := r.db.Where("team_id = ? AND status = ?", teamID, "active").
		Order("created_at DESC").
		Limit(limit).
		Find(&items).Error
	return items, err
}

// CountActiveByTeam 统计组内 active 记忆条数（AR-09 合并触发阈值判断）
func (r *TeamMemoryRepo) CountActiveByTeam(teamID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.TeamMemory{}).
		Where("team_id = ? AND status = ?", teamID, "active").
		Count(&count).Error
	return count, err
}

// UpdateEmbedding 回填单条记忆的向量（AR-09）
func (r *TeamMemoryRepo) UpdateEmbedding(id string, embedding []byte) error {
	return r.db.Model(&model.TeamMemory{}).Where("id = ?", id).Update("embedding", embedding).Error
}

// TouchLastHit 批量更新记忆的最近命中时间（AR-09 检索回写）
func (r *TeamMemoryRepo) TouchLastHit(ids []string, ts int64) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.Model(&model.TeamMemory{}).Where("id IN ?", ids).Update("last_hit_at", ts).Error
}

// MarkSuperseded 将给定记忆标记为 superseded（AR-09 合并软删）
func (r *TeamMemoryRepo) MarkSuperseded(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.Model(&model.TeamMemory{}).Where("id IN ?", ids).Update("status", "superseded").Error
}

// ArchiveStale 归档组内长期未命中的 active 记忆（AR-09 Forget）。
// 条件：created_at 与 last_hit_at 均早于 cutoff（last_hit_at=0 视为从未命中，一并归档）。
func (r *TeamMemoryRepo) ArchiveStale(teamID string, cutoff int64) error {
	return r.db.Model(&model.TeamMemory{}).
		Where("team_id = ? AND status = ? AND created_at < ? AND (last_hit_at = 0 OR last_hit_at < ?)",
			teamID, "active", cutoff, cutoff).
		Update("status", "archived").Error
}
