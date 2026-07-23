package service

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// VIP0 用户可试用 Agent 模式的最大次数
const AgentTrialQuotaVIP0 = 3

// VIPService 会员系统服务
type VIPService struct {
	db        *gorm.DB
	vipRepo   *repository.VIPRepo
	userRepo  *repository.UserRepo
	billRepo  *repository.BillingRepo
	orderRepo *repository.OrderRepo
	logger    *zap.Logger
	// unrestricted=true（claw 本地）：跳过所有本地 user 依赖的读门控，返回不限值。
	// claw 本地不限对话/Agent Session/ASR 配额/功能开关（云端 VIP 仅用于云端秘技下载/激活门控，
	// 由 CloudAccountService 直接校验，不经 VIPService）。置 true 后不查本地 users 表。
	unrestricted bool
}

// SetUnrestricted 设置为不限模式（claw 本地用；云端 cmd/server 保持 false）。
func (s *VIPService) SetUnrestricted(b bool) {
	s.unrestricted = b
}

// NewVIPService 创建会员服务
func NewVIPService(db *gorm.DB, vipRepo *repository.VIPRepo, userRepo *repository.UserRepo, billRepo *repository.BillingRepo, orderRepo *repository.OrderRepo, logger *zap.Logger) *VIPService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &VIPService{
		db:        db,
		vipRepo:   vipRepo,
		userRepo:  userRepo,
		billRepo:  billRepo,
		orderRepo: orderRepo,
		logger:    logger,
	}
}

// VIPStatus 用户当前生效的 VIP 状态（实时计算）
type VIPStatus struct {
	Level               int             `json:"level"`
	IsVIP               bool            `json:"is_vip"`
	ExpireAt            time.Time       `json:"expire_at"`
	PlanID              string          `json:"plan_id"`
	PlanName            string          `json:"plan_name"`
	DiscountPercent     int             `json:"discount_percent"` // 100 表示无折扣，80 表示 8 折
	MaxConversations    int             `json:"max_conversations"`
	MaxAgentSessions    int             `json:"max_agent_sessions"`
	AsrQuotaMonthly     int64           `json:"asr_quota_monthly"`
	AgentTrialRemaining int             `json:"agent_trial_remaining"` // VIP0 剩余试用次数，-1 表示不适用
	Features            map[string]bool `json:"features"`
}

// SubscribeResult 订阅下单结果
type SubscribeResult struct {
	OrderID         string `json:"order_id"`
	ProductType     string `json:"product_type"`
	Amount          int64  `json:"amount"`           // 仍需现金支付的金额（分）
	ElegantDeducted int64  `json:"elegant_deducted"` // 优雅弹丸抵扣额（分）
	RefundAmount    int64  `json:"refund_amount"`    // 旧套餐按周退还额（分）
	CashAmount      int64  `json:"cash_amount"`      // 同 Amount，便于前端展示
}

// RefundPreview 退费预览
type RefundPreview struct {
	RemainingWeeks int   `json:"remaining_weeks"`
	RefundAmount   int64 `json:"refund_amount"` // 优雅弹丸退还额（分）
}

// GetEffectiveVIP 获取用户当前生效 VIP 状态（已过期则降级为 VIP0）
func (s *VIPService) GetEffectiveVIP(userID string) (*VIPStatus, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}

	// 管理员无限制
	if user.Role == model.UserRoleAdmin {
		return &VIPStatus{
			Level:               math.MaxInt32,
			IsVIP:               true,
			ExpireAt:            time.Now().AddDate(100, 0, 0),
			PlanName:            "管理员",
			DiscountPercent:     100,
			MaxConversations:    math.MaxInt32,
			MaxAgentSessions:    math.MaxInt32,
			AsrQuotaMonthly:     math.MaxInt64,
			AgentTrialRemaining: -1,
			Features: map[string]bool{
				model.VIPFeatureAgentMode: true,
				model.VIPFeatureFileTools: true,
				model.VIPFeatureDiscount:  true,
			},
		}, nil
	}

	// 查找生效中的订阅
	sub, err := s.vipRepo.GetActiveSubscriptionByUserID(userID)
	if err != nil {
		// 无生效订阅时按 VIP0 处理（过期时间沿用用户字段，避免前端展示空时间）
		status := s.buildStatus(0, nil)
		status.ExpireAt = user.VIPExpireAt
		s.applyTrialStatus(status, user)
		return status, nil
	}

	plan, err := s.vipRepo.GetPlanByID(sub.PlanID)
	if err != nil {
		return nil, fmt.Errorf("查询套餐失败: %w", err)
	}

	status := s.buildStatus(sub.Level, plan)
	status.ExpireAt = sub.ExpiresAt
	s.applyTrialStatus(status, user)
	return status, nil
}

