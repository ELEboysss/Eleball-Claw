package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// ApiKeyRepo API Key 数据访问
type ApiKeyRepo struct {
	db *gorm.DB
}

// NewApiKeyRepo 创建仓库
func NewApiKeyRepo(db *gorm.DB) *ApiKeyRepo {
	return &ApiKeyRepo{db: db}
}

// Create 创建 Key
func (r *ApiKeyRepo) Create(key *model.ProviderApiKey) error {
	return r.db.Create(key).Error
}

// GetByID 根据 ID 获取
func (r *ApiKeyRepo) GetByID(id string) (*model.ProviderApiKey, error) {
	var key model.ProviderApiKey
	if err := r.db.First(&key, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &key, nil
}

// Update 更新（非零值字段）
func (r *ApiKeyRepo) Update(key *model.ProviderApiKey) error {
	return r.db.Model(&model.ProviderApiKey{}).Where("id = ?", key.ID).Updates(key).Error
}

// Delete 删除
func (r *ApiKeyRepo) Delete(id string) error {
	return r.db.Delete(&model.ProviderApiKey{}, "id = ?", id).Error
}

// List 列表查询（支持 provider 过滤、分页）
func (r *ApiKeyRepo) List(provider string, page, pageSize int) ([]*model.ProviderApiKey, int64, error) {
	var items []*model.ProviderApiKey
	var total int64

	query := r.db.Model(&model.ProviderApiKey{})
	if provider != "" {
		query = query.Where("provider = ?", provider)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("priority ASC, created_at ASC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// ListEnabledByProvider 获取指定 Provider 所有启用的 Key
func (r *ApiKeyRepo) ListEnabledByProvider(provider string) ([]*model.ProviderApiKey, error) {
	var items []*model.ProviderApiKey
	err := r.db.Where("provider = ? AND is_enabled = ?", provider, true).
		Order("priority ASC, created_at ASC").
		Find(&items).Error
	return items, err
}

// ResetDailyQuota 重置所有 Key 的日配额用量
func (r *ApiKeyRepo) ResetDailyQuota() error {
	return r.db.Model(&model.ProviderApiKey{}).UpdateColumn("used_tokens", 0).Error
}

// IncrementUsedTokens 增加已用 token
func (r *ApiKeyRepo) IncrementUsedTokens(id string, tokens int64) error {
	return r.db.Model(&model.ProviderApiKey{}).Where("id = ?", id).UpdateColumn("used_tokens", gorm.Expr("used_tokens + ?", tokens)).Error
}

// UpdateFields 使用 map 更新指定字段（支持 gorm.Expr 表达式）
func (r *ApiKeyRepo) UpdateFields(id string, fields map[string]interface{}) error {
	return r.db.Model(&model.ProviderApiKey{}).Where("id = ?", id).Updates(fields).Error
}

// ProviderStatus 统计各 Provider 的 Key 数量
func (r *ApiKeyRepo) ProviderStatus() ([]*model.ProviderStatus, error) {
	var results []*model.ProviderStatus
	err := r.db.Raw(`
		SELECT
			provider,
			COUNT(*) AS total_keys,
			SUM(CASE WHEN is_enabled = 1 THEN 1 ELSE 0 END) AS enabled_keys,
			SUM(CASE WHEN is_enabled = 1 AND (daily_quota = 0 OR used_tokens < daily_quota) THEN 1 ELSE 0 END) AS available_keys
		FROM provider_api_keys
		GROUP BY provider
	`).Scan(&results).Error
	return results, err
}
