package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/google/uuid"
)

// BillingService 计费服务
type BillingService struct {
	userRepo             *repository.UserRepo
	billRepo             *repository.BillingRepo
	eleAgentModelService *EleAgentModelService
	activityService      *ActivityService
	vipService           *VIPService
}

// NewBillingService 创建服务
func NewBillingService(userRepo *repository.UserRepo, billRepo *repository.BillingRepo, eleAgentModelService *EleAgentModelService, activityService *ActivityService, vipService *VIPService) *BillingService {
	return &BillingService{
		userRepo:             userRepo,
		billRepo:             billRepo,
		eleAgentModelService: eleAgentModelService,
		activityService:      activityService,
		vipService:           vipService,
	}
}

// Currency 货币类型常量
const (
	CurrencyDanwan  = "danwan"
	CurrencyElegant = "elegant"
)

// BalanceInfo 双货币余额信息
type BalanceInfo struct {
	Danwan  int64 `json:"danwan"`
	Elegant int64 `json:"elegant"`
}

// Deduct 按用量扣费（默认 source=agent，向后兼容；协同委派用 DeductWithSource 标注来源）
func (s *BillingService) Deduct(userID, provider, modelName, currency string, usage *llm.Usage) error {
	return s.DeductWithSource(userID, provider, modelName, currency, "agent", usage)
}

// DeductWithSource 按用量扣费并标注来源（agent / chat / call_assistant:子session / visual）。
// 仅对 Ele Agent 代理的模型收费；用户使用自带 API Key 的直连模型不扣费。
// currency 指定扣费货币：danwan / elegant，默认 danwan
func (s *BillingService) DeductWithSource(userID, provider, modelName, currency, source string, usage *llm.Usage) error {
	if currency == "" {
		currency = CurrencyDanwan
	}

	// 非 Ele Agent 请求不走平台余额扣费
	if provider != "eleagent" {
		return nil
	}

	// 无用量信息时不扣费（调用方应保证兜底估算后非 nil）
	if usage == nil {
		return nil
	}

	// Ele Agent 模型单价由管理员后台配置，分别按输入/输出 token 计费，单位为 弹丸 / 1M tokens
	// 同时支持按次附加费 price_per_generation，与 token 费用相加
	// modelName 约定格式为 "subProvider/subModel"，如 "qwen/Qwen/Qwen3-8B"
	var inputPrice, outputPrice, perGenPrice int64
	if s.eleAgentModelService != nil {
		searchProvider, searchModel := provider, modelName
		if idx := strings.Index(modelName, "/"); idx > 0 {
			searchProvider = modelName[:idx]
			searchModel = modelName[idx+1:]
		}
		inputPrice, outputPrice, perGenPrice = s.eleAgentModelService.GetModelPricing(searchProvider, searchModel)
	}

	inputCost := int64(usage.PromptTokens) * inputPrice / 1_000_000
	outputCost := int64(usage.CompletionTokens) * outputPrice / 1_000_000
	totalCost := inputCost + outputCost + perGenPrice
	// 输入或输出单价为正但 token 合计不足 1 弹丸时，最小按 1 弹丸计 token 费用；按次附加费仍额外累加
	if totalCost == perGenPrice && (inputPrice > 0 || outputPrice > 0) {
		totalCost = perGenPrice + 1
	}

	// VIP 折扣（VIP2 及以上）
	if s.vipService != nil {
		discounted, err := s.vipService.ApplyDiscount(userID, totalCost)
		if err == nil {
			totalCost = discounted
		}
	}

	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}

	username := user.Username
	if username == "" {
		username = userID
	}

	var balanceAfter int64
	switch currency {
	case CurrencyElegant:
		if user.ElegantBalance < totalCost {
			return errors.New("优雅弹丸余额不足，请充值")
		}
		if err := s.userRepo.UpdateElegantBalance(userID, -totalCost); err != nil {
			return fmt.Errorf("扣减优雅弹丸失败: %w", err)
		}
		balanceAfter = user.ElegantBalance - totalCost
	default:
		if user.Balance < totalCost {
			return errors.New("弹丸余额不足，请充值")
		}
		if err := s.userRepo.UpdateBalance(userID, -totalCost); err != nil {
			return fmt.Errorf("扣减弹丸失败: %w", err)
		}
		balanceAfter = user.Balance - totalCost
	}

	// 记录 Token 用量
	tokenUsage := &model.TokenUsage{
		ID:           uuid.New().String(),
		UserID:       userID,
		ModelID:      modelName,
		Provider:     provider,
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		CostAmount:   totalCost,
		Currency:     currency,
		Source:       source,
	}
	if err := s.billRepo.CreateTokenUsage(tokenUsage); err != nil {
		// 用量记录失败不阻断主流程，但记录日志
		_ = err
	}

	// 记录余额流水
	tx := &model.BalanceTransaction{
		ID:           uuid.New().String(),
		UserID:       userID,
		Type:         "consume",
		Amount:       -totalCost,
		Currency:     currency,
		BalanceAfter: balanceAfter,
		Description:  fmt.Sprintf("模型调用: %s/%s", provider, modelName),
	}
	if err := s.billRepo.CreateTransaction(tx); err != nil {
		// 流水记录失败不阻断主流程，但记录日志
		_ = err
	}

	// 记录管理后台动态
	if s.activityService != nil {
		inputTokens := int64(usage.PromptTokens)
		outputTokens := int64(usage.CompletionTokens)
		s.activityService.RecordModelUsage(userID, username, provider, modelName, totalCost, currency, inputTokens, outputTokens)
	}

	return nil
}