// applyTrialStatus 根据用户已试用次数计算 VIP0 剩余试用次数，并在有剩余时开放 agent_mode
// 每日自动重置试用次数
func (s *VIPService) applyTrialStatus(status *VIPStatus, user *model.User) {
	if status.Level > 0 {
		status.AgentTrialRemaining = -1
		return
	}

	s.resetAgentTrialIfNewDay(user)

	remaining := AgentTrialQuotaVIP0 - user.AgentTrialUsed
	if remaining < 0 {
		remaining = 0
	}
	status.AgentTrialRemaining = remaining
	if remaining > 0 {
		status.Features[model.VIPFeatureAgentMode] = true
	}
}

// resetAgentTrialIfNewDay 跨天时重置 VIP0 用户的 Agent 模式试用次数。
// 重置成功时同步内存中的 user 字段；失败时保留当前值并记录日志，不阻断流程。
func (s *VIPService) resetAgentTrialIfNewDay(user *model.User) {
	now := time.Now()
	resetAt := user.AgentTrialResetAt
	if resetAt.IsZero() {
		resetAt = now.Add(-24 * time.Hour) // 从未刷新过（新用户/旧数据），视为需要重置
	}
	if now.YearDay() == resetAt.YearDay() && now.Year() == resetAt.Year() {
		return // 同一天，无需重置
	}
	if err := s.userRepo.ResetAgentTrialUsed(user.ID); err != nil {
		s.logger.Warn("重置 Agent 试用次数失败", zap.String("user_id", user.ID), zap.Error(err))
		return
	}
	user.AgentTrialUsed = 0
	user.AgentTrialResetAt = now
}

// ConsumeAgentTrial 消耗一次 VIP0 的 Agent 模式试用次数
func (s *VIPService) ConsumeAgentTrial(userID string) error {
	if s.unrestricted {
		return nil // claw 本地不限 Agent 模式，无试用计数
	}
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return err
	}
	if user.VIPLevel > 0 {
		return nil
	}
	// 跨天时先重置再校验额度，避免依赖调用方先查询 VIP 状态
	s.resetAgentTrialIfNewDay(user)
	if user.AgentTrialUsed >= AgentTrialQuotaVIP0 {
		return errors.New("试用次数已用完")
	}
	return s.userRepo.IncrementAgentTrialUsed(userID)
}

// buildStatus 根据等级与套餐构造 VIPStatus
func (s *VIPService) buildStatus(level int, plan *model.VIPPlan) *VIPStatus {
	status := &VIPStatus{
		Level:            level,
		IsVIP:            level > 0,
		DiscountPercent:  100,
		MaxConversations: 100,
		MaxAgentSessions: 10,
		AsrQuotaMonthly:  1000,
		Features: map[string]bool{
			model.VIPFeatureAgentMode: false,
			model.VIPFeatureFileTools: false,
			model.VIPFeatureDiscount:  false,
		},
	}
	if plan != nil {
		status.PlanID = plan.ID
		status.PlanName = plan.Name
		status.DiscountPercent = plan.DiscountPercent
		status.MaxConversations = plan.MaxConversations
		status.MaxAgentSessions = plan.MaxAgentSessions
		status.AsrQuotaMonthly = plan.AsrQuotaMonthly
		status.Features[model.VIPFeatureAgentMode] = plan.AgentEnabled
		status.Features[model.VIPFeatureFileTools] = plan.FileToolsEnabled
		status.Features[model.VIPFeatureDiscount] = plan.DiscountPercent < 100
	}
	return status
}

