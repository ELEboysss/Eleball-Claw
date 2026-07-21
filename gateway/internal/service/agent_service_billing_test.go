package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupAgentServiceWithBilling 创建带计费能力的 AgentService 测试环境
func setupAgentServiceWithBilling(t *testing.T) (*AgentService, *repository.UserRepo, *gorm.DB) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.ChatConversation{}, &model.ChatMessage{}, &model.AgentSession{}, &model.AgentSessionOutput{}, &model.VIPPlan{}, &model.VIPSubscription{}, &model.TokenUsage{}, &model.BalanceTransaction{}))

	userRepo := repository.NewUserRepo(db)
	require.NoError(t, userRepo.Create(&model.User{ID: "u3", Username: "user3", Role: model.UserRoleUser, Status: 1, Balance: 10000}))

	convRepo := repository.NewChatConversationRepo(db)
	vipService := newTestVIPService(db)

	// 开通 VIP1 以获取 Agent 模式权限，折扣 100% 便于计算
	plan := createVIPPlan(t, vipService, 1, "强力弹丸", 4900, 100)
	expiresAt := time.Now().Add(30 * 24 * time.Hour)
	require.NoError(t, db.Create(&model.VIPSubscription{
		UserID:       "u3",
		PlanID:       plan.ID,
		Level:        plan.Level,
		PriceFen:     plan.PriceFen,
		DurationDays: plan.DurationDays,
		StartedAt:    time.Now(),
		ExpiresAt:    expiresAt,
		Status:       "active",
	}).Error)
	require.NoError(t, userRepo.UpdateVIP("u3", plan.Level, expiresAt, &plan.ID))

	convSvc := NewConversationService(convRepo, vipService, t.TempDir())
	sessionRepo := repository.NewAgentSessionRepo(db)
	billingRepo := repository.NewBillingRepo(db)

	// 配置模型单价：输入 1000 弹丸/1M，输出 2000 弹丸/1M
	eleAgentModelService := NewTestEleAgentModelServiceWithConfigs([]*model.EleAgentModelConfig{
		{
			ID:                "model-1",
			Provider:          "qwen",
			Protocol:          model.EleAgentUpstreamOpenAICompatible,
			ModelName:         "Qwen/Qwen3-8B",
			BaseURL:           "https://example.com",
			EncryptedKey:      "key",
			Nonce:             "nonce",
			IsEnabled:         true,
			SupportsTools:     true,
			InputPricePerCall: 1000,
			PricePerCall:      2000,
		},
	})
	billingService := NewBillingService(userRepo, billingRepo, eleAgentModelService, nil, vipService)

	mockClient := &mockAgentLLM{}
	resolver := func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return mockClient, nil
	}
	agentSvc := NewAgentService(convSvc, sessionRepo, userRepo, vipService, billingService, eleAgentModelService, NewFileSandbox(t.TempDir(), ""), NewToolRegistry(), NewToolSchemaBuilder(NewToolRegistry()), NewAgentTrigger(), resolver, "", 10, nil)
	return agentSvc, userRepo, db
}

func TestAgentService_Execute_Billing_DirectAnswer(t *testing.T) {
	agentSvc, userRepo, db := setupAgentServiceWithBilling(t)


	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{
				Delta: "hello",
				Usage: &llm.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500},
			},
		},
	}
	agentSvc.clientResolver = func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return client, nil
	}

	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u3")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "hi", Model: "qwen/Qwen/Qwen3-8B", Provider: "eleagent", EnableTools: boolPtr(true)}, &buf)
	require.NoError(t, err)

	// 费用 = 1000*1000/1e6 + 500*2000/1e6 = 1 + 1 = 2 弹丸
	user, err := userRepo.GetByID("u3")
	require.NoError(t, err)

	// 验证 TokenUsage 与余额流水已记录
	var count int64
	require.NoError(t, db.Model(&model.TokenUsage{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	require.NoError(t, db.Model(&model.BalanceTransaction{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	var tx model.BalanceTransaction
	require.NoError(t, db.First(&tx).Error)
	assert.Equal(t, int64(-2), tx.Amount)
	var tu model.TokenUsage
	require.NoError(t, db.First(&tu).Error)
	assert.Equal(t, int64(2), tu.CostAmount)
	assert.Equal(t, "eleagent", tu.Provider)
	assert.Equal(t, "qwen/Qwen/Qwen3-8B", tu.ModelID)
	assert.Equal(t, int64(9998), user.Balance)
}

func TestAgentService_Execute_Billing_InsufficientBalance(t *testing.T) {
	agentSvc, _, _ := setupAgentServiceWithBilling(t)

	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{Delta: "hello", Usage: &llm.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}},
		},
	}
	agentSvc.clientResolver = func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return client, nil
	}

	// 将余额清零
	require.NoError(t, agentSvc.userRepo.UpdateBalance("u3", -10000))

	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u3")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "hi", Model: "qwen/Qwen/Qwen3-8B", Provider: "eleagent", EnableTools: boolPtr(true)}, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "弹丸余额不足")
}
