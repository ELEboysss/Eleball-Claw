package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// RechargePackageRepo 充值套餐数据访问
type RechargePackageRepo struct {
	db *gorm.DB
}

// NewRechargePackageRepo 创建仓库
func NewRechargePackageRepo(db *gorm.DB) *RechargePackageRepo {
	return &RechargePackageRepo{db: db}
}

// ListEnabled 返回所有上架套餐，按 sort_order 升序、updated_at 降序排列
func (r *RechargePackageRepo) ListEnabled() ([]*model.RechargePackage, error) {
	var items []*model.RechargePackage
	if err := r.db.Where("is_enabled = ?", true).
		Order("sort_order ASC, updated_at DESC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListAll 返回全部套餐（含下架），管理后台使用
func (r *RechargePackageRepo) ListAll() ([]*model.RechargePackage, error) {
	var items []*model.RechargePackage
	if err := r.db.Order("sort_order ASC, updated_at DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// GetByID 根据 ID 查询套餐
func (r *RechargePackageRepo) GetByID(id string) (*model.RechargePackage, error) {
	var item model.RechargePackage
	if err := r.db.First(&item, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// Create 创建套餐
func (r *RechargePackageRepo) Create(item *model.RechargePackage) error {
	return r.db.Create(item).Error
}

// Update 更新套餐（非零字段）
func (r *RechargePackageRepo) Update(item *model.RechargePackage) error {
	return r.db.Model(item).Updates(item).Error
}

// Delete 删除套餐
func (r *RechargePackageRepo) Delete(id string) error {
	return r.db.Delete(&model.RechargePackage{}, "id = ?", id).Error
}

// Count 返回套餐总数
func (r *RechargePackageRepo) Count() (int64, error) {
	var count int64
	if err := r.db.Model(&model.RechargePackage{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
