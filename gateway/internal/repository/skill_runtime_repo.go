package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// SkillRuntimeRepo 秘技运行时数据访问
type SkillRuntimeRepo struct {
	db *gorm.DB
}

// NewSkillRuntimeRepo 创建运行时仓库
func NewSkillRuntimeRepo(db *gorm.DB) *SkillRuntimeRepo {
	return &SkillRuntimeRepo{db: db}
}

// CreateOrUpdate 创建或更新运行时记录
func (r *SkillRuntimeRepo) CreateOrUpdate(rt *model.SkillRuntime) error {
	return r.db.Save(rt).Error
}

// GetByID 根据 ID 查询运行时
func (r *SkillRuntimeRepo) GetByID(id string) (*model.SkillRuntime, error) {
	var rt model.SkillRuntime
	if err := r.db.First(&rt, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &rt, nil
}

// GetByDriverID 根据驱动别名查询运行时
func (r *SkillRuntimeRepo) GetByDriverID(driverID string) (*model.SkillRuntime, error) {
	var rt model.SkillRuntime
	if err := r.db.First(&rt, "driver_id = ?", driverID).Error; err != nil {
		return nil, err
	}
	return &rt, nil
}

// List 查询所有运行时
func (r *SkillRuntimeRepo) List() ([]*model.SkillRuntime, error) {
	var items []*model.SkillRuntime
	err := r.db.Order("created_at DESC").Find(&items).Error
	return items, err
}

// ListOnline 查询所有在线（且未禁用）的运行时
func (r *SkillRuntimeRepo) ListOnline() ([]*model.SkillRuntime, error) {
	var items []*model.SkillRuntime
	err := r.db.Where("status = ?", model.SkillRuntimeStatusOnline).Find(&items).Error
	return items, err
}

// ListByDeployment 按 deployment 类型查询
func (r *SkillRuntimeRepo) ListByDeployment(deployment model.SkillRuntimeDeployment) ([]*model.SkillRuntime, error) {
	var items []*model.SkillRuntime
	err := r.db.Where("deployment = ?", deployment).Find(&items).Error
	return items, err
}

// ListBySource 按 source 类型查询
func (r *SkillRuntimeRepo) ListBySource(source model.SkillRuntimeSource) ([]*model.SkillRuntime, error) {
	var items []*model.SkillRuntime
	err := r.db.Where("source = ?", source).Find(&items).Error
	return items, err
}

// UpdateStatus 更新运行时状态、版本、能力、心跳
func (r *SkillRuntimeRepo) UpdateStatus(rt *model.SkillRuntime) error {
	return r.db.Model(&model.SkillRuntime{}).
		Where("id = ?", rt.ID).
		Updates(map[string]interface{}{
			"status":         rt.Status,
			"version":        rt.Version,
			"capabilities":   rt.Capabilities,
			"last_heartbeat": rt.LastHeartbeat,
			"updated_at":     rt.UpdatedAt,
		}).Error
}

// Delete 删除运行时
func (r *SkillRuntimeRepo) Delete(id string) error {
	return r.db.Delete(&model.SkillRuntime{}, "id = ?", id).Error
}