// GetVIPStatus 获取用户当前 VIP 状态（对外展示）
func (s *VIPService) GetVIPStatus(userID string) (*VIPStatus, error) {
	return s.GetEffectiveVIP(userID)
}

// ListPlansForUser 返回上架的 VIP 套餐列表（供用户端展示）
func (s *VIPService) ListPlansForUser() ([]*model.VIPPlan, error) {
	plans, err := s.vipRepo.ListEnabledPlans()
	if err != nil {
		return nil, err
	}
	// 小弹丸（VIP0）是免费默认等级，不在用户端作为可购买套餐展示
	var result []*model.VIPPlan
	for _, p := range plans {
		if p.Level > 0 {
			result = append(result, p)
		}
	}
	return result, nil
}

// ListAllPlans 返回全部套餐（管理后台）
func (s *VIPService) ListAllPlans() ([]*model.VIPPlan, error) {
	return s.vipRepo.ListAllPlans()
}

// GetPlanByID 根据 ID 查询套餐
func (s *VIPService) GetPlanByID(id string) (*model.VIPPlan, error) {
	return s.vipRepo.GetPlanByID(id)
}

// CreatePlan 创建 VIP 套餐
func (s *VIPService) CreatePlan(req *CreateVIPPlanRequest) (*model.VIPPlan, error) {
	if req.Level < 0 {
		return nil, errors.New("VIP 等级不能为负数")
	}
	if req.DurationDays <= 0 {
		req.DurationDays = 30
	}
	plan := &model.VIPPlan{
		ID:               uuid.New().String(),
		Level:            req.Level,
		Name:             req.Name,
		PriceFen:         req.PriceFen,
		DurationDays:     req.DurationDays,
		DiscountPercent:  req.DiscountPercent,
		MaxConversations: req.MaxConversations,
		MaxAgentSessions: req.MaxAgentSessions,
		AsrQuotaMonthly:  req.AsrQuotaMonthly,
		AgentEnabled:     req.AgentEnabled,
		FileToolsEnabled: req.FileToolsEnabled,
		SortOrder:        req.SortOrder,
		IsEnabled:        req.IsEnabled,
		Description:      req.Description,
	}
	if err := s.vipRepo.CreatePlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// CreateVIPPlanRequest 创建套餐请求
type CreateVIPPlanRequest struct {
	Level            int    `json:"level" binding:"required,min=0"`
	Name             string `json:"name" binding:"required"`
	PriceFen         int64  `json:"price_fen" binding:"min=0"`
	DurationDays     int    `json:"duration_days"`
	DiscountPercent  int    `json:"discount_percent" binding:"min=1,max=100"`
	MaxConversations int    `json:"max_conversations" binding:"min=0"`
	MaxAgentSessions int    `json:"max_agent_sessions" binding:"min=0"`
	AsrQuotaMonthly  int64  `json:"asr_quota_monthly" binding:"min=0"`
	AgentEnabled     bool   `json:"agent_enabled"`
	FileToolsEnabled bool   `json:"file_tools_enabled"`
	SortOrder        int    `json:"sort_order"`
	IsEnabled        bool   `json:"is_enabled"`
	Description      string `json:"description"`
}

// UpdatePlan 更新 VIP 套餐
func (s *VIPService) UpdatePlan(id string, req *UpdateVIPPlanRequest) (*model.VIPPlan, error) {
	plan, err := s.vipRepo.GetPlanByID(id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		plan.Name = *req.Name
	}
	if req.PriceFen != nil {
		plan.PriceFen = *req.PriceFen
	}
	if req.DurationDays != nil && *req.DurationDays > 0 {
		plan.DurationDays = *req.DurationDays
	}
	if req.DiscountPercent != nil {
		plan.DiscountPercent = *req.DiscountPercent
	}
	if req.MaxConversations != nil {
		plan.MaxConversations = *req.MaxConversations
	}
	if req.MaxAgentSessions != nil {
		plan.MaxAgentSessions = *req.MaxAgentSessions
	}
	if req.AsrQuotaMonthly != nil {
		plan.AsrQuotaMonthly = *req.AsrQuotaMonthly
	}
	if req.AgentEnabled != nil {
		plan.AgentEnabled = *req.AgentEnabled
	}
	if req.FileToolsEnabled != nil {
		plan.FileToolsEnabled = *req.FileToolsEnabled
	}
	if req.SortOrder != nil {
		plan.SortOrder = *req.SortOrder
	}
	if req.IsEnabled != nil {
		plan.IsEnabled = *req.IsEnabled
	}
	if req.Description != nil {
		plan.Description = *req.Description
	}
	if err := s.vipRepo.UpdatePlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// UpdateVIPPlanRequest 更新套餐请求（指针字段区分未传）
type UpdateVIPPlanRequest struct {
	Name             *string `json:"name,omitempty"`
	PriceFen         *int64  `json:"price_fen,omitempty"`
	DurationDays     *int    `json:"duration_days,omitempty"`
	DiscountPercent  *int    `json:"discount_percent,omitempty"`
	MaxConversations *int    `json:"max_conversations,omitempty"`
	MaxAgentSessions *int    `json:"max_agent_sessions,omitempty"`
	AsrQuotaMonthly  *int64  `json:"asr_quota_monthly,omitempty"`
	AgentEnabled     *bool   `json:"agent_enabled,omitempty"`
	FileToolsEnabled *bool   `json:"file_tools_enabled,omitempty"`
	SortOrder        *int    `json:"sort_order,omitempty"`
	IsEnabled        *bool   `json:"is_enabled,omitempty"`
	Description      *string `json:"description,omitempty"`
}

// DeletePlan 删除套餐
func (s *VIPService) DeletePlan(id string) error {
	return s.vipRepo.DeletePlan(id)
}

// Subscribe 用户订阅/更换 VIP 套餐，返回支付所需信息
func (s *VIPService) Subscribe(userID, planID, channel string, useElegant bool) (*SubscribeResult, error) {
	plan, err := s.vipRepo.GetPlanByID(planID)
	if err != nil {
		return nil, errors.New("套餐不存在")
	}
	if !plan.IsEnabled {
		return nil, errors.New("套餐已下架")
	}
	if plan.Level == 0 {
		return nil, errors.New("小弹丸无需订阅")
	}
	if channel != "wechat" && channel != "alipay" {
		channel = "wechat"
	}

	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}

	var refundAmount int64
	oldSub, _ := s.vipRepo.GetActiveSubscriptionByUserID(userID)
	if oldSub != nil && oldSub.Level != plan.Level {
		preview, err := s.calcRefund(oldSub)
		if err != nil {
			return nil, err
		}
		refundAmount = preview.RefundAmount
	}

	availableElegant := user.ElegantBalance
	if useElegant {
		availableElegant += refundAmount
	}

	var elegantDeducted int64
	cashAmount := plan.PriceFen
	if useElegant {
		if availableElegant >= plan.PriceFen {
			elegantDeducted = plan.PriceFen
			cashAmount = 0
		} else {
			elegantDeducted = availableElegant
			cashAmount = plan.PriceFen - availableElegant
		}
	}

	// 优雅弹丸足额时直接激活，不创建现金订单
	if cashAmount == 0 {
		if err := s.activateInTx(userID, plan, refundAmount, elegantDeducted, oldSub); err != nil {
			return nil, err
		}
		return &SubscribeResult{
			OrderID:         "",
			ProductType:     "vip",
			Amount:          0,
			ElegantDeducted: elegantDeducted,
			RefundAmount:    refundAmount,
			CashAmount:      0,
		}, nil
	}

	// 创建现金订单，记录应抵扣优雅弹丸
	order := &model.Order{
		ID:              uuid.New().String(),
		UserID:          userID,
		Channel:         channel,
		Amount:          cashAmount,
		Currency:        "cny",
		Status:          "pending",
		ProductType:     "vip",
		VIPPlanID:       planID,
		ElegantDeducted: elegantDeducted,
		Quantity:        1,
		Danwan:          0,
	}
	if err := s.orderRepo.Create(order); err != nil {
		return nil, fmt.Errorf("创建订单失败: %w", err)
	}
	return &SubscribeResult{
		OrderID:         order.ID,
		ProductType:     "vip",
		Amount:          cashAmount,
		ElegantDeducted: elegantDeducted,
		RefundAmount:    refundAmount,
		CashAmount:      cashAmount,
	}, nil
}

// ActivateSubscription 订单支付成功后激活会员订阅（优雅弹丸抵扣在下单时已计算，这里统一执行）。
// 幂等：事务内以「pending→paid 条件更新认领」作为唯一发放凭证，
// 支付宝重复 notify、notify 与管理员确认并发等场景下订阅只创建一次。
// tradeNo 为支付宝交易号（管理员确认等无渠道单号场景传空串，不覆盖已有值）。
func (s *VIPService) ActivateSubscription(orderID, tradeNo string) error {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return errors.New("订单不存在")
	}
	if order.ProductType != "vip" {
		return errors.New("订单类型不正确")
	}
	if order.Status == "paid" {
		return nil // 已处理，幂等返回
	}
	if order.Status != "pending" {
		return errors.New("订单状态不正确")
	}

	plan, err := s.vipRepo.GetPlanByID(order.VIPPlanID)
	if err != nil {
		return errors.New("套餐不存在")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// 事务内操作
		userTx := repository.NewUserRepo(tx)
		vipTx := repository.NewVIPRepo(tx)
		billTx := repository.NewBillingRepo(tx)
		orderTx := repository.NewOrderRepo(tx)

		// 先认领订单：只有首次 pending→paid 的调用才继续发放权益
		claimed, err := orderTx.ClaimPaid(orderID, tradeNo, time.Now())
		if err != nil {
			return err
		}
		if !claimed {
			return nil // 并发/重复调用，幂等返回
		}

		user, err := userTx.GetByID(order.UserID)
		if err != nil {
			return err
		}

		oldSub, _ := vipTx.GetActiveSubscriptionByUserID(order.UserID)
		var refundAmount int64
		if oldSub != nil && oldSub.Level != plan.Level {
			preview, _ := s.calcRefund(oldSub)
			refundAmount = preview.RefundAmount
			if refundAmount > 0 {
				if err := userTx.UpdateElegantBalance(order.UserID, refundAmount); err != nil {
					return err
				}
				if err := billTx.CreateTransaction(&model.BalanceTransaction{
					ID:           uuid.New().String(),
					UserID:       order.UserID,
					Type:         "refund",
					Amount:       refundAmount,
					Currency:     CurrencyElegant,
					BalanceAfter: user.ElegantBalance + refundAmount,
					Description:  fmt.Sprintf("VIP 更换计划退款: %s", oldSub.ID),
				}); err != nil {
					return err
				}
			}
			oldSub.Status = "cancelled"
			if err := vipTx.UpdateSubscription(oldSub); err != nil {
				return err
			}
		}

		// 优雅弹丸抵扣（现金订单中用户选择抵扣的部分）
		if order.ElegantDeducted > 0 {
			available := user.ElegantBalance + refundAmount
			if available < order.ElegantDeducted {
				return errors.New("优雅弹丸余额不足，无法完成抵扣")
			}
			if err := userTx.UpdateElegantBalance(order.UserID, -order.ElegantDeducted); err != nil {
				return err
			}
			if err := billTx.CreateTransaction(&model.BalanceTransaction{
				ID:           uuid.New().String(),
				UserID:       order.UserID,
				Type:         "consume",
				Amount:       -order.ElegantDeducted,
				Currency:     CurrencyElegant,
				BalanceAfter: available - order.ElegantDeducted,
				Description:  fmt.Sprintf("VIP 订阅抵扣: %s", plan.Name),
			}); err != nil {
				return err
			}
		}

		start := time.Now()
		if oldSub != nil && oldSub.Level == plan.Level && oldSub.ExpiresAt.After(start) {
			start = oldSub.ExpiresAt
		}
		expireAt := start.AddDate(0, 0, plan.DurationDays)

		sub := &model.VIPSubscription{
			ID:           uuid.New().String(),
			UserID:       order.UserID,
			PlanID:       plan.ID,
			Level:        plan.Level,
			PriceFen:     plan.PriceFen,
			DurationDays: plan.DurationDays,
			StartedAt:    start,
			ExpiresAt:    expireAt,
			Status:       "active",
		}
		if err := vipTx.CreateSubscription(sub); err != nil {
			return err
		}
		if err := userTx.UpdateVIP(order.UserID, plan.Level, expireAt, &plan.ID); err != nil {
			return err
		}
		// 订单状态与 paid_at 已在认领（ClaimPaid）时写入
		return nil
	})
}

