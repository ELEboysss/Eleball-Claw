package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// AgentSessionRepo Agent Session 数据访问
type AgentSessionRepo struct {
	db *gorm.DB
}

// NewAgentSessionRepo 创建仓库
func NewAgentSessionRepo(db *gorm.DB) *AgentSessionRepo {
	return &AgentSessionRepo{db: db}
}

// Create 创建 Session
func (r *AgentSessionRepo) Create(session *model.AgentSession) error {
	return r.db.Create(session).Error
}

// GetByID 根据 ID 查询 Session
func (r *AgentSessionRepo) GetByID(id string) (*model.AgentSession, error) {
	var session model.AgentSession
	if err := r.db.First(&session, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// ListByUser 查询用户 Session 列表
func (r *AgentSessionRepo) ListByUser(userID string, page, pageSize int) ([]model.AgentSession, int64, error) {
	var total int64
	if err := r.db.Model(&model.AgentSession{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.AgentSession
	offset := (page - 1) * pageSize
	err := r.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(pageSize).
		Offset(offset).
		Find(&items).Error
	return items, total, err
}

// Update 更新 Session
func (r *AgentSessionRepo) Update(session *model.AgentSession) error {
	return r.db.Save(session).Error
}

// Delete 删除 Session
func (r *AgentSessionRepo) Delete(id string) error {
	return r.db.Delete(&model.AgentSession{}, "id = ?", id).Error
}

// CountByUser 统计用户 Session 数量
func (r *AgentSessionRepo) CountByUser(userID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.AgentSession{}).Where("user_id = ?", userID).Count(&count).Error
	return count, err
}

// FindOldest 查询用户最早的 Session
func (r *AgentSessionRepo) FindOldest(userID string) (*model.AgentSession, error) {
	var session model.AgentSession
	if err := r.db.Where("user_id = ?", userID).Order("updated_at ASC").First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// SaveOutput 保存输出资源元数据
func (r *AgentSessionRepo) SaveOutput(output *model.AgentSessionOutput) error {
	return r.db.Create(output).Error
}

// GetOutputByResourceID 根据资源 ID 查询输出
func (r *AgentSessionRepo) GetOutputByResourceID(resourceID string) (*model.AgentSessionOutput, error) {
	var output model.AgentSessionOutput
	if err := r.db.First(&output, "resource_id = ?", resourceID).Error; err != nil {
		return nil, err
	}
	return &output, nil
}

// ListByConversation 查询某个对话下的所有 Session（带用户校验）
func (r *AgentSessionRepo) ListByConversation(userID, conversationID string) ([]model.AgentSession, error) {
	var items []model.AgentSession
	err := r.db.Where("user_id = ? AND conversation_id = ?", userID, conversationID).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

// ListRunningByUser 查询指定用户所有运行中（status='running'）的 Session。
func (r *AgentSessionRepo) ListRunningByUser(userID string) ([]model.AgentSession, error) {
	var items []model.AgentSession
	err := r.db.Where("user_id = ? AND status = ?", userID, "running").
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

// DeleteByIDs 批量删除 Session
func (r *AgentSessionRepo) DeleteByIDs(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.Delete(&model.AgentSession{}, "id IN ?", ids).Error
}

// DeleteOutputsBySessionID 删除某个 Session 的所有输出资源元数据
func (r *AgentSessionRepo) DeleteOutputsBySessionID(sessionID string) error {
	return r.db.Delete(&model.AgentSessionOutput{}, "session_id = ?", sessionID).Error
}

// DeleteOutputsBySessionIDs 批量删除多个 Session 的输出资源元数据
func (r *AgentSessionRepo) DeleteOutputsBySessionIDs(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.Delete(&model.AgentSessionOutput{}, "session_id IN ?", ids).Error
}
