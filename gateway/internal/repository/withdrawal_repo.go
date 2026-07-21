package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// WithdrawalRepo 提现数据访问
type WithdrawalRepo struct {
	db *gorm.DB
}

// NewWithdrawalRepo 创建仓库
func NewWithdrawalRepo(db *gorm.DB) *WithdrawalRepo {
	return &WithdrawalRepo{db: db}
}

// Create 创建提现记录
func (r *WithdrawalRepo) Create(record *model.WithdrawalRecord) error {
	return r.db.Create(record).Error
}

// GetByID 根据 ID 查询
func (r *WithdrawalRepo) GetByID(id string) (*model.WithdrawalRecord, error) {
	var record model.WithdrawalRecord
	if err := r.db.First(&record, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// ListByUser 查询用户的提现记录
func (r *WithdrawalRepo) ListByUser(userID string, page, pageSize int) ([]*model.WithdrawalRecord, int64, error) {
	var items []*model.WithdrawalRecord
	var total int64

	query := r.db.Model(&model.WithdrawalRecord{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListAll 管理员查询所有提现记录
func (r *WithdrawalRepo) ListAll(page, pageSize int, status string) ([]*model.WithdrawalRecord, int64, error) {
	var items []*model.WithdrawalRecord
	var total int64

	query := r.db.Model(&model.WithdrawalRecord{})
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// UpdateStatus 更新状态
func (r *WithdrawalRepo) UpdateStatus(id string, status model.WithdrawalStatus, adminNote, txID string) error {
	updates := map[string]interface{}{
		"status":     status,
		"admin_note": adminNote,
	}
	if txID != "" {
		updates["tx_id"] = txID
	}
	return r.db.Model(&model.WithdrawalRecord{}).Where("id = ?", id).Updates(updates).Error
}

// SumTodayAmount 统计用户今日提现金额
func (r *WithdrawalRepo) SumTodayAmount(userID string) (int64, error) {
	var result int64
	err := r.db.Model(&model.WithdrawalRecord{}).
		Where("user_id = ? AND DATE(created_at) = DATE('now')", userID).
		Where("status IN ?", []model.WithdrawalStatus{model.WithdrawalStatusPending, model.WithdrawalStatusApproved, model.WithdrawalStatusCompleted}).
		Select("COALESCE(SUM(amount), 0)").
		Scan(&result).Error
	return result, err
}