// activateInTx 优雅弹丸足额时直接激活（不走现金订单）
func (s *VIPService) activateInTx(userID string, plan *model.VIPPlan, refundAmount, elegantDeducted int64, oldSub *model.VIPSubscription) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		userTx := repository.NewUserRepo(tx)
		vipTx := repository.NewVIPRepo(tx)
		billTx := repository.NewBillingRepo(tx)

		user, err := userTx.GetByID(userID)
		if err != nil {
			return err
		}

		// 1. 旧套餐退费（更换计划时）
		if oldSub != nil && oldSub.Level != plan.Level && refundAmount > 0 {
			if err := userTx.UpdateElegantBalance(userID, refundAmount); err != nil {
				return err
			}
			if err := billTx.CreateTransaction(&model.BalanceTransaction{
				ID:           uuid.New().String(),
				UserID:       userID,
				Type:         "refund",
				Amount:       refundAmount,
				Currency:     CurrencyElegant,
				BalanceAfter: user.ElegantBalance + refundAmount,
				Description:  fmt.Sprintf("VIP 更换计划退款: %s", oldSub.ID),
			}); err != nil {
				return err
			}
			oldSub.Status = "cancelled"
			if err := vipTx.UpdateSubscription(oldSub); err != nil {
				return err
			}
		}

		// 2. 优雅弹丸抵扣
		if elegantDeducted > 0 {
			if user.ElegantBalance+refundAmount < elegantDeducted {
				return errors.New("优雅弹丸余额不足")
			}
			if err := userTx.UpdateElegantBalance(userID, -elegantDeducted); err != nil {
				return err
			}
			if err := billTx.CreateTransaction(&model.BalanceTransaction{
				ID:           uuid.New().String(),
				UserID:       userID,
				Type:         "consume",
				Amount:       -elegantDeducted,
				Currency:     CurrencyElegant,
				BalanceAfter: user.ElegantBalance + refundAmount - elegantDeducted,
				Description:  fmt.Sprintf("VIP 订阅抵扣: %s", plan.Name),
			}); err != nil {
				return err
			}
		}

		// 3. 创建订阅
		start := time.Now()
		if oldSub != nil && oldSub.Level == plan.Level && oldSub.ExpiresAt.After(start) {
			start = oldSub.ExpiresAt
		}
		expireAt := start.AddDate(0, 0, plan.DurationDays)
		sub := &model.VIPSubscription{
			ID:           uuid.New().String(),
			UserID:       userID,
			PlanID:       plan.ID,
			Level:        plan.Level,
			PriceFen:     plan.PriceFen,
			DurationDays: plan.DurationDays,
			StartedAt:    start,
			ExpiresAt:    expireAt,
			Status:       "active",
		}
		if err := vipTx.CreateSubscription(sub); err != nil {
			return err
		}
		return userTx.UpdateVIP(userID, plan.Level, expireAt, &plan.ID)
	})
}

