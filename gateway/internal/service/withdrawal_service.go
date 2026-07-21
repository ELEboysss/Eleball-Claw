package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// WithdrawalService 提现服务
type WithdrawalService struct {
	db               *gorm.DB
	withdrawalRepo   *repository.WithdrawalRepo
	agentRepo        *repository.AgentRepo
	paymentProvider  PaymentProvider
	config           model.WithdrawalConfig
}

// NewWithdrawalService 创建服务
func NewWithdrawalService(
	db *gorm.DB,
	withdrawalRepo *repository.WithdrawalRepo,
	agentRepo *repository.AgentRepo,
	paymentProvider PaymentProvider,
) *WithdrawalService {
	return &WithdrawalService{
		db:              db,
		withdrawalRepo:  withdrawalRepo,
		agentRepo:       agentRepo,
		paymentProvider: paymentProvider,
		config:          model.DefaultWithdrawalConfig,
	}
}

// ApplyWithdrawalRequest 提现申请请求
type ApplyWithdrawalRequest struct {
	Amount      int64  `json:"amount" binding:"required,min=1"`      // 金额（分）
	Channel     string `json:"channel" binding:"required,oneof=wechat alipay"`
	AccountInfo string `json:"account_info" binding:"required"`      // 收款账号
	RealName    string `json:"real_name" binding:"required"`         // 真实姓名
}

// ApplyWithdrawal 申请提现
func (s *WithdrawalService) ApplyWithdrawal(userID, userName string, req ApplyWithdrawalRequest) (*model.WithdrawalRecord, error) {
	// 1. 检查金额限制
	if req.Amount < s.config.MinAmount {
		return nil, fmt.Errorf("最小提现金额为 %.2f 元", float64(s.config.MinAmount)/100)
	}
	if req.Amount > s.config.MaxAmount {
		return nil, fmt.Errorf("最大提现金额为 %.2f 元", float64(s.config.MaxAmount)/100)
	}

	// 2. 检查开发者账户余额
	acc, err := s.agentRepo.GetDeveloperAccount(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("开发者账户不存在")
		}
		return nil, err
	}
	if acc.ElegantBalance < req.Amount {
		return nil, errors.New("优雅弹丸余额不足")
	}

	// 3. 检查每日限额
	todayAmount, _ := s.withdrawalRepo.SumTodayAmount(userID)
	if todayAmount+req.Amount > s.config.DailyLimit {
		return nil, fmt.Errorf("今日提现额度已用完，剩余 %.2f 元", float64(s.config.DailyLimit-todayAmount)/100)
	}

	// 4. 扣除余额（预冻结）
	if err := s.db.Model(&model.DeveloperAccount{}).
		Where("user_id = ? AND elegant_balance >= ?", userID, req.Amount).
		Update("elegant_balance", gorm.Expr("elegant_balance - ?", req.Amount)).Error; err != nil {
		return nil, fmt.Errorf("扣除余额失败: %w", err)
	}

	// 5. 创建提现记录
	record := &model.WithdrawalRecord{
		ID:          uuid.New().String(),
		UserID:      userID,
		UserName:    userName,
		Amount:      req.Amount,
		Channel:     req.Channel,
		AccountInfo: req.AccountInfo,
		RealName:    req.RealName,
		Status:      model.WithdrawalStatusPending,
	}
	if err := s.withdrawalRepo.Create(record); err != nil {
		// 回滚余额
		s.db.Model(&model.DeveloperAccount{}).Where("user_id = ?", userID).
			Update("elegant_balance", gorm.Expr("elegant_balance + ?", req.Amount))
		return nil, fmt.Errorf("创建记录失败: %w", err)
	}

	return record, nil
}

// ListMyWithdrawals 查询我的提现记录
func (s *WithdrawalService) ListMyWithdrawals(userID string, page, pageSize int) ([]*model.WithdrawalRecord, int64, error) {
	return s.withdrawalRepo.ListByUser(userID, page, pageSize)
}

// ApproveWithdrawal 审核通过并付款
func (s *WithdrawalService) ApproveWithdrawal(adminID, recordID, adminNote string) error {
	record, err := s.withdrawalRepo.GetByID(recordID)
	if err != nil {
		return errors.New("记录不存在")
	}
	if record.Status != model.WithdrawalStatusPending {
		return errors.New("记录状态不允许审核")
	}

	// 更新为已批准
	if err := s.withdrawalRepo.UpdateStatus(recordID, model.WithdrawalStatusApproved, adminNote, ""); err != nil {
		return err
	}

	// 调用企业付款
	ctx := context.Background()
	result, err := s.paymentProvider.Transfer(ctx, TransferRequest{
		OrderID:  record.ID,
		Amount:   record.Amount,
		Account:  record.AccountInfo,
		RealName: record.RealName,
		Desc:     "Eleball 开发者提现",
	})

	if err != nil || !result.Success {
		// 付款失败，回滚余额
		s.db.Model(&model.DeveloperAccount{}).Where("user_id = ?", record.UserID).
			Update("elegant_balance", gorm.Expr("elegant_balance + ?", record.Amount))
		s.withdrawalRepo.UpdateStatus(recordID, model.WithdrawalStatusFailed, "付款失败: "+result.ErrMsg, "")
		return fmt.Errorf("付款失败: %s", result.ErrMsg)
	}

	// 更新为已完成
	return s.withdrawalRepo.UpdateStatus(recordID, model.WithdrawalStatusCompleted, adminNote, result.TxID)
}

// RejectWithdrawal 审核拒绝
func (s *WithdrawalService) RejectWithdrawal(adminID, recordID, adminNote string) error {
	record, err := s.withdrawalRepo.GetByID(recordID)
	if err != nil {
		return errors.New("记录不存在")
	}
	if record.Status != model.WithdrawalStatusPending {
		return errors.New("记录状态不允许拒绝")
	}

	// 退回余额
	if err := s.db.Model(&model.DeveloperAccount{}).Where("user_id = ?", record.UserID).
		Update("elegant_balance", gorm.Expr("elegant_balance + ?", record.Amount)).Error; err != nil {
		return fmt.Errorf("退回余额失败: %w", err)
	}

	// 更新状态
	return s.withdrawalRepo.UpdateStatus(recordID, model.WithdrawalStatusRejected, adminNote, "")
}

// ListAllWithdrawals 管理员查询所有提现记录
func (s *WithdrawalService) ListAllWithdrawals(page, pageSize int, status string) ([]*model.WithdrawalRecord, int64, error) {
	return s.withdrawalRepo.ListAll(page, pageSize, status)
}
