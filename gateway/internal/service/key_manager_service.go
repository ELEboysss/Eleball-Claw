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
	"github.com/eleball/gateway/pkg/llm"
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
	now := time.Now()
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
		// AR-04：冷却未过期的 Key 跳过（过期自动重新纳入候选）
		if k.RateLimitedUntil != nil && k.RateLimitedUntil.After(now) {
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
// AR-04：成功后清除冷却与退避级别，重置连续失败计数。
func (s *KeyManagerService) ReportSuccess(keyID string, usedTokens int64) error {
	now := time.Now()
	updates := map[string]interface{}{
		"used_tokens":         gorm.Expr("used_tokens + ?", usedTokens),
		"failure_count":       0,
		"last_error":          "",
		"last_error_type":     "",
		"backoff_level":       0,
		"rate_limited_until":  nil,
		"last_used_at":        now,
	}
	if err := s.repo.UpdateFields(keyID, updates); err != nil {
		return err
	}
	// 立即刷新内存缓存，保证后续 SelectKey 读到最新用量
	return s.reload()
}

// ReportFailure 报告调用失败（向后兼容入口：按错误消息字符串粗分类）。
// 优先使用 ReportFailureWithErr 传入原始 error，可拿到 HTTP 状态码精确分类。
func (s *KeyManagerService) ReportFailure(keyID string, errMsg string) error {
	return s.ReportFailureWithErr(keyID, errors.New(errMsg))
}

// ReportFailureWithErr 报告调用失败并按错误类型设置冷却/退避/熔断（AR-04）。
// 参考 providers/OmniRoute 三层 resilience：
//   - rate_limit (429)：指数退避冷却，2s * 2^level，上限 5min
//   - server_error (5xx)：1s * 2^level，上限 2min
//   - network：500ms * 2^level，上限 1min
//   - auth (401/403)：不冷却（Key 无效，重试无意义，靠连续失败阈值熔断）
//   - 其他 4xx 语义错误：不冷却、不计退避（不应惩罚 Key）
//
// 连续失败达到 banThreshold（5 次）自动禁用该 Key（Circuit Breaker OPEN）。
func (s *KeyManagerService) ReportFailureWithErr(keyID string, err error) error {
	errType, baseCooldown, maxCooldown := classifyKeyError(err)

	s.mu.RLock()
	prev := s.cachedKey(keyID)
	s.mu.RUnlock()
	prevLevel := 0
	prevFailures := 0
	if prev != nil {
		prevLevel = prev.BackoffLevel
		prevFailures = prev.FailureCount
	}

	updates := map[string]interface{}{
		"failure_count":   gorm.Expr("failure_count + 1"),
		"last_error":      err.Error(),
		"last_error_type": errType,
	}
	// 可冷却的错误类型才推进退避级别与冷却截止时间
	if baseCooldown > 0 {
		newLevel := prevLevel + 1
		cooldown := exponentialCooldown(baseCooldown, newLevel, maxCooldown)
		until := time.Now().Add(cooldown)
		updates["backoff_level"] = newLevel
		updates["rate_limited_until"] = until
	}

	// 连续失败达阈值：熔断，禁用 Key（success 会重置 failure_count，故"连续"语义成立）
	const banThreshold = 5
	if prevFailures+1 >= banThreshold {
		updates["is_enabled"] = false
	}

	if err := s.repo.UpdateFields(keyID, updates); err != nil {
		return err
	}
	return s.reload()
}

// cachedKey 从内存缓存按 ID 查 Key（调用方持读锁）。
func (s *KeyManagerService) cachedKey(keyID string) *model.ProviderApiKey {
	for _, keys := range s.keys {
		for _, k := range keys {
			if k.ID == keyID {
				return k
			}
		}
	}
	return nil
}

// classifyKeyError 按上游错误类型返回 (errorType, baseCooldown, maxCooldown)。
// baseCooldown=0 表示不冷却（auth 或不可重试的 4xx 语义错误）。
// 实际冷却时长 = min(base * 2^newLevel, max)，由 ReportFailureWithErr 计算。
func classifyKeyError(err error) (string, time.Duration, time.Duration) {
	if err == nil {
		return "unknown", 0, 0
	}
	code := llm.UpstreamStatusCode(err)
	if code != 0 {
		switch {
		case code == 429:
			return "rate_limit", 2 * time.Second, 5 * time.Minute
		case code >= 500:
			return "server_error", 1 * time.Second, 2 * time.Minute
		case code == 401 || code == 403:
			return "auth", 0, 0
		default:
			return "client_error", 0, 0
		}
	}
	if llm.IsRetryableUpstreamErr(err) {
		return "network", 500 * time.Millisecond, 1 * time.Minute
	}
	return "unknown", 0, 0
}

// backoffCapShift 限制 2^level 的最大位移，避免溢出。
const backoffCapShift = 6 // 2^6 = 64 倍上限

// exponentialCooldown 计算 base * 2^level（封顶 max）。
func exponentialCooldown(base time.Duration, level int, max time.Duration) time.Duration {
	if level < 0 {
		level = 0
	}
	if level > backoffCapShift {
		level = backoffCapShift
	}
	d := base * time.Duration(1<<level)
	if d > max {
		return max
	}
	return d
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
		ID:               key.ID,
		Provider:         key.Provider,
		Name:             key.Name,
		BaseURL:          key.BaseURL,
		KeyVersion:       key.KeyVersion,
		IsEnabled:        key.IsEnabled,
		Priority:         key.Priority,
		DailyQuota:       key.DailyQuota,
		UsedTokens:       key.UsedTokens,
		FailureCount:     key.FailureCount,
		LastError:        key.LastError,
		LastUsedAt:       key.LastUsedAt,
		RateLimitedUntil: key.RateLimitedUntil,
		BackoffLevel:     key.BackoffLevel,
		LastErrorType:    key.LastErrorType,
		CreatedAt:        key.CreatedAt,
		UpdatedAt:        key.UpdatedAt,
	}
}
