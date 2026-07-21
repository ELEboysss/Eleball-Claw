package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
	"time"
)

// OrderRepo 订单数据访问
type OrderRepo struct {
	db *gorm.DB
}

// NewOrderRepo 创建仓库
func NewOrderRepo(db *gorm.DB) *OrderRepo {
	return &OrderRepo{db: db}
}

// Create 创建订单
func (r *OrderRepo) Create(order *model.Order) error {
	return r.db.Create(order).Error
}

// GetByID 根据 ID 查询
func (r *OrderRepo) GetByID(id string) (*model.Order, error) {
	var order model.Order
	if err := r.db.First(&order, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

// List 查询订单列表
func (r *OrderRepo) List(page, pageSize int, status string) ([]*model.Order, int64, error) {
	var items []*model.Order
	var total int64

	query := r.db.Model(&model.Order{})
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

// UpdateStatus 更新订单状态
func (r *OrderRepo) UpdateStatus(id, status string) error {
	return r.db.Model(&model.Order{}).Where("id = ?", id).Update("status", status).Error
}

// UpdateStatusIf 条件更新订单状态：仅当当前状态为 from 时才流转为 to。
// 返回是否实际生效，用于防并发重复流转（如重复退款）。
func (r *OrderRepo) UpdateStatusIf(id, from, to string) (bool, error) {
	res := r.db.Model(&model.Order{}).Where("id = ? AND status = ?", id, from).Update("status", to)
	return res.RowsAffected > 0, res.Error
}

// ClaimPaid 将订单从 pending「认领」为 paid（条件更新）。
// 仅首次调用返回 true：支付宝重复通知、管理员确认与回调并发等场景下保证权益只发放一次。
// tradeNo 为空时不更新 trade_no 列（管理员确认收款等无渠道交易号场景）。
func (r *OrderRepo) ClaimPaid(id, tradeNo string, paidAt time.Time) (bool, error) {
	updates := map[string]interface{}{
		"status":  "paid",
		"paid_at": paidAt,
	}
	if tradeNo != "" {
		updates["trade_no"] = tradeNo
	}
	res := r.db.Model(&model.Order{}).Where("id = ? AND status = ?", id, "pending").Updates(updates)
	return res.RowsAffected > 0, res.Error
}

// UpdateChannel 更新订单支付渠道（VIP 订单调起支付宝收银台时回写）。
func (r *OrderRepo) UpdateChannel(id, channel string) error {
	return r.db.Model(&model.Order{}).Where("id = ?", id).Update("channel", channel).Error
}

// ListPendingBefore 查询创建时间早于 before 的 pending 订单（过期订单扫描用），按创建时间升序。
func (r *OrderRepo) ListPendingBefore(before time.Time, limit int) ([]*model.Order, error) {
	var items []*model.Order
	if limit <= 0 {
		limit = 100
	}
	err := r.db.Where("status = ? AND created_at < ?", "pending", before).
		Order("created_at ASC").Limit(limit).Find(&items).Error
	return items, err
}
