package service

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/crypto"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// KeyManagerService 后端 LLM API Key 管理器
// 负责加密存储、内存缓存、调度选择、配额统计。
type KeyManagerService struct {
	repo    *repository.ApiKeyRepo
	encrypt *crypto.KeyEncryption

	mu   sync.RWMutex
	keys map[string][]*model.ProviderApiKey // provider -> keys
}

// NewNoOpKeyManager 创建一个不管理任何 Key 的空管理器，用于测试或纯 fallback 模式。
func NewNoOpKeyManager() *KeyManagerService {
	return &KeyManagerService{
		repo: nil,
		keys: make(map[string][]*model.ProviderApiKey),
	}
}

// NewKeyManagerService 创建 Key 管理器
// masterKeyHex: 64 字符十六进制 AES-256 主密钥；若为空且 DB 中无 Key，则允许创建无加密能力的服务（仅 fallback 模式）。
func NewKeyManagerService(repo *repository.ApiKeyRepo, masterKeyHex string) (*KeyManagerService, error) {
	if repo == nil {
		return NewNoOpKeyManager(), nil
	}
	svc := &KeyManagerService{
		repo: repo,
		keys: make(map[string][]*model.ProviderApiKey),
	}

	if masterKeyHex != "" {
		ke, err := crypto.NewKeyEncryption(masterKeyHex)
		if err != nil {
			return nil, fmt.Errorf("初始化 KeyEncryption 失败: %w", err)
		}
		svc.encrypt = ke
	}

	// 启动时加载一次
	if err := svc.reload(); err != nil {
		return nil, fmt.Errorf("加载 API Key 失败: %w", err)
	}

	// 后台定时刷新（每 30 秒）
	go svc.backgroundReload()

	return svc, nil
}

// SelectedKey 选中的 Key 及解密后的明文
type SelectedKey struct {
	Key       *model.ProviderApiKey
	Plaintext string
}

// SelectKey 为指定 Provider 选择一个可用 Key
// 算法：过滤未启用/超配额 → 按 Priority 升序 → 按 LastUsedAt 升序（轮询）
func (s *KeyManagerService) SelectKey(provider string) (*SelectedKey, error) {
	s.mu.RLock()
	keys := s.keys[provider]
	s.mu.RUnlock()

	candidates := make([]*model.ProviderApiKey, 0, len(keys))
	for _, k := range keys {
		if !k.IsEnabled {
			continue
		}
		if k.DailyQuota > 0 && k.UsedTokens >= k.DailyQuota {
			continue
		}
		candidates = append(candidates, k)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("provider %s 无可用 API Key", provider)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		// LastUsedAt 为空表示从未使用，优先使用
		if candidates[i].LastUsedAt == nil && candidates[j].LastUsedAt != nil {
			return true
		}
		if candidates[i].LastUsedAt != nil && candidates[j].LastUsedAt == nil {
			return false
		}
		if candidates[i].LastUsedAt == nil && candidates[j].LastUsedAt == nil {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		return candidates[i].LastUsedAt.Before(*candidates[j].LastUsedAt)
	})

	selected := candidates[0]
	plaintext, err := s.decryptKey(selected)
	if err != nil {
		return nil, fmt.Errorf("解密 Key 失败: %w", err)
	}

	return &SelectedKey{Key: selected, Plaintext: plaintext}, nil
}

// ReportSuccess 报告调用成功，更新用量和最近使用时间
func (s *KeyManagerService) ReportSuccess(keyID string, usedTokens int64) error {
	now := time.Now()
	updates := map[string]interface{}{
		"used_tokens":   gorm.Expr("used_tokens + ?", usedTokens),
		"failure_count": 0,
		"last_error":    "",
		"last_used_at":  now,
	}
	if err := s.repo.UpdateFields(keyID, updates); err != nil {
		return err
	}
	// 立即刷新内存缓存，保证后续 SelectKey 读到最新用量
	return s.reload()
}

// ReportFailure 报告调用失败，增加失败计数
func (s *KeyManagerService) ReportFailure(keyID string, errMsg string) error {
	updates := map[string]interface{}{
		"failure_count": gorm.Expr("failure_count + 1"),
		"last_error":    errMsg,
	}
	if err := s.repo.UpdateFields(keyID, updates); err != nil {
		return err
	}
	return s.reload()
}

// CreateKey 新增一个 Key（自动加密）
func (s *KeyManagerService) CreateKey(provider, name, baseURL, plaintext string, priority int, dailyQuota int64) (*model.ApiKeyListItem, error) {
	if s.encrypt == nil {
		return nil, errors.New("未配置 ENCRYPTION_MASTER_KEY，无法加密存储 API Key")
	}

	ciphertext, nonce, version, err := s.encrypt.Encrypt(plaintext)
	if err != nil {
		return nil, fmt.Errorf("加密 Key 失败: %w", err)
	}

	key := &model.ProviderApiKey{
		ID:           uuid.New().String(),
		Provider:     provider,
		Name:         name,
		BaseURL:      baseURL,
		EncryptedKey: ciphertext,
		Nonce:        nonce,
		KeyVersion:   version,
		IsEnabled:    true,
		Priority:     priority,
		DailyQuota:   dailyQuota,
	}

	if err := s.repo.Create(key); err != nil {
		return nil, err
	}

	_ = s.reload()
	return toListItem(key), nil
}

