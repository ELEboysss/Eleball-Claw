package repository

import (
	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// ChatConversationRepo 对话历史数据访问
type ChatConversationRepo struct {
	db *gorm.DB
}

// NewChatConversationRepo 创建仓库
func NewChatConversationRepo(db *gorm.DB) *ChatConversationRepo {
	return &ChatConversationRepo{db: db}
}

// Create 创建对话
func (r *ChatConversationRepo) Create(conv *model.ChatConversation) error {
	return r.db.Create(conv).Error
}

// GetByID 根据 ID 查询对话（排除已软删除）
func (r *ChatConversationRepo) GetByID(id string) (*model.ChatConversation, error) {
	var conv model.ChatConversation
	if err := r.db.First(&conv, "id = ? AND status != ?", id, "deleted").Error; err != nil {
		return nil, err
	}
	return &conv, nil
}

// ListByUser 查询用户对话列表（排除已软删除）
func (r *ChatConversationRepo) ListByUser(userID string, page, pageSize int) ([]model.ChatConversation, int64, error) {
	var total int64
	if err := r.db.Model(&model.ChatConversation{}).Where("user_id = ? AND status != ?", userID, "deleted").Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.ChatConversation
	offset := (page - 1) * pageSize
	err := r.db.Where("user_id = ? AND status != ?", userID, "deleted").
		Order("updated_at DESC").
		Limit(pageSize).
		Offset(offset).
		Find(&items).Error
	return items, total, err
}

// Update 更新对话元数据
func (r *ChatConversationRepo) Update(conv *model.ChatConversation) error {
	return r.db.Save(conv).Error
}

// UpdateFields 批量更新字段
func (r *ChatConversationRepo) UpdateFields(id string, updates map[string]interface{}) error {
	return r.db.Model(&model.ChatConversation{}).Where("id = ?", id).Updates(updates).Error
}

// UpdateEnableTools 更新 Agent 工具开关
func (r *ChatConversationRepo) UpdateEnableTools(id string, enable bool, updatedAt int64) error {
	return r.db.Model(&model.ChatConversation{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"enable_tools": enable,
			"updated_at":   updatedAt,
		}).Error
}

// Delete 硬删除对话
func (r *ChatConversationRepo) Delete(id string) error {
	return r.db.Delete(&model.ChatConversation{}, "id = ?", id).Error
}

// SoftDelete 软删除对话
func (r *ChatConversationRepo) SoftDelete(id string, updatedAt int64) error {
	return r.db.Model(&model.ChatConversation{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     "deleted",
			"updated_at": updatedAt,
		}).Error
}

// CountByUser 统计用户对话数量（排除已软删除）
func (r *ChatConversationRepo) CountByUser(userID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ChatConversation{}).Where("user_id = ? AND status != ?", userID, "deleted").Count(&count).Error
	return count, err
}

// FindOldest 查询用户最早的对话（排除已软删除）
func (r *ChatConversationRepo) FindOldest(userID string) (*model.ChatConversation, error) {
	var conv model.ChatConversation
	if err := r.db.Where("user_id = ? AND status != ?", userID, "deleted").Order("updated_at ASC").First(&conv).Error; err != nil {
		return nil, err
	}
	return &conv, nil
}

// GetMessageByClientID 根据 client_message_id 查询消息
func (r *ChatConversationRepo) GetMessageByClientID(clientMessageID string) (*model.ChatMessage, error) {
	if clientMessageID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var msg model.ChatMessage
	if err := r.db.First(&msg, "client_message_id = ?", clientMessageID).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

// SaveMessage 保存消息（client_message_id 存在时更新内容，实现去重）
func (r *ChatConversationRepo) SaveMessage(msg *model.ChatMessage) error {
	if msg.ClientMessageID != "" {
		existing, err := r.GetMessageByClientID(msg.ClientMessageID)
		if err == nil && existing != nil {
			// 已存在则更新内容，避免多设备同步时重复消息
			return r.db.Model(&model.ChatMessage{}).
				Where("id = ?", existing.ID).
				Updates(map[string]interface{}{
					"content":           msg.Content,
					"reasoning_content": msg.ReasoningContent,
					"tool_results":      msg.ToolResults,
					"attachments":       msg.Attachments,
					"created_at":        msg.CreatedAt,
				}).Error
		}
	}
	return r.db.Create(msg).Error
}

// ListMessages 查询对话消息
func (r *ChatConversationRepo) ListMessages(conversationID string, page, pageSize int) ([]model.ChatMessage, int64, error) {
	var total int64
	if err := r.db.Model(&model.ChatMessage{}).Where("conversation_id = ?", conversationID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.ChatMessage
	offset := (page - 1) * pageSize
	err := r.db.Where("conversation_id = ?", conversationID).
		Order("created_at ASC").
		Limit(pageSize).
		Offset(offset).
		Find(&items).Error
	return items, total, err
}

// CountMessages 统计对话消息数
func (r *ChatConversationRepo) CountMessages(conversationID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ChatMessage{}).Where("conversation_id = ?", conversationID).Count(&count).Error
	return count, err
}