// calcRefund 计算取消/更换计划时的退费预览
func (s *VIPService) calcRefund(sub *model.VIPSubscription) (*RefundPreview, error) {
	if sub.Status != "active" || sub.ExpiresAt.Before(time.Now()) {
		return &RefundPreview{RemainingWeeks: 0, RefundAmount: 0}, nil
	}
	remainingHours := sub.ExpiresAt.Sub(time.Now()).Hours()
	remainingWeeks := int(math.Floor(remainingHours / 168))
	if remainingWeeks < 0 {
		remainingWeeks = 0
	}
	refundAmount := sub.PriceFen * int64(remainingWeeks) / 4
	return &RefundPreview{
		RemainingWeeks: remainingWeeks,
		RefundAmount:   refundAmount,
	}, nil
}

// CancelSubscription 取消订阅并按周退费
func (s *VIPService) CancelSubscription(userID, subID string) (*RefundPreview, error) {
	sub, err := s.vipRepo.GetSubscriptionByID(subID)
	if err != nil {
		return nil, errors.New("订阅不存在")
	}
	if sub.UserID != userID {
		return nil, errors.New("无权操作")
	}
	preview, err := s.calcRefund(sub)
	if err != nil {
		return nil, err
	}
	if preview.RefundAmount > 0 {
		if err := s.userRepo.UpdateElegantBalance(userID, preview.RefundAmount); err != nil {
			return nil, fmt.Errorf("退还优雅弹丸失败: %w", err)
		}
		user, _ := s.userRepo.GetByID(userID)
		tx := &model.BalanceTransaction{
			ID:           uuid.New().String(),
			UserID:       userID,
			Type:         "refund",
			Amount:       preview.RefundAmount,
			Currency:     CurrencyElegant,
			BalanceAfter: user.ElegantBalance + preview.RefundAmount,
			Description:  fmt.Sprintf("VIP 退订退款: %d 周", preview.RemainingWeeks),
		}
		_ = s.billRepo.CreateTransaction(tx)
	}
	sub.Status = "cancelled"
	if err := s.vipRepo.UpdateSubscription(sub); err != nil {
		return nil, err
	}
	// 同步用户 VIP 字段为 0
	if err := s.userRepo.UpdateVIP(userID, 0, time.Time{}, nil); err != nil {
		return nil, err
	}
	return preview, nil
}

