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

// TestBillingService_DeductWithPerGeneration 验证按次附加费与 token 费用相加。
func TestBillingService_DeductWithPerGeneration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.User{}, &model.BalanceTransaction{}, &model.TokenUsage{})

	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	eleAgentModelService := NewTestEleAgentModelServiceWithConfigs([]*model.EleAgentModelConfig{
		{
			Provider:           "test",
			Protocol:           model.EleAgentUpstreamOpenAICompatible,
			ModelName:          "pergen",
			BaseURL:            "https://api.example.com/v1",
			IsEnabled:          true,
			InputPricePerCall:  5000,
			PricePerCall:       10000,
			PricePerGeneration: 100,
		},
		{
			Provider:           "test",
			Protocol:           model.EleAgentUpstreamOpenAICompatible,
			ModelName:          "pergen-only",
			BaseURL:            "https://api.example.com/v1",
			IsEnabled:          true,
			PricePerGeneration: 50,
		},
	})
	svc := NewBillingService(userRepo, billRepo, eleAgentModelService, nil, nil)

	user := &model.User{
		ID:       "user-pergen",
		Username: "pergen@example.com",
		Balance:  10000,
		Role:     model.UserRoleUser,
	}
	db.Create(user)

	// 输入 1000 tokens + 输出 500 tokens + 按次附加费 100
	// cost = 1000*5000/1M + 500*10000/1M + 100 = 5 + 5 + 100 = 110
	usage := &llm.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}
	err = svc.Deduct("user-pergen", "eleagent", "test/pergen", CurrencyDanwan, usage)
	assert.NoError(t, err)

	var updated model.User
	db.First(&updated, "id = ?", "user-pergen")
	assert.Equal(t, int64(10000-110), updated.Balance)

	// 仅有按次附加费、无 token 单价时，按次费直接扣除
	user2 := &model.User{
		ID:       "user-pergen-only",
		Username: "pergen-only@example.com",
		Balance:  100,
		Role:     model.UserRoleUser,
	}
	db.Create(user2)
	err = svc.Deduct("user-pergen-only", "eleagent", "test/pergen-only", CurrencyDanwan, &llm.Usage{
		PromptTokens:     100,
		CompletionTokens: 100,
		TotalTokens:      200,
	})
	assert.NoError(t, err)

	var updated2 model.User
	db.First(&updated2, "id = ?", "user-pergen-only")
	assert.Equal(t, int64(50), updated2.Balance)
}

// TestBillingService_CheckBalanceWithPerGeneration 验证按次附加费参与余额校验。
func TestBillingService_CheckBalanceWithPerGeneration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.User{})

	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	eleAgentModelService := NewTestEleAgentModelServiceWithConfigs([]*model.EleAgentModelConfig{
		{
			Provider:           "test",
			Protocol:           model.EleAgentUpstreamOpenAICompatible,
			ModelName:          "pergen-balance",
			BaseURL:            "https://api.example.com/v1",
			IsEnabled:          true,
			PricePerGeneration: 200,
		},
	})
	svc := NewBillingService(userRepo, billRepo, eleAgentModelService, nil, nil)

	// 余额为正但小于按次附加费，仍应放行（预校验只要求至少 max(1, perGen)）
	user := &model.User{
		ID:       "user-pg-balance",
		Username: "pg-balance@example.com",
		Balance:  200,
		Role:     model.UserRoleUser,
	}
	db.Create(user)
	err = svc.CheckBalance("user-pg-balance", "eleagent", "test/pergen-balance", CurrencyDanwan)
	assert.NoError(t, err)

	// 余额为负且模型有按次附加费 -> 拒绝
	negativeUser := &model.User{
		ID:       "user-pg-negative",
		Username: "pg-negative@example.com",
		Balance:  -1,
		Role:     model.UserRoleUser,
	}
	db.Create(negativeUser)
	err = svc.CheckBalance("user-pg-negative", "eleagent", "test/pergen-balance", CurrencyDanwan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "余额不足")
}
