package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// SettingRepo 系统设置数据访问
type SettingRepo struct {
	db *gorm.DB
}

// NewSettingRepo 创建仓库
func NewSettingRepo(db *gorm.DB) *SettingRepo {
	return &SettingRepo{db: db}
}

// Get 获取单个设置值，不存在返回空字符串
func (r *SettingRepo) Get(key string) (string, error) {
	var s model.SystemSetting
	if err := r.db.First(&s, "key = ?", key).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	return s.Value, nil
}

// Set 设置键值
func (r *SettingRepo) Set(key, value string) error {
	return r.db.Save(&model.SystemSetting{Key: key, Value: value}).Error
}

// MSet 批量设置
func (r *SettingRepo) MSet(settings map[string]string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for k, v := range settings {
			if err := tx.Save(&model.SystemSetting{Key: k, Value: v}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetAll 获取全部设置
func (r *SettingRepo) GetAll() (map[string]string, error) {
	var list []model.SystemSetting
	if err := r.db.Find(&list).Error; err != nil {
		return nil, err
	}
	result := make(map[string]string, len(list))
	for _, s := range list {
		result[s.Key] = s.Value
	}
	return result, nil
}
