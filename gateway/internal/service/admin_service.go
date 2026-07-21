package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AdminService 管理服务
type AdminService struct {
	db              *gorm.DB
	userRepo        *repository.UserRepo
	billRepo        *repository.BillingRepo
	orderRepo       *repository.OrderRepo
	activityService *ActivityService
	vipService      *VIPService
}

// NewAdminService 创建服务
func NewAdminService(db *gorm.DB, userRepo *repository.UserRepo, billRepo *repository.BillingRepo, orderRepo *repository.OrderRepo, activityService *ActivityService, vipService *VIPService) *AdminService {
	return &AdminService{
		db:              db,
		userRepo:        userRepo,
		billRepo:        billRepo,
		orderRepo:       orderRepo,
		activityService: activityService,
		vipService:      vipService,
	}
}

// ====== Dashboard 统计 ======

// DashboardStats 仪表盘统计
type DashboardStats struct {
	TotalUsers          int64 `json:"total_users"`
	TodayActive         int64 `json:"today_active"`
	YesterdayActive     int64 `json:"yesterday_active"`
	TodayTokenUsage     int64 `json:"today_token_usage"`
	YesterdayTokenUsage int64 `json:"yesterday_token_usage"`
	TodayRevenue        int64 `json:"today_revenue"`
	YesterdayRevenue    int64 `json:"yesterday_revenue"`
	TotalRevenue        int64 `json:"total_revenue"`
}

// GetDashboardStats 获取仪表盘统计
func (s *AdminService) GetDashboardStats() (*DashboardStats, error) {
	totalUsers, err := s.userRepo.Count()
	if err != nil {
		return nil, err
	}

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	todayActive, _ := s.userRepo.CountActiveToday(today)
	yesterdayActive, _ := s.userRepo.CountActiveToday(yesterday)
	todayTokenUsage, _ := s.billRepo.SumTokenUsageToday(today)
	yesterdayTokenUsage, _ := s.billRepo.SumTokenUsageToday(yesterday)
	todayRevenue, _ := s.billRepo.SumRevenueToday(today)
	yesterdayRevenue, _ := s.billRepo.SumRevenueToday(yesterday)
	totalRevenue, _ := s.billRepo.SumTotalRevenue()

	return &DashboardStats{
		TotalUsers:          totalUsers,
		TodayActive:         todayActive,
		YesterdayActive:     yesterdayActive,
		TodayTokenUsage:     todayTokenUsage,
		YesterdayTokenUsage: yesterdayTokenUsage,
		TodayRevenue:        todayRevenue,
		YesterdayRevenue:    yesterdayRevenue,
		TotalRevenue:        totalRevenue,
	}, nil
}

// DailyStat 每日统计
type DailyStat struct {
	Date  string `json:"date"`
	Value int64  `json:"value"`
}

// TokenUsageStat 每日 Token 使用量统计（按输入/输出拆分）
type TokenUsageStat struct {
	Date   string `json:"date"`
	Input  int64  `json:"input"`
	Output int64  `json:"output"`
}

// GetDailyActive 获取日活跃用户趋势
func (s *AdminService) GetDailyActive(days int) ([]DailyStat, error) {
	raw, err := s.userRepo.DailyActiveStats(days)
	if err != nil {
		return nil, err
	}
	result := make([]DailyStat, len(raw))
	for i, r := range raw {
		result[i] = DailyStat{Date: r.Date, Value: r.Value}
	}
	return result, nil
}

// GetTokenUsageTrend 获取 Token 使用趋势
func (s *AdminService) GetTokenUsageTrend(days int) ([]TokenUsageStat, error) {
	raw, err := s.billRepo.DailyTokenUsageStats(days)
	if err != nil {
		return nil, err
	}
	result := make([]TokenUsageStat, len(raw))
	for i, r := range raw {
		result[i] = TokenUsageStat{Date: r.Date, Input: r.Input, Output: r.Output}
	}
	return result, nil
}

// ListRecentActivities 查询最近动态
func (s *AdminService) ListRecentActivities(limit int) ([]*model.ActivityEvent, error) {
	if s.activityService != nil {
		return s.activityService.ListRecent(limit)
	}
	return []*model.ActivityEvent{}, nil
}

// ====== 用户管理 ======

// UserListRequest 用户列表请求
type UserListRequest struct {
	Page     int    `form:"page" binding:"min=1"`
	PageSize int    `form:"page_size" binding:"min=1,max=100"`
	Search   string `form:"search"`
	Status   *int   `form:"status"`
}

// UserListResponse 用户列表响应
type UserListResponse struct {
	Total int64         `json:"total"`
	Items []*model.User `json:"items"`
}

