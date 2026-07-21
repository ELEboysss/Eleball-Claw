package repository

import (
	"time"

	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// CDKRepo 兑换码数据访问
type CDKRepo struct {
	db *gorm.DB
}

// NewCDKRepo 创建兑换码仓库
func NewCDKRepo(db *gorm.DB) *CDKRepo {
	return &CDKRepo{db: db}
}

// CreateBatch 批量创建兑换码
func (r *CDKRepo) CreateBatch(items []*model.CDK) error {
	return r.db.CreateInBatches(items, 100).Error
}

// CDKListFilters 兑换码列表筛选参数
type CDKListFilters struct {
	Status   string // all / used / unused
	Value    int64  // -1 表示不筛选
	Search   string // 兑换码模糊搜索
	BatchID  string
	Page     int
	PageSize int
}

// CDKListResponse 兑换码列表响应
type CDKListResponse struct {
	Total int64         `json:"total"`
	Items []*model.CDK  `json:"items"`
}

// List 分页查询兑换码列表
func (r *CDKRepo) List(filters CDKListFilters) (*CDKListResponse, error) {
	if filters.Page <= 0 {
		filters.Page = 1
	}
	if filters.PageSize <= 0 || filters.PageSize > 100 {
		filters.PageSize = 20
	}

	query := r.db.Model(&model.CDK{})
	if filters.Status == "used" {
		query = query.Where("used = ?", true)
	} else if filters.Status == "unused" {
		query = query.Where("used = ?", false)
	}
	if filters.Value >= 0 {
		query = query.Where("value = ?", filters.Value)
	}
	if filters.BatchID != "" {
		query = query.Where("batch_id = ?", filters.BatchID)
	}
	if filters.Search != "" {
		query = query.Where("code LIKE ?", "%"+filters.Search+"%")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	offset := (filters.Page - 1) * filters.PageSize
	var items []*model.CDK
	if err := query.Order("created_at DESC").Offset(offset).Limit(filters.PageSize).Find(&items).Error; err != nil {
		return nil, err
	}
	return &CDKListResponse{Total: total, Items: items}, nil
}

// GetByCode 根据标准化后的兑换码查询
func (r *CDKRepo) GetByCode(code string) (*model.CDK, error) {
	var item model.CDK
	if err := r.db.Where("code = ?", code).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// CountByCode 统计兑换码数量（生成时去重用）
func (r *CDKRepo) CountByCode(code string) (int64, error) {
	var count int64
	err := r.db.Model(&model.CDK{}).Where("code = ?", code).Count(&count).Error
	return count, err
}

// MarkUsed 在事务内将兑换码标记为已使用
func (r *CDKRepo) MarkUsed(id, userID string) error {
	now := time.Now()
	return r.db.Model(&model.CDK{}).Where("id = ? AND used = ?", id, false).Updates(map[string]interface{}{
		"used":     true,
		"used_by":  userID,
		"used_at":  now,
	}).Error
}

// RollbackUsed 兑换失败时将兑换码回滚为未使用
func (r *CDKRepo) RollbackUsed(id string) error {
	return r.db.Model(&model.CDK{}).Where("id = ?", id).Updates(map[string]interface{}{
		"used":    false,
		"used_by": nil,
		"used_at": nil,
	}).Error
}

// Delete 删除兑换码
func (r *CDKRepo) Delete(id string) error {
	return r.db.Delete(&model.CDK{}, "id = ?", id).Error
}
