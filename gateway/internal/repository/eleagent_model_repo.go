package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// EleAgentModelRepo Ele Agent 模型配置数据访问
type EleAgentModelRepo struct {
	db *gorm.DB
}

// NewEleAgentModelRepo 创建仓库
func NewEleAgentModelRepo(db *gorm.DB) *EleAgentModelRepo {
	return &EleAgentModelRepo{db: db}
}

// Create 创建配置
func (r *EleAgentModelRepo) Create(config *model.EleAgentModelConfig) error {
	return r.db.Create(config).Error
}

// GetByID 根据 ID 获取
func (r *EleAgentModelRepo) GetByID(id string) (*model.EleAgentModelConfig, error) {
	var config model.EleAgentModelConfig
	if err := r.db.First(&config, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

// Update 更新（非零值字段）
func (r *EleAgentModelRepo) Update(config *model.EleAgentModelConfig) error {
	return r.db.Model(&model.EleAgentModelConfig{}).Where("id = ?", config.ID).Updates(config).Error
}

// UpdateFields 使用 map 更新指定字段
func (r *EleAgentModelRepo) UpdateFields(id string, fields map[string]interface{}) error {
	return r.db.Model(&model.EleAgentModelConfig{}).Where("id = ?", id).Updates(fields).Error
}

// Delete 删除
func (r *EleAgentModelRepo) Delete(id string) error {
	return r.db.Delete(&model.EleAgentModelConfig{}, "id = ?", id).Error
}

// List 列表查询（支持 provider 过滤、分页）
func (r *EleAgentModelRepo) List(provider string, page, pageSize int) ([]*model.EleAgentModelConfig, int64, error) {
	var items []*model.EleAgentModelConfig
	var total int64

	query := r.db.Model(&model.EleAgentModelConfig{})
	if provider != "" {
		query = query.Where("provider = ?", provider)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("priority ASC, created_at ASC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// ListEnabled 获取所有启用的配置
func (r *EleAgentModelRepo) ListEnabled() ([]*model.EleAgentModelConfig, error) {
	var items []*model.EleAgentModelConfig
	err := r.db.Where("is_enabled = ?", true).
		Order("priority ASC, created_at ASC").
		Find(&items).Error
	return items, err
}

// GetEnabledByModel 根据平台与模型名获取启用配置
func (r *EleAgentModelRepo) GetEnabledByModel(provider, modelName string) (*model.EleAgentModelConfig, error) {
	var config model.EleAgentModelConfig
	if err := r.db.Where("provider = ? AND model_name = ? AND is_enabled = ?", provider, modelName, true).
		Order("priority ASC, created_at ASC").
		First(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}
