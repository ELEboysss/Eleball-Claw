package repository

import (
	"time"

	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// VisualTaskRepo 视觉生成任务数据访问
type VisualTaskRepo struct {
	db *gorm.DB
}

// NewVisualTaskRepo 创建仓库
func NewVisualTaskRepo(db *gorm.DB) *VisualTaskRepo {
	return &VisualTaskRepo{db: db}
}

// Create 创建任务
func (r *VisualTaskRepo) Create(task *model.VisualGenerationTask) error {
	return r.db.Create(task).Error
}

// GetByID 根据 ID 查询任务
func (r *VisualTaskRepo) GetByID(id string) (*model.VisualGenerationTask, error) {
	var task model.VisualGenerationTask
	err := r.db.Where("id = ?", id).First(&task).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// GetByIDAndUser 根据 ID 与用户 ID 查询（确保用户只能访问自己的任务）
func (r *VisualTaskRepo) GetByIDAndUser(id, userID string) (*model.VisualGenerationTask, error) {
	var task model.VisualGenerationTask
	err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&task).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// Update 更新任务
func (r *VisualTaskRepo) Update(task *model.VisualGenerationTask) error {
	return r.db.Save(task).Error
}

// ListByUser 查询用户任务列表，按创建时间倒序
func (r *VisualTaskRepo) ListByUser(userID string, mediaType model.VisualMediaType, page, pageSize int) ([]model.VisualGenerationTask, int64, error) {
	var tasks []model.VisualGenerationTask
	var total int64

	query := r.db.Model(&model.VisualGenerationTask{}).Where("user_id = ?", userID)
	if mediaType != "" {
		query = query.Where("media_type = ?", mediaType)
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err = query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&tasks).Error
	return tasks, total, err
}

// ListByConversation 查询某会话下的所有任务
func (r *VisualTaskRepo) ListByConversation(conversationID, userID string) ([]model.VisualGenerationTask, error) {
	var tasks []model.VisualGenerationTask
	err := r.db.Where("conversation_id = ? AND user_id = ?", conversationID, userID).
		Order("created_at DESC").
		Find(&tasks).Error
	return tasks, err
}

// DeleteByConversation 删除某会话下的所有任务（软删除能力不足时物理删除）
func (r *VisualTaskRepo) DeleteByConversation(conversationID, userID string) error {
	return r.db.Where("conversation_id = ? AND user_id = ?", conversationID, userID).Delete(&model.VisualGenerationTask{}).Error
}

// CountRunningByUser 统计用户进行中的视频任务数
func (r *VisualTaskRepo) CountRunningByUser(userID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.VisualGenerationTask{}).
		Where("user_id = ? AND media_type = ? AND status IN ?", userID, model.VisualMediaTypeVideo, []model.VisualTaskStatus{model.VisualTaskStatusPending, model.VisualTaskStatusRunning}).
		Count(&count).Error
	return count, err
}

// ListRunningVideoTasksBefore 查询指定用户超过阈值的进行中视频任务
func (r *VisualTaskRepo) ListRunningVideoTasksBefore(userID string, threshold time.Time) ([]model.VisualGenerationTask, error) {
	var tasks []model.VisualGenerationTask
	err := r.db.Where("user_id = ? AND media_type = ? AND status IN ? AND created_at < ?",
		userID, model.VisualMediaTypeVideo, []model.VisualTaskStatus{model.VisualTaskStatusPending, model.VisualTaskStatusRunning}, threshold).
		Find(&tasks).Error
	return tasks, err
}

// ListIncompleteImageTasks 查询所有未完成的图片任务，用于服务启动后恢复执行。
func (r *VisualTaskRepo) ListIncompleteImageTasks() ([]model.VisualGenerationTask, error) {
	var tasks []model.VisualGenerationTask
	err := r.db.Where("media_type = ? AND status IN ?", model.VisualMediaTypeImage, []model.VisualTaskStatus{model.VisualTaskStatusPending, model.VisualTaskStatusRunning}).
		Order("created_at ASC").
		Find(&tasks).Error
	return tasks, err
}