// UpdateKey 更新 Key 元信息（不含明文；如需更新明文，调用 RotateKey）
func (s *KeyManagerService) UpdateKey(key *model.ProviderApiKey) (*model.ApiKeyListItem, error) {
	// 使用 map 更新，避免 GORM Updates 跳过 bool 零值
	updates := map[string]interface{}{
		"name":        key.Name,
		"base_url":    key.BaseURL,
		"is_enabled":  key.IsEnabled,
		"priority":    key.Priority,
		"daily_quota": key.DailyQuota,
	}
	if err := s.repo.UpdateFields(key.ID, updates); err != nil {
		return nil, err
	}
	_ = s.reload()
	updated, err := s.repo.GetByID(key.ID)
	if err != nil {
		return nil, err
	}
	return toListItem(updated), nil
}

// RotateKey 轮换 Key 明文
func (s *KeyManagerService) RotateKey(id, newPlaintext string) (*model.ApiKeyListItem, error) {
	if s.encrypt == nil {
		return nil, errors.New("未配置 ENCRYPTION_MASTER_KEY，无法加密存储 API Key")
	}

	key, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	ciphertext, nonce, version, err := s.encrypt.Encrypt(newPlaintext)
	if err != nil {
		return nil, fmt.Errorf("加密新 Key 失败: %w", err)
	}

	key.EncryptedKey = ciphertext
	key.Nonce = nonce
	key.KeyVersion = version
	key.FailureCount = 0
	key.LastError = ""

	if err := s.repo.Update(key); err != nil {
		return nil, err
	}
	_ = s.reload()
	return toListItem(key), nil
}

// DeleteKey 删除 Key
func (s *KeyManagerService) DeleteKey(id string) error {
	if err := s.repo.Delete(id); err != nil {
		return err
	}
	return s.reload()
}

// GetKey 获取单个 Key（不含明文）
func (s *KeyManagerService) GetKey(id string) (*model.ApiKeyListItem, error) {
	key, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	return toListItem(key), nil
}

// ListKeys 列表查询
func (s *KeyManagerService) ListKeys(provider string, page, pageSize int) ([]*model.ApiKeyListItem, int64, error) {
	items, total, err := s.repo.List(provider, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	result := make([]*model.ApiKeyListItem, len(items))
	for i, item := range items {
		result[i] = toListItem(item)
	}
	return result, total, nil
}

// ProviderStatus 各 Provider 统计
func (s *KeyManagerService) ProviderStatus() ([]*model.ProviderStatus, error) {
	return s.repo.ProviderStatus()
}

// ResetDailyQuota 重置所有 Key 日配额
func (s *KeyManagerService) ResetDailyQuota() error {
	return s.repo.ResetDailyQuota()
}

// HasAnyKey 判断某 Provider 是否有可用数据库 Key
func (s *KeyManagerService) HasAnyKey(provider string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.keys[provider]) > 0
}

// decryptKey 解密单个 Key
func (s *KeyManagerService) decryptKey(key *model.ProviderApiKey) (string, error) {
	if s.encrypt == nil {
		return "", errors.New("未配置 ENCRYPTION_MASTER_KEY，无法解密 API Key")
	}
	return s.encrypt.Decrypt(key.EncryptedKey, key.Nonce)
}

// reload 从数据库重新加载 Key 到内存
func (s *KeyManagerService) reload() error {
	items, _, err := s.repo.List("", 1, 10000)
	if err != nil {
		return err
	}

	newKeys := make(map[string][]*model.ProviderApiKey)
	for _, k := range items {
		newKeys[k.Provider] = append(newKeys[k.Provider], k)
	}

	s.mu.Lock()
	s.keys = newKeys
	s.mu.Unlock()
	return nil
}

// backgroundReload 后台定时刷新
func (s *KeyManagerService) backgroundReload() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		_ = s.reload()
	}
}

// toListItem 转换为列表项（脱敏）
func toListItem(key *model.ProviderApiKey) *model.ApiKeyListItem {
	return &model.ApiKeyListItem{
		ID:           key.ID,
		Provider:     key.Provider,
		Name:         key.Name,
		BaseURL:      key.BaseURL,
		KeyVersion:   key.KeyVersion,
		IsEnabled:    key.IsEnabled,
		Priority:     key.Priority,
		DailyQuota:   key.DailyQuota,
		UsedTokens:   key.UsedTokens,
		FailureCount: key.FailureCount,
		LastError:    key.LastError,
		LastUsedAt:   key.LastUsedAt,
		CreatedAt:    key.CreatedAt,
		UpdatedAt:    key.UpdatedAt,
	}
}
