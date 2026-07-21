package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// BillingRepo 计费数据访问
type BillingRepo struct {
	db *gorm.DB
}

// NewBillingRepo 创建仓库
func NewBillingRepo(db *gorm.DB) *BillingRepo {
	return &BillingRepo{db: db}
}

// CreateTransaction 创建交易记录
func (r *BillingRepo) CreateTransaction(tx *model.BalanceTransaction) error {
	return r.db.Create(tx).Error
}

// CreateTokenUsage 创建 Token 用量记录
func (r *BillingRepo) CreateTokenUsage(usage *model.TokenUsage) error {
	return r.db.Create(usage).Error
}

// ListTransactions 查询交易记录列表
func (r *BillingRepo) ListTransactions(page, pageSize int, txType string) ([]*model.BalanceTransaction, int64, error) {
	var items []*model.BalanceTransaction
	var total int64

	query := r.db.Model(&model.BalanceTransaction{})
	if txType != "" {
		query = query.Where("type = ?", txType)
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

// ListTransactionsByUser 查询指定用户的交易记录
func (r *BillingRepo) ListTransactionsByUser(userID string, page, pageSize int, txType string) ([]*model.BalanceTransaction, int64, error) {
	var items []*model.BalanceTransaction
	var total int64

	query := r.db.Model(&model.BalanceTransaction{}).Where("user_id = ?", userID)
	if txType != "" {
		query = query.Where("type = ?", txType)
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

// SumTokenUsageToday 今日 Token 使用量
func (r *BillingRepo) SumTokenUsageToday(date string) (int64, error) {
	var result int64
	err := r.db.Model(&model.TokenUsage{}).
		Where("DATE(created_at) = ?", date).
		Select("COALESCE(SUM(input_tokens + output_tokens), 0)").
		Scan(&result).Error
	return result, err
}

// SumRevenueToday 今日收入
func (r *BillingRepo) SumRevenueToday(date string) (int64, error) {
	var result int64
	err := r.db.Model(&model.BalanceTransaction{}).
		Where("DATE(created_at) = ? AND type = ?", date, "recharge").
		Select("COALESCE(SUM(amount), 0)").
		Scan(&result).Error
	return result, err
}

// SumTotalRevenue 总收入
func (r *BillingRepo) SumTotalRevenue() (int64, error) {
	var result int64
	err := r.db.Model(&model.BalanceTransaction{}).
		Where("type = ?", "recharge").
		Select("COALESCE(SUM(amount), 0)").
		Scan(&result).Error
	return result, err
}

// DailyTokenUsageStats Token 使用趋势（按输入/输出拆分）
func (r *BillingRepo) DailyTokenUsageStats(days int) ([]struct {
	Date   string `json:"date"`
	Input  int64  `json:"input"`
	Output int64  `json:"output"`
}, error) {
	var results []struct {
		Date   string `json:"date"`
		Input  int64  `json:"input"`
		Output int64  `json:"output"`
	}

	err := r.db.Raw(`
		SELECT DATE(created_at) as date,
		       COALESCE(SUM(input_tokens), 0) as input,
		       COALESCE(SUM(output_tokens), 0) as output
		FROM token_usages
		WHERE created_at >= DATE('now', '-' || ? || ' days')
		GROUP BY DATE(created_at)
		ORDER BY date
	`, days).Scan(&results).Error

	return results, err
}
