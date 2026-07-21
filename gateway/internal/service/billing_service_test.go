package service

import (
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/stretchr/testify/assert"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// testEleAgentPricePerMillion 测试用 Ele Agent 单价：弹丸 / 1M tokens
const testEleAgentPricePerMillion = int64(10000)

func setupBillingService(t *testing.T) (*BillingService, *gorm.DB) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.User{}, &model.BalanceTransaction{}, &model.TokenUsage{})

	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	eleAgentModelService := NewTestEleAgentModelServiceWithConfigs([]*model.EleAgentModelConfig{
		{Provider: "qwen", Protocol: model.EleAgentUpstreamOpenAICompatible, ModelName: "Qwen/Qwen3-8B", BaseURL: "https://api.siliconflow.cn/v1", IsEnabled: true, PricePerCall: testEleAgentPricePerMillion},
		{Provider: "deepseek", Protocol: model.EleAgentUpstreamOpenAICompatible, ModelName: "deepseek-chat", BaseURL: "https://api.deepseek.com/v1", IsEnabled: true, PricePerCall: testEleAgentPricePerMillion},
	})
	billingService := NewBillingService(userRepo, billRepo, eleAgentModelService, nil, nil)

	return billingService, db
}

func TestBillingService_Deduct(t *testing.T) {
	svc, db := setupBillingService(t)

	// 创建用户并充值
	user := &model.User{
		ID:      "user-001",
		Username:   "test@example.com",
		Balance: 10000,
		Role:    model.UserRoleUser,
	}
	db.Create(user)

	// 扣费：Ele Agent 代理 deepseek/deepseek-chat，输出单价 10000 弹丸/M tokens
	usage := &llm.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}
	err := svc.Deduct("user-001", "eleagent", "deepseek/deepseek-chat", CurrencyDanwan, usage)
	assert.NoError(t, err)

	// 验证余额减少：仅输出 token 计费，500 * 10000 / 1_000_000 = 5
	var updated model.User
	db.First(&updated, "id = ?", "user-001")
	assert.Equal(t, int64(10000-5), updated.Balance)
}

func TestBillingService_DeductElegant(t *testing.T) {
	svc, db := setupBillingService(t)

	user := &model.User{
		ID:             "user-001-elegant",
		Username:          "elegant@example.com",
		Balance:        0,
		ElegantBalance: 10000,
		Role:           model.UserRoleUser,
	}
	db.Create(user)

	usage := &llm.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}
	err := svc.Deduct("user-001-elegant", "eleagent", "qwen/Qwen/Qwen3-8B", CurrencyElegant, usage)
	assert.NoError(t, err)

	var updated model.User
	db.First(&updated, "id = ?", "user-001-elegant")
	// completionTokens=500, outputPrice=10000 => 500*10000/1_000_000 = 5
	assert.Equal(t, int64(10000-5), updated.ElegantBalance)
	assert.Equal(t, int64(0), updated.Balance)
}

func TestBillingService_DeductInsufficientBalance(t *testing.T) {
	svc, db := setupBillingService(t)

	// 创建余额为 0 的用户
	user := &model.User{
		ID:      "user-002",
		Username:   "poor@example.com",
		Balance: 0,
		Role:    model.UserRoleUser,
	}
	db.Create(user)

	usage := &llm.Usage{
		PromptTokens:     1000,
		CompletionTokens: 1000,
		TotalTokens:      2000,
	}
	// 非 Ele Agent 不扣费，不会余额不足；改用 Ele Agent 触发余额不足
	err := svc.Deduct("user-002", "eleagent", "deepseek/deepseek-chat", CurrencyDanwan, usage)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "余额不足")
}

func TestBillingService_GetBalance(t *testing.T) {
	svc, db := setupBillingService(t)

	user := &model.User{
		ID:             "user-003",
		Username:          "rich@example.com",
		Balance:        50000,
		ElegantBalance: 20000,
		Role:           model.UserRoleUser,
	}
	db.Create(user)

	balance, err := svc.GetBalance("user-003")
	assert.NoError(t, err)
	assert.Equal(t, int64(50000), balance.Danwan)
	assert.Equal(t, int64(20000), balance.Elegant)
}

func TestBillingService_NonEleAgentFree(t *testing.T) {
	svc, db := setupBillingService(t)

	user := &model.User{
		ID:      "user-004",
		Username:   "direct@example.com",
		Balance: 10000,
		Role:    model.UserRoleUser,
	}
	db.Create(user)

	// 用户使用自带 API Key 的直连模型，不应扣平台弹丸
	usage := &llm.Usage{
		PromptTokens:     2000,
		CompletionTokens: 1000,
		TotalTokens:      3000,
	}
	err := svc.Deduct("user-004", "deepseek", "chat", CurrencyDanwan, usage)
	assert.NoError(t, err)

	var updated model.User
	db.First(&updated, "id = ?", "user-004")
	assert.Equal(t, int64(10000), updated.Balance)
}

func TestBillingService_EleAgentFreeWhenPriceZero(t *testing.T) {
	svc, db := setupBillingService(t)

	user := &model.User{
		ID:      "user-005",
		Username:   "free@example.com",
		Balance: 100,
		Role:    model.UserRoleUser,
	}
	db.Create(user)

	// Ele Agent 模型未配置或输入/输出单价均为 0 时免费
	usage := &llm.Usage{
		PromptTokens:     2000,
		CompletionTokens: 1000,
		TotalTokens:      3000,
	}
	err := svc.Deduct("user-005", "eleagent", "unknown-model", CurrencyDanwan, usage)
	assert.NoError(t, err)

	var updated model.User
	db.First(&updated, "id = ?", "user-005")
	assert.Equal(t, int64(100), updated.Balance)
}

// TestBillingService_CheckBalance 校验 Ele Agent 付费模型调用前余额检查
func TestBillingService_CheckBalance(t *testing.T) {
	svc, db := setupBillingService(t)

	// 余额为负的用户
	negativeUser := &model.User{
		ID:       "user-negative",
		Username: "negative@example.com",
		Balance:  -100,
		Role:     model.UserRoleUser,
	}
	db.Create(negativeUser)

	// 付费模型（输出单价 > 0）且余额为负 -> 拒绝
	err := svc.CheckBalance("user-negative", "eleagent", "deepseek/deepseek-chat", CurrencyDanwan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "余额不足")

	// 免费模型（单价 = 0 或未配置）-> 放行，即使余额为负
	err = svc.CheckBalance("user-negative", "eleagent", "unknown-model", CurrencyDanwan)
	assert.NoError(t, err)

	// 直连模型 -> 放行
	err = svc.CheckBalance("user-negative", "openai", "gpt-4o", CurrencyDanwan)
	assert.NoError(t, err)

	// 余额为正的用户可正常调用付费模型
	positiveUser := &model.User{
		ID:       "user-positive",
		Username: "positive@example.com",
		Balance:  1000,
		Role:     model.UserRoleUser,
	}
	db.Create(positiveUser)
	err = svc.CheckBalance("user-positive", "eleagent", "deepseek/deepseek-chat", CurrencyDanwan)
	assert.NoError(t, err)
}