// GrantSubscriptionByAdmin 管理员手动开通/续期会员
func (s *VIPService) GrantSubscriptionByAdmin(userID, planID string, months int) error {
	if months <= 0 {
		months = 1
	}
	plan, err := s.vipRepo.GetPlanByID(planID)
	if err != nil {
		return errors.New("套餐不存在")
	}

	oldSub, _ := s.vipRepo.GetActiveSubscriptionByUserID(userID)
	if oldSub != nil && oldSub.Level != plan.Level {
		preview, _ := s.calcRefund(oldSub)
		if preview.RefundAmount > 0 {
			_ = s.userRepo.UpdateElegantBalance(userID, preview.RefundAmount)
		}
		oldSub.Status = "cancelled"
		_ = s.vipRepo.UpdateSubscription(oldSub)
	}

	start := time.Now()
	if oldSub != nil && oldSub.Level == plan.Level && oldSub.ExpiresAt.After(start) {
		start = oldSub.ExpiresAt
	}
	expireAt := start.AddDate(0, 0, plan.DurationDays*months)

	sub := &model.VIPSubscription{
		ID:           uuid.New().String(),
		UserID:       userID,
		PlanID:       plan.ID,
		Level:        plan.Level,
		PriceFen:     plan.PriceFen,
		DurationDays: plan.DurationDays * months,
		StartedAt:    start,
		ExpiresAt:    expireAt,
		Status:       "active",
	}
	if err := s.vipRepo.CreateSubscription(sub); err != nil {
		return err
	}
	return s.userRepo.UpdateVIP(userID, plan.Level, expireAt, &plan.ID)
}

