package service

import (
	"encoding/hex"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/stretchr/testify/assert"
	sqlite "github.com/glebarez/sqlite"
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
