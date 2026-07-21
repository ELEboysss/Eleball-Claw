package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EleAgentService Ele Agent 官方模型凭证服务
// 采用后端代理模式：客户端凭此凭证将请求发给后端 /v1/chat/completions（provider=eleagent），
// 后端根据管理员配置的 EleAgentModelConfig 调用真实模型，并按 token 扣费。
type EleAgentService struct {
	chatService          *ChatProxyService
	eleagentModelService *EleAgentModelService
	billingService       *BillingService
	eleagentBaseURL      string
}

// EleAgentCredentials Ele Agent 调用凭证
type EleAgentCredentials struct {
	BaseURL   string    `json:"baseUrl"`
	APIKey    string    `json:"apiKey"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// NewEleAgentService 创建 Ele Agent 服务
// eleagentBaseURL: Ele Agent 代理 BaseURL，如 https://api.eleball.cn/v1
func NewEleAgentService(chatService *ChatProxyService, eleAgentModelService *EleAgentModelService, billingService *BillingService, eleagentBaseURL string) *EleAgentService {
	if eleagentBaseURL == "" {
		eleagentBaseURL = "https://api.eleball.cn/v1"
	}
	return &EleAgentService{
		chatService:          chatService,
		eleagentModelService: eleAgentModelService,
		billingService:       billingService,
		eleagentBaseURL:      eleagentBaseURL,
	}
}

// GetCredentials 获取 Ele Agent 调用凭证
// subProvider: 子平台 Provider，如 qwen / openai / deepseek
// subModel: 子平台模型名，如 Qwen/Qwen3-8B / gpt-4o
func (s *EleAgentService) GetCredentials(userID, subProvider, subModel string) (*EleAgentCredentials, error) {
	if subProvider == "" || subModel == "" {
		return nil, errors.New("子平台 Provider 和模型名不能为空")
	}

	// 校验该模型是否已在管理员后台配置
	if !s.eleagentModelService.HasModel(subProvider, subModel) {
		return nil, fmt.Errorf("不支持的 Ele Agent 模型: %s/%s", subProvider, subModel)
	}

	// 付费模型需校验余额；余额为负则拒绝发放凭证
	modelKey := subProvider + "/" + subModel
	if s.billingService != nil {
		if err := s.billingService.CheckBalance(userID, "eleagent", modelKey, CurrencyDanwan); err != nil {
			return nil, &BalanceInsufficientError{Message: err.Error()}
		}
	}

	// 后端代理模式下，凭证指向网关自身，API Key 为内部占位符
	return &EleAgentCredentials{
		BaseURL:   s.eleagentBaseURL,
		APIKey:    "eleagent_" + uuid.New().String(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}

// BalanceInsufficientError 余额不足错误，用于上层返回 402 Payment Required
type BalanceInsufficientError struct {
	Message string
}

func (e *BalanceInsufficientError) Error() string {
	return e.Message
}