// ActivateSubscriptionByCDK 兑换码激活会员
func (s *VIPService) ActivateSubscriptionByCDK(userID string, level, durationDays int) error {
	plan, err := s.vipRepo.GetPlanByLevel(level)
	if err != nil {
		return errors.New("对应等级 VIP 套餐不存在或未上架")
	}

	oldSub, _ := s.vipRepo.GetActiveSubscriptionByUserID(userID)
	start := time.Now()
	if oldSub != nil && oldSub.Level == plan.Level && oldSub.ExpiresAt.After(start) {
		start = oldSub.ExpiresAt
	}
	if durationDays <= 0 {
		durationDays = 30
	}
	expireAt := start.AddDate(0, 0, durationDays)

	sub := &model.VIPSubscription{
		ID:           uuid.New().String(),
		UserID:       userID,
		PlanID:       plan.ID,
		Level:        plan.Level,
		PriceFen:     plan.PriceFen,
		DurationDays: durationDays,
		StartedAt:    start,
		ExpiresAt:    expireAt,
		Status:       "active",
	}
	if err := s.vipRepo.CreateSubscription(sub); err != nil {
		return err
	}
	return s.userRepo.UpdateVIP(userID, plan.Level, expireAt, &plan.ID)
}

// ApplyDiscount 对费用应用 VIP 折扣（返回折扣后费用）。管理员不再无条件免费，按实际 VIP 订阅折扣计费。
func (s *VIPService) ApplyDiscount(userID string, cost int64) (int64, error) {
	sub, err := s.vipRepo.GetActiveSubscriptionByUserID(userID)
	if err != nil {
		// 无生效订阅按原价计费
		return cost, nil
	}
	plan, err := s.vipRepo.GetPlanByID(sub.PlanID)
	if err != nil {
		return 0, fmt.Errorf("查询套餐失败: %w", err)
	}
	if plan.DiscountPercent <= 0 || plan.DiscountPercent > 100 {
		return cost, nil
	}
	return cost * int64(plan.DiscountPercent) / 100, nil
}

