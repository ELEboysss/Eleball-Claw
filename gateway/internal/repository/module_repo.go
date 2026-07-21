package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// ModuleRepo 集市模块数据访问
type ModuleRepo struct {
	db *gorm.DB
}

// NewModuleRepo 创建模块仓库
func NewModuleRepo(db *gorm.DB) *ModuleRepo {
	return &ModuleRepo{db: db}
}

// CreateOrUpdate 创建或更新模块记录
func (r *ModuleRepo) CreateOrUpdate(m *model.ModuleRecord) error {
	return r.db.Save(m).Error
}

// GetByID 根据 ID 查询模块
func (r *ModuleRepo) GetByID(id string) (*model.ModuleRecord, error) {
	var m model.ModuleRecord
	if err := r.db.First(&m, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// List 查询所有模块
func (r *ModuleRepo) List() ([]*model.ModuleRecord, error) {
	var items []*model.ModuleRecord
	err := r.db.Order("created_at DESC").Find(&items).Error
	return items, err
}

// ListOnline 查询所有在线（且未禁用）的模块
func (r *ModuleRepo) ListOnline() ([]*model.ModuleRecord, error) {
	var items []*model.ModuleRecord
	err := r.db.Where("status = ?", model.ModuleStatusOnline).Find(&items).Error
	return items, err
}

// UpdateStatus 更新模块状态、版本、能力、心跳
func (r *ModuleRepo) UpdateStatus(m *model.ModuleRecord) error {
	return r.db.Model(&model.ModuleRecord{}).
		Where("id = ?", m.ID).
		Updates(map[string]interface{}{
			"status":          m.Status,
			"version":         m.Version,
			"capabilities":    m.Capabilities,
			"last_heartbeat":  m.LastHeartbeat,
			"updated_at":      m.UpdatedAt,
		}).Error
}

// Delete 删除模块
func (r *ModuleRepo) Delete(id string) error {
	return r.db.Delete(&model.ModuleRecord{}, "id = ?", id).Error
}

// DriverRepo 动态驱动数据访问
type DriverRepo struct {
	db *gorm.DB
}

// NewDriverRepo 创建驱动仓库
func NewDriverRepo(db *gorm.DB) *DriverRepo {
	return &DriverRepo{db: db}
}

// CreateOrUpdate 创建或更新驱动记录
func (r *DriverRepo) CreateOrUpdate(d *model.DriverRecord) error {
	return r.db.Save(d).Error
}

// GetByID 根据 ID 查询驱动
func (r *DriverRepo) GetByID(id string) (*model.DriverRecord, error) {
	var d model.DriverRecord
	if err := r.db.First(&d, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// GetByAuthToken 根据 auth_token 查询驱动
func (r *DriverRepo) GetByAuthToken(token string) (*model.DriverRecord, error) {
	if token == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var d model.DriverRecord
	if err := r.db.Where("auth_token = ?", token).First(&d).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// UpdateModuleID 更新驱动绑定的模块 ID
func (r *DriverRepo) UpdateModuleID(id, moduleID string) error {
	return r.db.Model(&model.DriverRecord{}).Where("id = ?", id).Update("module_id", moduleID).Error
}

// List 查询所有驱动
func (r *DriverRepo) List() ([]*model.DriverRecord, error) {
	var items []*model.DriverRecord
	err := r.db.Order("created_at DESC").Find(&items).Error
	return items, err
}

// Delete 删除驱动
func (r *DriverRepo) Delete(id string) error {
	return r.db.Delete(&model.DriverRecord{}, "id = ?", id).Error
}
