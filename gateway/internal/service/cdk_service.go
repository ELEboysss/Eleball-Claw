package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/util"
	"github.com/google/uuid"
)

// CDKService 兑换码服务
type CDKService struct {
	cdkRepo    *repository.CDKRepo
	userRepo   *repository.UserRepo
	billRepo   *repository.BillingRepo
	vipService *VIPService
}

// NewCDKService 创建兑换码服务
func NewCDKService(cdkRepo *repository.CDKRepo, userRepo *repository.UserRepo, billRepo *repository.BillingRepo, vipService *VIPService) *CDKService {
	return &CDKService{
		cdkRepo:    cdkRepo,
		userRepo:   userRepo,
		billRepo:   billRepo,
		vipService: vipService,
	}
}

// BatchGenerateRequest 批量生成兑换码请求
// Value 为 0 表示不筛选面值（仅用于列表）
type BatchGenerateRequest struct {
	Type            string `json:"type" binding:"required,oneof=recharge vip"` // recharge / vip
	Value           int64  `json:"value"`                                      // 单个兑换码面值（弹丸数，recharge 时必填）
	VIPLevel        int    `json:"vip_level"`                                  // VIP 等级（type=vip 时必填）
	VIPDurationDays int    `json:"vip_duration_days"`                          // VIP 时长（天，type=vip 时默认 30）
	Count           int    `json:"count" binding:"required,min=1,max=500"`    // 生成数量
	Note            string `json:"note"`                                       // 备注
}

// BatchGenerateResponse 批量生成响应
type BatchGenerateResponse struct {
	BatchID string       `json:"batch_id"`
	Count   int          `json:"count"`
	Items   []*model.CDK `json:"items"`
}

// BatchGenerate 批量生成兑换码
func (s *CDKService) BatchGenerate(req BatchGenerateRequest) (*BatchGenerateResponse, error) {
	batchID := uuid.New().String()
	items := make([]*model.CDK, 0, req.Count)
	attempts := 0
	maxAttempts := req.Count * 10 // 防止极端情况下死循环

	// 参数校验
	if req.Type == model.CDKTypeRecharge && req.Value <= 0 {
		return nil, errors.New("弹丸充值码面值必须大于 0")
	}
	if req.Type == model.CDKTypeVIP && req.VIPLevel <= 0 {
		return nil, errors.New("VIP 兑换码等级必须大于 0")
	}
	if req.Type == model.CDKTypeVIP && req.VIPDurationDays <= 0 {
		req.VIPDurationDays = 30
	}

	for len(items) < int(req.Count) && attempts < maxAttempts {
		attempts++
		code, err := util.GenerateCDKCode(16)
		if err != nil {
			return nil, fmt.Errorf("生成兑换码失败: %w", err)
		}
		// 检查是否已存在
		exists, err := s.cdkRepo.CountByCode(code)
		if err != nil {
			return nil, fmt.Errorf("检查兑换码唯一性失败: %w", err)
		}
		if exists > 0 {
			continue
		}
		items = append(items, &model.CDK{
			ID:              uuid.New().String(),
			Code:            code,
			Type:            req.Type,
			Value:           req.Value,
			VIPLevel:        req.VIPLevel,
			VIPDurationDays: req.VIPDurationDays,
			Used:            false,
			BatchID:         batchID,
			Note:            req.Note,
		})
	}

	if len(items) < int(req.Count) {
		return nil, errors.New("生成兑换码时无法保证唯一性，请减少数量后重试")
	}

	if err := s.cdkRepo.CreateBatch(items); err != nil {
		return nil, fmt.Errorf("保存兑换码失败: %w", err)
	}

	return &BatchGenerateResponse{
		BatchID: batchID,
		Count:   len(items),
		Items:   items,
	}, nil
}

// CDKListRequest 兑换码列表请求
type CDKListRequest struct {
	Status   string `form:"status"`     // all / used / unused
	Value    int64  `form:"value"`      // -1 表示不筛选
	Search   string `form:"search"`
	BatchID  string `form:"batch_id"`
	Page     int    `form:"page"`
	PageSize int    `form:"page_size"`
}

// CDKListResponse 兑换码列表响应
type CDKListResponse = repository.CDKListResponse

