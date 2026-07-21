package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// ConversationRepo 对话同步数据访问
type ConversationRepo struct {
	db *gorm.DB
}

// NewConversationRepo 创建仓库
func NewConversationRepo(db *gorm.DB) *ConversationRepo {
	return &ConversationRepo{db: db}
}

// SaveSyncRecord 保存同步记录
func (r *ConversationRepo) SaveSyncRecord(record *model.Conversation) error {
	return r.db.Create(record).Error
}

// PullSyncRecords 拉取某用户大于指定版本的增量记录
func (r *ConversationRepo) PullSyncRecords(userID string, minVersion int64) ([]model.Conversation, error) {
	var records []model.Conversation
	err := r.db.Where("user_id = ? AND sync_version > ?", userID, minVersion).
		Order("sync_version ASC").
		Find(&records).Error
	return records, err
}

// GetMaxSyncVersion 获取用户当前最大同步版本号
func (r *ConversationRepo) GetMaxSyncVersion(userID string) (int64, error) {
	var maxVersion int64
	err := r.db.Model(&model.Conversation{}).
		Where("user_id = ?", userID).
		Select("COALESCE(MAX(sync_version), 0)").
		Scan(&maxVersion).Error
	return maxVersion, err
}
