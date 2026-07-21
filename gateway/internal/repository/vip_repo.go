package repository

import (
	"time"

	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// VIPRepo 会员数据访问
type VIPRepo struct {
	db *gorm.DB
}

// NewVIPRepo 创建会员仓库
func NewVIPRepo(db *gorm.DB) *VIPRepo {
	return &VIPRepo{db: db}
}

// ====== VIPPlan ======

// CreatePlan 创建会员套餐
func (r *VIPRepo) CreatePlan(plan *model.VIPPlan) error {
	return r.db.Create(plan).Error
}

// GetPlanByID 根据 ID 查询套餐
func (r *VIPRepo) GetPlanByID(id string) (*model.VIPPlan, error) {
	var plan model.VIPPlan
	if err := r.db.First(&plan, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &plan, nil
}

// GetPlanByLevel 根据等级查询上架套餐
func (r *VIPRepo) GetPlanByLevel(level int) (*model.VIPPlan, error) {
	var plan model.VIPPlan
	if err := r.db.Where("level = ? AND is_enabled = ?", level, true).First(&plan).Error; err != nil {
		return nil, err
	}
	return &plan, nil
}

// ListEnabledPlans 查询上架套餐列表
func (r *VIPRepo) ListEnabledPlans() ([]*model.VIPPlan, error) {
	var items []*model.VIPPlan
	if err := r.db.Where("is_enabled = ?", true).Order("sort_order ASC, level ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListAllPlans 查询全部套餐（管理后台）
func (r *VIPRepo) ListAllPlans() ([]*model.VIPPlan, error) {
	var items []*model.VIPPlan
	if err := r.db.Order("sort_order ASC, level ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// UpdatePlan 更新套餐
func (r *VIPRepo) UpdatePlan(plan *model.VIPPlan) error {
	return r.db.Save(plan).Error
}

// DeletePlan 删除套餐
func (r *VIPRepo) DeletePlan(id string) error {
	return r.db.Delete(&model.VIPPlan{}, "id = ?", id).Error
}

// CountPlans 统计套餐数量
func (r *VIPRepo) CountPlans() (int64, error) {
	var count int64
	err := r.db.Model(&model.VIPPlan{}).Count(&count).Error
	return count, err
}

// ====== VIPSubscription ======

// CreateSubscription 创建订阅记录
func (r *VIPRepo) CreateSubscription(sub *model.VIPSubscription) error {
	return r.db.Create(sub).Error
}

// GetSubscriptionByID 根据 ID 查询订阅
func (r *VIPRepo) GetSubscriptionByID(id string) (*model.VIPSubscription, error) {
	var sub model.VIPSubscription
	if err := r.db.First(&sub, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &sub, nil
}

// GetActiveSubscriptionByUserID 查询用户当前生效中的订阅
func (r *VIPRepo) GetActiveSubscriptionByUserID(userID string) (*model.VIPSubscription, error) {
	var sub model.VIPSubscription
	if err := r.db.Where("user_id = ? AND status = ? AND expires_at > ?", userID, "active", time.Now()).
		Order("expires_at DESC").First(&sub).Error; err != nil {
		return nil, err
	}
	return &sub, nil
}

// UpdateSubscription 更新订阅
func (r *VIPRepo) UpdateSubscription(sub *model.VIPSubscription) error {
	return r.db.Save(sub).Error
}

// ListSubscriptions 分页查询订阅记录
func (r *VIPRepo) ListSubscriptions(page, pageSize int, userID string) ([]*model.VIPSubscription, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	query := r.db.Model(&model.VIPSubscription{})
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var items []*model.VIPSubscription
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
