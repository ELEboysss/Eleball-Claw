package service

import (
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/stretchr/testify/assert"
)

// TestBillingService_DeductWithEstimatedUsage 验证使用兜底估算的 usage 时，
// 扣费公式与“不足 1M 按 1 弹丸”兜底逻辑正确。
func TestBillingService_DeductWithEstimatedUsage(t *testing.T) {
	svc, db := setupBillingService(t)

	user := &model.User{
		ID:       "user-est-001",
		Username: "est@example.com",
		Balance:  100,
		Role:     model.UserRoleUser,
	}
	db.Create(user)

	// 估算出的用量通常较小（< 1M tokens），应触发最小扣费 1 弹丸
	usage := llm.EstimateUsageFromMessages([]llm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "你好"},
	}, "你好！有什么可以帮你的吗？")

	err := svc.Deduct("user-est-001", "eleagent", "deepseek/deepseek-chat", CurrencyDanwan, usage)
	assert.NoError(t, err)

	var updated model.User
	db.First(&updated, "id = ?", "user-est-001")
	assert.Equal(t, int64(99), updated.Balance)

	// 继续扣费，验证多次小额调用均按 1 弹丸扣除
	err = svc.Deduct("user-est-001", "eleagent", "deepseek/deepseek-chat", CurrencyDanwan, usage)
	assert.NoError(t, err)
	db.First(&updated, "id = ?", "user-est-001")
	assert.Equal(t, int64(98), updated.Balance)
}

// TestBillingService_DeductLargeEstimatedUsage 验证较大估算用量下的精确公式。
func TestBillingService_DeductLargeEstimatedUsage(t *testing.T) {
	svc, db := setupBillingService(t)

	user := &model.User{
		ID:       "user-est-002",
		Username: "est2@example.com",
		Balance:  100000,
		Role:     model.UserRoleUser,
	}
	db.Create(user)

	// 构造一个较大的用量：输入 1000000 + 输出 500000，输出单价 10000 弹丸/M
	// cost = 500000 * 10000 / 1000000 = 5000
	usage := &llm.Usage{
		PromptTokens:     1000000,
		CompletionTokens: 500000,
		TotalTokens:      1500000,
	}

	err := svc.Deduct("user-est-002", "eleagent", "deepseek/deepseek-chat", CurrencyDanwan, usage)
	assert.NoError(t, err)

	var updated model.User
	db.First(&updated, "id = ?", "user-est-002")
	assert.Equal(t, int64(95000), updated.Balance)
}
