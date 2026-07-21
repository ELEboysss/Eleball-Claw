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

// TestBillingService_DeductSplitPricing 验证区分输入/输出单价后的计费公式。
func TestBillingService_DeductSplitPricing(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.User{}, &model.BalanceTransaction{}, &model.TokenUsage{})

	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	eleAgentModelService := NewTestEleAgentModelServiceWithConfigs([]*model.EleAgentModelConfig{
		{
			Provider:          "test",
			Protocol:          model.EleAgentUpstreamOpenAICompatible,
			ModelName:         "split",
			BaseURL:           "https://api.example.com/v1",
			IsEnabled:         true,
			InputPricePerCall: 5000,
			PricePerCall:      10000,
		},
	})
	svc := NewBillingService(userRepo, billRepo, eleAgentModelService, nil, nil)

	user := &model.User{
		ID:       "user-split",
		Username: "split@example.com",
		Balance:  10000,
		Role:     model.UserRoleUser,
	}
	db.Create(user)

	// 输入 1000 tokens + 输出 500 tokens
	// cost = 1000*5000/1M + 500*10000/1M = 5 + 5 = 10
	usage := &llm.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}
	err = svc.Deduct("user-split", "eleagent", "test/split", CurrencyDanwan, usage)
	assert.NoError(t, err)

	var updated model.User
	db.First(&updated, "id = ?", "user-split")
	assert.Equal(t, int64(9990), updated.Balance)
}
