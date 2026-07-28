package service

import (
	"encoding/hex"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupKeyManagerService(t *testing.T) (*KeyManagerService, *repository.ApiKeyRepo) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.ProviderApiKey{})

	repo := repository.NewApiKeyRepo(db)
	masterKey := hex.EncodeToString(make([]byte, 32))
	svc, err := NewKeyManagerService(repo, masterKey)
	if err != nil {
		t.Fatal(err)
	}
	return svc, repo
}

func TestKeyManagerService_CreateAndSelect(t *testing.T) {
	svc, _ := setupKeyManagerService(t)

	_, err := svc.CreateKey("openai", "OpenAI-01", "", "sk-test-1", 0, 0)
	assert.NoError(t, err)

	selected, err := svc.SelectKey("openai")
	assert.NoError(t, err)
	assert.NotNil(t, selected)
	assert.Equal(t, "sk-test-1", selected.Plaintext)
	assert.Equal(t, "openai", selected.Key.Provider)
}

func TestKeyManagerService_SelectFallbackToError(t *testing.T) {
	svc, _ := setupKeyManagerService(t)

	_, err := svc.SelectKey("openai")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "无可用 API Key")
}

func TestKeyManagerService_NoEncryptionKey(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.ProviderApiKey{})
	repo := repository.NewApiKeyRepo(db)

	// 无 Master Key 时，SelectKey 返回错误（fallback 模式）
	svc, err := NewKeyManagerService(repo, "")
	assert.NoError(t, err)

	_, err = svc.CreateKey("openai", "OpenAI-01", "", "sk-test", 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未配置 ENCRYPTION_MASTER_KEY")
}

func TestKeyManagerService_DailyQuota(t *testing.T) {
	svc, _ := setupKeyManagerService(t)

	key, err := svc.CreateKey("openai", "OpenAI-Quota", "", "sk-test", 0, 100)
	assert.NoError(t, err)

	// 报告使用了 100 token
	err = svc.ReportSuccess(key.ID, 100)
	assert.NoError(t, err)

	// 此时应无可用 Key
	_, err = svc.SelectKey("openai")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "无可用 API Key")
}

func TestKeyManagerService_DisableKey(t *testing.T) {
	svc, _ := setupKeyManagerService(t)

	key, err := svc.CreateKey("openai", "OpenAI-01", "", "sk-test", 0, 0)
	assert.NoError(t, err)

	// 禁用 Key
	isEnabled := false
	priority := 0
	dailyQuota := int64(0)
	_, err = svc.UpdateKey(&model.ProviderApiKey{
		ID:         key.ID,
		Provider:   "openai",
		Name:       key.Name,
		IsEnabled:  isEnabled,
		Priority:   priority,
		DailyQuota: dailyQuota,
	})
	assert.NoError(t, err)

	_, err = svc.SelectKey("openai")
	assert.Error(t, err)
}

func TestKeyManagerService_RotateKey(t *testing.T) {
	svc, _ := setupKeyManagerService(t)

	key, err := svc.CreateKey("openai", "OpenAI-01", "", "old-key", 0, 0)
	assert.NoError(t, err)

	rotated, err := svc.RotateKey(key.ID, "new-key")
	assert.NoError(t, err)
	assert.Equal(t, key.ID, rotated.ID)

	selected, err := svc.SelectKey("openai")
	assert.NoError(t, err)
	assert.Equal(t, "new-key", selected.Plaintext)
}

// TestKeyManagerService_SelectKeyHeadroom 验证 headroom 策略优先选剩余配额多的 Key（AR-16 LLM-P1-6）。
func TestKeyManagerService_SelectKeyHeadroom(t *testing.T) {
	svc, _ := setupKeyManagerService(t)

	// k1: quota 500，从未使用（remaining 500, LastUsedAt nil）
	// k2: quota 2000，用掉 100（remaining 1900, LastUsedAt now）
	_, err := svc.CreateKey("openai", "OpenAI-01", "", "sk-1", 0, 500)
	require.NoError(t, err)
	k2, err := svc.CreateKey("openai", "OpenAI-02", "", "sk-2", 0, 2000)
	require.NoError(t, err)
	require.NoError(t, svc.ReportSuccess(k2.ID, 100)) // k2 用 100，remaining 1900

	// headroom：k2 剩余 2000 > k1 剩余 400 -> 选 k2
	sel, err := svc.SelectKeyWithStrategy("openai", StrategyHeadroom)
	require.NoError(t, err)
	assert.Equal(t, "sk-2", sel.Plaintext, "headroom 应选剩余配额多的 k2")

	// 默认 round_robin：k1 LastUsedAt 更早（先用过），轮询选 LastUsedAt 最早 -> 选 k1
	selRR, err := svc.SelectKey("openai")
	require.NoError(t, err)
	assert.Equal(t, "sk-1", selRR.Plaintext, "round_robin 应选 LastUsedAt 最早的 k1")
}

// TestKeyManagerService_SelectKeyHeadroomUnlimited 验证 DailyQuota=0（无限配额）在 headroom 下优先级最高。
func TestKeyManagerService_SelectKeyHeadroomUnlimited(t *testing.T) {
	svc, _ := setupKeyManagerService(t)

	// k1: 无配额限制（DailyQuota=0 -> 剩余 MaxInt64），已用 5000
	// k2: quota 2000，从未使用（remaining 2000）
	k1, err := svc.CreateKey("openai", "OpenAI-01", "", "sk-1", 0, 0)
	require.NoError(t, err)
	_, err = svc.CreateKey("openai", "OpenAI-02", "", "sk-2", 0, 2000)
	require.NoError(t, err)
	require.NoError(t, svc.ReportSuccess(k1.ID, 5000)) // k1 用 5000，但无限配额

	// headroom：k1 无限配额优先 -> 选 k1（即便已用更多）
	sel, err := svc.SelectKeyWithStrategy("openai", StrategyHeadroom)
	require.NoError(t, err)
	assert.Equal(t, "sk-1", sel.Plaintext, "headroom 应优先选无限配额的 k1")
}