// HasFeature 判断用户是否拥有某项 VIP 权益
func (s *VIPService) HasFeature(userID string, feature string) (bool, error) {
	if s.unrestricted {
		return true, nil // claw 本地不限功能开关
	}
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return false, err
	}
	if user.Role == model.UserRoleAdmin {
		return true, nil
	}
	status, err := s.GetEffectiveVIP(userID)
	if err != nil {
		return false, err
	}
	return status.Features[feature], nil
}

// GetMaxConversations 获取用户最大历史会话配额
func (s *VIPService) GetMaxConversations(userID string) (int, error) {
	if s.unrestricted {
		return math.MaxInt32, nil // claw 本地不限对话数
	}
	status, err := s.GetEffectiveVIP(userID)
	if err != nil {
		return 0, err
	}
	return status.MaxConversations, nil
}

// GetMaxAgentSessions 获取用户最大 Agent Session 配额
func (s *VIPService) GetMaxAgentSessions(userID string) (int, error) {
	if s.unrestricted {
		return math.MaxInt32, nil // claw 本地不限 Agent Session 数
	}
	status, err := s.GetEffectiveVIP(userID)
	if err != nil {
		return 0, err
	}
	return status.MaxAgentSessions, nil
}

// ListSubscriptions 查询订阅记录（管理后台）
func (s *VIPService) ListSubscriptions(page, pageSize int, userID string) ([]*model.VIPSubscription, int64, error) {
	return s.vipRepo.ListSubscriptions(page, pageSize, userID)
}

// GetAsrQuotaMonthly 获取用户 ASR 月度额度
func (s *VIPService) GetAsrQuotaMonthly(userID string) (int64, error) {
	if s.unrestricted {
		return math.MaxInt64, nil // claw STT 走集市模块（用户自带 key），本地不限 ASR 配额
	}
	status, err := s.GetEffectiveVIP(userID)
	if err != nil {
		return 0, err
	}
	return status.AsrQuotaMonthly, nil
}