// ListCDKs 查询兑换码列表
func (s *CDKService) ListCDKs(req CDKListRequest) (*CDKListResponse, error) {
	return s.cdkRepo.List(repository.CDKListFilters{
		Status:   req.Status,
		Value:    req.Value,
		Search:   req.Search,
		BatchID:  req.BatchID,
		Page:     req.Page,
		PageSize: req.PageSize,
	})
}

// DeleteCDK 删除兑换码
func (s *CDKService) DeleteCDK(id string) error {
	return s.cdkRepo.Delete(id)
}

// RedeemRequest 用户兑换请求
type RedeemRequest struct {
	Code string `json:"code" binding:"required"`
}

// RedeemResponse 用户兑换响应
type RedeemResponse struct {
	Value         int64     `json:"value"`
	Danwan        int64     `json:"danwan"`
	Elegant       int64     `json:"elegant"`
	VIPActivated  bool      `json:"vip_activated"`
	VIPLevel      int       `json:"vip_level"`
	VIPExpireAt   time.Time `json:"vip_expire_at"`
}

// Redeem 用户兑换兑换码
func (s *CDKService) Redeem(userID, rawCode string) (*RedeemResponse, error) {
	code := util.NormalizeCDKCode(rawCode)
	if code == "" {
		return nil, errors.New("兑换码不能为空")
	}

	cdk, err := s.cdkRepo.GetByCode(code)
	if err != nil {
		return nil, errors.New("兑换码无效")
	}
	if cdk.Used {
		return nil, errors.New("兑换码已被使用")
	}
	if cdk.Type == model.CDKTypeRecharge && cdk.Value <= 0 {
		return nil, errors.New("兑换码面值异常")
	}

	// 第一阶段：先把兑换码标记为已使用，防止并发重复兑换
	if err := s.cdkRepo.MarkUsed(cdk.ID, userID); err != nil {
		return nil, errors.New("兑换失败，请稍后重试")
	}

	// 第二阶段：根据兑换码类型给用户加权益
	redeemErr := func() error {
		switch cdk.Type {
		case model.CDKTypeVIP:
			return s.vipService.ActivateSubscriptionByCDK(userID, cdk.VIPLevel, cdk.VIPDurationDays)
		default:
			// recharge
			user, err := s.userRepo.GetByID(userID)
			if err != nil {
				return fmt.Errorf("查询用户失败: %w", err)
			}

			if err := s.userRepo.UpdateBalance(userID, cdk.Value); err != nil {
				return fmt.Errorf("更新弹丸余额失败: %w", err)
			}
			// 兑换码充值与现金充值等价，累计充值金额同步增加
			if err := s.userRepo.UpdateTotalRecharged(userID, cdk.Value); err != nil {
				return fmt.Errorf("更新累计充值金额失败: %w", err)
			}

			tx := &model.BalanceTransaction{
				ID:           uuid.New().String(),
				UserID:       userID,
				Type:         "recharge",
				Amount:       cdk.Value,
				Currency:     CurrencyDanwan,
				BalanceAfter: user.Balance + cdk.Value,
				Description:  fmt.Sprintf("兑换码充值: %s", util.FormatCDKCode(cdk.Code)),
			}
			if err := s.billRepo.CreateTransaction(tx); err != nil {
				return fmt.Errorf("记录交易流水失败: %w", err)
			}
			return nil
		}
	}()

	if redeemErr != nil {
		// 充值流程失败，回滚兑换码状态，避免码被吞
		if rbErr := s.cdkRepo.RollbackUsed(cdk.ID); rbErr != nil {
			return nil, fmt.Errorf("兑换失败且回滚异常: %v; 原始错误: %w", rbErr, redeemErr)
		}
		return nil, redeemErr
	}

	// 查询兑换后的最新余额/VIP 状态用于返回
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		// 余额查询失败不影响兑换成功
		return &RedeemResponse{Value: cdk.Value, VIPActivated: cdk.Type == model.CDKTypeVIP}, nil
	}

	resp := &RedeemResponse{
		Value:   cdk.Value,
		Danwan:  user.Balance,
		Elegant: user.ElegantBalance,
	}
	if cdk.Type == model.CDKTypeVIP {
		resp.VIPActivated = true
		resp.VIPLevel = user.VIPLevel
		resp.VIPExpireAt = user.VIPExpireAt
	}
	return resp, nil
}

// FormatForDisplay 把数据库中的兑换码格式化为展示形式
func (s *CDKService) FormatForDisplay(code string) string {
	return util.FormatCDKCode(code)
}

