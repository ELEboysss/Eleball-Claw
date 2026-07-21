package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// ActivityRepo 动态事件数据访问
type ActivityRepo struct {
	db *gorm.DB
}

// NewActivityRepo 创建仓库
func NewActivityRepo(db *gorm.DB) *ActivityRepo {
	return &ActivityRepo{db: db}
}

// Create 创建动态事件
func (r *ActivityRepo) Create(event *model.ActivityEvent) error {
	return r.db.Create(event).Error
}

// ListRecent 查询最近的动态事件
func (r *ActivityRepo) ListRecent(limit int) ([]*model.ActivityEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	var items []*model.ActivityEvent
	err := r.db.Model(&model.ActivityEvent{}).
		Order("created_at DESC").
		Limit(limit).
		Find(&items).Error
	return items, err
}