// CheckBalance 检查用户是否有余额调用 Ele Agent 付费模型
// 仅当 provider 为 eleagent 且模型单价大于 0 时才进行校验；免费模型与直连模型始终放行。
func (s *BillingService) CheckBalance(userID, provider, modelName, currency string) error {
	if currency == "" {
		currency = CurrencyDanwan
	}

	// 非 Ele Agent 请求不扣平台余额，无需校验
	if provider != "eleagent" {
		return nil
	}

	// 计算模型输入/输出单价与按次附加费
	var inputPrice, outputPrice, perGenPrice int64
	if s.eleAgentModelService != nil {
		searchProvider, searchModel := provider, modelName
		if idx := strings.Index(modelName, "/"); idx > 0 {
			searchProvider = modelName[:idx]
			searchModel = modelName[idx+1:]
		}
		inputPrice, outputPrice, perGenPrice = s.eleAgentModelService.GetModelPricing(searchProvider, searchModel)
	}

	// 输入、输出、按次附加费均为 0 的模型视为免费，不做余额限制
	if inputPrice <= 0 && outputPrice <= 0 && perGenPrice <= 0 {
		return nil
	}

	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}

	// 付费模型至少需要 max(1, perGenPrice) 弹丸余额；防止余额为 0 的用户调用后扣费失败
	minRequired := int64(1)
	if perGenPrice > minRequired {
		minRequired = perGenPrice
	}
	switch currency {
	case CurrencyElegant:
		if user.ElegantBalance < minRequired {
			return errors.New("优雅弹丸余额不足，请充值")
		}
	default:
		if user.Balance < minRequired {
			return errors.New("弹丸余额不足，请充值")
		}
	}

	return nil
}