// ListUsers 获取用户列表
func (s *AdminService) ListUsers(req UserListRequest) (*UserListResponse, error) {
	if req.Page == 0 {
		req.Page = 1
	}
	if req.PageSize == 0 {
		req.PageSize = 20
	}

	users, total, err := s.userRepo.List(req.Page, req.PageSize, req.Search, req.Status)
	if err != nil {
		return nil, err
	}

	return &UserListResponse{Total: total, Items: users}, nil
}

// GetUserDetail 获取用户详情
func (s *AdminService) GetUserDetail(userID string) (*model.User, error) {
	return s.userRepo.GetByID(userID)
}

// UpdateUserStatus 更新用户状态
func (s *AdminService) UpdateUserStatus(userID string, status int) error {
	return s.userRepo.UpdateStatus(userID, status)
}

// DeleteUser 删除用户
func (s *AdminService) DeleteUser(userID string) error {
	return s.userRepo.Delete(userID)
}

// ====== 计费管理 ======

// TransactionListRequest 交易记录列表请求
type TransactionListRequest struct {
	Page     int    `form:"page" binding:"min=1"`
	PageSize int    `form:"page_size" binding:"min=1,max=100"`
	Type     string `form:"type"`
}

// TransactionListResponse 交易记录列表响应
type TransactionListResponse struct {
	Total int64                       `json:"total"`
	Items []*model.BalanceTransaction `json:"items"`
}

// ListTransactions 获取交易记录
func (s *AdminService) ListTransactions(req TransactionListRequest) (*TransactionListResponse, error) {
	if req.Page == 0 {
		req.Page = 1
	}
	if req.PageSize == 0 {
		req.PageSize = 20
	}

	items, total, err := s.billRepo.ListTransactions(req.Page, req.PageSize, req.Type)
	if err != nil {
		return nil, err
	}
	return &TransactionListResponse{Total: total, Items: items}, nil
}

// RechargeRequest 充值请求
type RechargeRequest struct {
	UserID   string `json:"user_id" binding:"required"`
	Amount   int64  `json:"amount" binding:"required,min=1"` // 分
	Currency string `json:"currency"`                        // danwan / elegant，默认 danwan
}

// Recharge 给用户充值
func (s *AdminService) Recharge(req RechargeRequest) error {
	if req.Amount <= 0 {
		return errors.New("充值金额必须大于 0")
	}
	if req.Currency == "" {
		req.Currency = CurrencyDanwan
	}

	// 根据货币类型更新对应余额，并同步累计充值金额（仅弹丸充值计入人民币累计）
	var balanceAfter int64
	user, err := s.userRepo.GetByID(req.UserID)
	if err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}

	switch req.Currency {
	case CurrencyElegant:
		if err := s.userRepo.UpdateElegantBalance(req.UserID, req.Amount); err != nil {
			return fmt.Errorf("更新优雅弹丸余额失败: %w", err)
		}
		balanceAfter = user.ElegantBalance + req.Amount
	case CurrencyDanwan:
		if err := s.userRepo.UpdateBalance(req.UserID, req.Amount); err != nil {
			return fmt.Errorf("更新弹丸余额失败: %w", err)
		}
		balanceAfter = user.Balance + req.Amount
		// 弹丸与人民币按 1:1 分计价，累计充值金额同步增加
		if err := s.userRepo.UpdateTotalRecharged(req.UserID, req.Amount); err != nil {
			return fmt.Errorf("更新累计充值金额失败: %w", err)
		}
	default:
		return errors.New("不支持的货币类型")
	}

	// 记录充值动态
	if s.activityService != nil {
		s.activityService.RecordUserRecharged(req.UserID, req.Amount, req.Currency)
	}

	// 记录交易流水
	tx := &model.BalanceTransaction{
		ID:           uuid.New().String(),
		UserID:       req.UserID,
		Type:         "recharge",
		Amount:       req.Amount,
		Currency:     req.Currency,
		BalanceAfter: balanceAfter,
		Description:  "管理员手动充值",
	}
	if err := s.billRepo.CreateTransaction(tx); err != nil {
		return fmt.Errorf("记录交易失败: %w", err)
	}

	return nil
}

// ====== 订单管理 ======

// OrderListRequest 订单列表请求
type OrderListRequest struct {
	Page     int    `form:"page" binding:"min=1"`
	PageSize int    `form:"page_size" binding:"min=1,max=100"`
	Status   string `form:"status"`
}

// OrderListResponse 订单列表响应
type OrderListResponse struct {
	Total int64          `json:"total"`
	Items []*model.Order `json:"items"`
}