// RechargeHistoryItem 用户充值记录项
type RechargeHistoryItem struct {
	ID          string `json:"id"`
	SourceType  string `json:"source_type"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
}

// RechargeHistoryResponse 用户充值记录响应
type RechargeHistoryResponse struct {
	Items []*RechargeHistoryItem `json:"items"`
	Total int64                  `json:"total"`
}

// GetRechargeHistory 查询用户充值记录（仅包含 type=recharge 的余额流水）
func (s *BillingService) GetRechargeHistory(userID string, page, pageSize int) (*RechargeHistoryResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	items, total, err := s.billRepo.ListTransactionsByUser(userID, page, pageSize, "recharge")
	if err != nil {
		return nil, err
	}

	result := make([]*RechargeHistoryItem, 0, len(items))
	for _, tx := range items {
		// 根据描述推断来源：兑换码 / 手动充值 / 未知
		sourceType := "unknown"
		if strings.Contains(tx.Description, "兑换码") {
			sourceType = "cdk"
		} else if strings.Contains(tx.Description, "手动充值") || strings.Contains(tx.Description, "管理员") {
			sourceType = "manual"
		} else if strings.Contains(tx.Description, "微信") || strings.Contains(tx.Description, "微信支付") {
			sourceType = "wechat"
		} else if strings.Contains(tx.Description, "支付宝") {
			sourceType = "alipay"
		}

		createdAt := tx.CreatedAt.UnixMilli()
		if createdAt == 0 {
			createdAt = tx.CreatedAt.Unix() * 1000
		}

		result = append(result, &RechargeHistoryItem{
			ID:          tx.ID,
			SourceType:  sourceType,
			Amount:      tx.Amount,
			Currency:    tx.Currency,
			Description: tx.Description,
			CreatedAt:   createdAt,
		})
	}

	return &RechargeHistoryResponse{
		Items: result,
		Total: total,
	}, nil
}

// CheckBalanceGeneric 通用余额检查，要求用户指定货币余额不少于 minRequired
func (s *BillingService) CheckBalanceGeneric(userID, currency string, minRequired int64) error {
	if currency == "" {
		currency = CurrencyDanwan
	}

	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}

	switch currency {
	case CurrencyElegant:
		if user.ElegantBalance < minRequired {
			return errors.New("优雅弹丸余额不足，请充值")
		}
	default:
		if user.Balance < minRequired {
			return errors.New("弹丸余额不足，请充值")
		}
	}
	return nil
}

// DeductVisual 视觉生成扣费
// cost 为已计算好的弹丸数；mediaType 用于描述，usage 可选（视频模型返回 token 时）。
func (s *BillingService) DeductVisual(userID, provider, modelName, mediaType, currency string, cost int64, promptTokens, completionTokens int) error {
	if currency == "" {
		currency = CurrencyDanwan
	}

	if cost <= 0 {
		return nil
	}

	if s.vipService != nil {
		discounted, err := s.vipService.ApplyDiscount(userID, cost)
		if err == nil {
			cost = discounted
		}
	}

	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}

	username := user.Username
	if username == "" {
		username = userID
	}

	var balanceAfter int64
	switch currency {
	case CurrencyElegant:
		if user.ElegantBalance < cost {
			return errors.New("优雅弹丸余额不足，请充值")
		}
		if err := s.userRepo.UpdateElegantBalance(userID, -cost); err != nil {
			return fmt.Errorf("扣减优雅弹丸失败: %w", err)
		}
		balanceAfter = user.ElegantBalance - cost
	default:
		if user.Balance < cost {
			return errors.New("弹丸余额不足，请充值")
		}
		if err := s.userRepo.UpdateBalance(userID, -cost); err != nil {
			return fmt.Errorf("扣减弹丸失败: %w", err)
		}
		balanceAfter = user.Balance - cost
	}

	// 记录 Token 用量（视觉生成主要按次计费，token 信息可选）
	tokenUsage := &model.TokenUsage{
		ID:           uuid.New().String(),
		UserID:       userID,
		ModelID:      modelName,
		Provider:     provider,
		InputTokens:  promptTokens,
		OutputTokens: completionTokens,
		CostAmount:   cost,
		Currency:     currency,
	}
	if err := s.billRepo.CreateTokenUsage(tokenUsage); err != nil {
		_ = err
	}

	// 记录余额流水
	tx := &model.BalanceTransaction{
		ID:           uuid.New().String(),
		UserID:       userID,
		Type:         "consume",
		Amount:       -cost,
		Currency:     currency,
		BalanceAfter: balanceAfter,
		Description:  fmt.Sprintf("视觉生成(%s): %s/%s", mediaType, provider, modelName),
	}
	if err := s.billRepo.CreateTransaction(tx); err != nil {
		_ = err
	}

	// 记录管理后台动态
	if s.activityService != nil {
		s.activityService.RecordModelUsage(userID, username, provider, modelName, cost, currency, int64(promptTokens), int64(completionTokens))
	}

	return nil
}

// GetBalance 查询双货币余额
func (s *BillingService) GetBalance(userID string) (*BalanceInfo, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, err
	}
	return &BalanceInfo{
		Danwan:  user.Balance,
		Elegant: user.ElegantBalance,
	}, nil
}