// ListOrders 获取订单列表
func (s *AdminService) ListOrders(req OrderListRequest) (*OrderListResponse, error) {
	if req.Page == 0 {
		req.Page = 1
	}
	if req.PageSize == 0 {
		req.PageSize = 20
	}

	items, total, err := s.orderRepo.List(req.Page, req.PageSize, req.Status)
	if err != nil {
		return nil, err
	}
	return &OrderListResponse{Total: total, Items: items}, nil
}

// ====== ASR 额度管理 ======

// GetUserAsrQuota 获取用户 ASR 额度
func (s *AdminService) GetUserAsrQuota(userID string) (*repository.AsrQuota, error) {
	return s.userRepo.GetAsrQuota(userID)
}

// UpdateUserAsrQuotaRequest 设置用户 ASR 额度请求
// 设置后额度立即生效；若 used >= monthly 则后续调用会被拒绝。
type UpdateUserAsrQuotaRequest struct {
	Monthly int64 `json:"monthly" binding:"min=0"` // 每月额度，0 表示使用系统默认
	Used    int64 `json:"used" binding:"min=0"`    // 已用次数，可用于手动清零或调整
}

// UpdateUserAsrQuota 设置用户 ASR 额度
func (s *AdminService) UpdateUserAsrQuota(userID string, req UpdateUserAsrQuotaRequest) error {
	resetAt := time.Now()
	return s.userRepo.UpdateAsrQuota(userID, req.Monthly, req.Used, resetAt)
}

// RefundOrder 订单退款
func (s *AdminService) RefundOrder(orderID string) error {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return err
	}

	// 条件更新认领退款流转：paid→refunded 仅首次生效，防并发重复退款
	claimed, err := s.orderRepo.UpdateStatusIf(orderID, "paid", "refunded")
	if err != nil {
		return err
	}
	if !claimed {
		return errors.New("订单状态不允许退款")
	}

	// 根据订单货币退还对应余额
	var balanceAfter int64
	user, err := s.userRepo.GetByID(order.UserID)
	if err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}

	switch order.Currency {
	case CurrencyElegant:
		if err := s.userRepo.UpdateElegantBalance(order.UserID, order.Amount); err != nil {
			return err
		}
		balanceAfter = user.ElegantBalance + order.Amount
	default:
		if err := s.userRepo.UpdateBalance(order.UserID, order.Amount); err != nil {
			return err
		}
		balanceAfter = user.Balance + order.Amount
	}

	// 记录退款流水
	tx := &model.BalanceTransaction{
		ID:           uuid.New().String(),
		UserID:       order.UserID,
		Type:         "refund",
		Amount:       order.Amount,
		Currency:     order.Currency,
		BalanceAfter: balanceAfter,
		Description:  fmt.Sprintf("订单退款: %s", orderID),
	}
	return s.billRepo.CreateTransaction(tx)
}

// ConfirmOrder 管理员确认订单已收款（用于测试/补单）
func (s *AdminService) ConfirmOrder(orderID string) error {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return errors.New("订单不存在")
	}
	if order.Status != "pending" {
		return errors.New("订单状态非待支付")
	}

	switch order.ProductType {
	case "vip":
		if s.vipService == nil {
			return errors.New("VIP 服务未初始化")
		}
		return s.vipService.ActivateSubscription(orderID, "")
	default:
		return s.confirmRechargeOrder(order)
	}
}

// confirmRechargeOrder 充值订单确认收款：事务内先认领（pending→paid）再发放弹丸，
// 与支付宝 notify 并发或重复点击时权益只发放一次。
func (s *AdminService) confirmRechargeOrder(order *model.Order) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		orderTx := repository.NewOrderRepo(tx)
		claimed, err := orderTx.ClaimPaid(order.ID, "", time.Now())
		if err != nil {
			return err
		}
		if !claimed {
			return nil // 已被并发流程处理，幂等返回
		}
		userTx := repository.NewUserRepo(tx)
		billTx := repository.NewBillingRepo(tx)
		if order.Danwan > 0 {
			user, err := userTx.GetByID(order.UserID)
			if err != nil {
				return err
			}
			if err := userTx.UpdateBalance(order.UserID, order.Danwan); err != nil {
				return err
			}
			if err := userTx.UpdateTotalRecharged(order.UserID, order.Danwan); err != nil {
				return err
			}
			txRecord := &model.BalanceTransaction{
				ID:           uuid.New().String(),
				UserID:       order.UserID,
				Type:         "recharge",
				Amount:       order.Danwan,
				Currency:     CurrencyDanwan,
				BalanceAfter: user.Balance + order.Danwan,
				Description:  "管理员确认收款充值",
			}
			if err := billTx.CreateTransaction(txRecord); err != nil {
				return err
			}
		}
		return nil
	})
}
