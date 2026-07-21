package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrConversationNotFound 对话不存在
var ErrConversationNotFound = errors.New("对话不存在")

// ConversationService 对话业务逻辑
type ConversationService struct {
	repo       *repository.ChatConversationRepo
	vipService *VIPService
	basePath   string
}

// NewConversationService 创建服务
func NewConversationService(repo *repository.ChatConversationRepo, vipService *VIPService, basePath string) *ConversationService {
	return &ConversationService{repo: repo, vipService: vipService, basePath: basePath}
}

// CreateConversationReq 创建对话请求
type CreateConversationReq struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Model           string `json:"model"`
	Provider        string `json:"provider"`
	EnableTools     bool   `json:"enable_tools"`
	EnableWebSearch bool   `json:"enable_web_search"`
	SearchProvider  string `json:"search_provider"`
}

// createConversationWithID 使用指定 ID 创建对话，是 CreateConversation 与 GetOrCreate 的公共逻辑。
// 当指定 ID 已存在时，若属于同一用户则返回已有对话（幂等），避免前端重复请求产生大量空白对话。
func (s *ConversationService) createConversationWithID(ctx context.Context, userID, id string, req CreateConversationReq) (*model.ChatConversation, error) {
	// 确保配额有空间
	if err := s.EnsureQuota(ctx, userID); err != nil {
		return nil, err
	}

	// 幂等：相同 ID 已存在时直接返回，防止重复创建
	if existing, err := s.repo.GetByID(id); err == nil {
		if existing.UserID != userID {
			return nil, fmt.Errorf("对话 ID 已被其他用户占用")
		}
		return existing, nil
	}

	searchProvider := req.SearchProvider
	if searchProvider == "" {
		searchProvider = "baidu"
	}
	conv := &model.ChatConversation{
		ID:              id,
		UserID:          userID,
		Title:           req.Title,
		Model:           req.Model,
		Provider:        req.Provider,
		Status:          "active",
		EnableTools:     req.EnableTools,
		EnableWebSearch: req.EnableWebSearch,
		SearchProvider:  searchProvider,
		DiskPath:        filepath.Join(s.basePath, userID, "conversations", id),
		CreatedAt:       time.Now().Unix(),
		UpdatedAt:       time.Now().Unix(),
	}

	if err := s.repo.Create(conv); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(conv.DiskPath, 0750); err != nil {
		return nil, fmt.Errorf("创建对话目录失败: %w", err)
	}
	return conv, nil
}

// CreateConversation 创建新对话
func (s *ConversationService) CreateConversation(ctx context.Context, userID string, req CreateConversationReq) (*model.ChatConversation, error) {
	id := req.ID
	if id == "" {
		id = generateID("conv")
	}
	return s.createConversationWithID(ctx, userID, id, req)
}

// GetOrCreate 获取或创建对话
// 当传入的 conversationID 不存在时，使用客户端传入的 ID 创建对话，保证前后端对话 ID 一致，
// 避免 Agent Session 关联的 conversation_id 与前端本地对话列表不匹配。
func (s *ConversationService) GetOrCreate(ctx context.Context, userID string, conversationID string) (*model.ChatConversation, error) {
	if conversationID == "" {
		return s.createConversationWithID(ctx, userID, generateID("conv"), CreateConversationReq{
			Title:       "新对话",
			EnableTools: false,
		})
	}
	conv, err := s.repo.GetByID(conversationID)
	if err != nil {
		return s.createConversationWithID(ctx, userID, conversationID, CreateConversationReq{
			Title:       "新对话",
			EnableTools: false,
		})
	}
	if conv.UserID != userID {
		return nil, fmt.Errorf("无权访问该对话")
	}
	return conv, nil
}

// UpdateEnableTools 更新 Agent 工具开关
func (s *ConversationService) UpdateEnableTools(ctx context.Context, conversationID string, enable bool) error {
	return s.repo.UpdateEnableTools(conversationID, enable, time.Now().Unix())
}

// EnsureQuota 确保对话数量未超限，超出则删除最早的
func (s *ConversationService) EnsureQuota(ctx context.Context, userID string) error {
	limit, err := s.vipService.GetMaxConversations(userID)
	if err != nil {
		return err
	}
	count, err := s.repo.CountByUser(userID)
	if err != nil {
		return err
	}
	if count >= int64(limit) {
		oldest, err := s.repo.FindOldest(userID)
		if err != nil {
			return err
		}
		return s.DeleteConversation(ctx, oldest.ID)
	}
	return nil
}

// DeleteConversation 删除对话（软删除，保留数据便于审计与多端同步）
func (s *ConversationService) DeleteConversation(ctx context.Context, id string) error {
	conv, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrConversationNotFound
		}
		return err
	}
	// 删除磁盘目录
	if conv.DiskPath != "" {
		_ = os.RemoveAll(conv.DiskPath)
	}
	return s.repo.SoftDelete(id, time.Now().Unix())
}

// UpdateConversationReq 更新对话请求
type UpdateConversationReq struct {
	Title           *string `json:"title,omitempty"`
	EnableTools     *bool   `json:"enable_tools,omitempty"`
	EnableWebSearch *bool   `json:"enable_web_search,omitempty"`
	SearchProvider  *string `json:"search_provider,omitempty"`
	UpdatedAt       *int64  `json:"updated_at,omitempty"`
}

// Update 更新对话元数据（带简单冲突检测：客户端 updated_at 小于服务端时拒绝）
func (s *ConversationService) Update(ctx context.Context, id, userID string, req UpdateConversationReq) error {
	conv, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	if conv.UserID != userID {
		return fmt.Errorf("无权访问该对话")
	}
	// 若客户端提供 updated_at 且小于服务端当前值，判定为过期写
	if req.UpdatedAt != nil && *req.UpdatedAt < conv.UpdatedAt {
		return fmt.Errorf("冲突：服务端存在更新版本")
	}

	updates := map[string]interface{}{
		"updated_at": time.Now().Unix(),
	}
	if req.Title != nil {
		updates["title"] = *req.Title
	}
	if req.EnableTools != nil {
		updates["enable_tools"] = *req.EnableTools
	}
	if req.EnableWebSearch != nil {
		updates["enable_web_search"] = *req.EnableWebSearch
	}
	if req.SearchProvider != nil {
		updates["search_provider"] = *req.SearchProvider
	}
	return s.repo.UpdateFields(id, updates)
}

// Delete 删除对话
func (s *ConversationService) Delete(ctx context.Context, id, userID string) error {
	conv, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrConversationNotFound
		}
		return err
	}
	if conv.UserID != userID {
		return fmt.Errorf("无权访问该对话")
	}
	return s.DeleteConversation(ctx, id)
}

// GetDetail 查询对话详情
func (s *ConversationService) GetDetail(ctx context.Context, id, userID string) (*model.ChatConversation, error) {
	conv, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if conv.UserID != userID {
		return nil, fmt.Errorf("无权访问该对话")
	}
	return conv, nil
}

// List 查询对话列表
func (s *ConversationService) List(ctx context.Context, userID string, page, pageSize int) ([]model.ChatConversation, int64, error) {
	return s.repo.ListByUser(userID, page, pageSize)
}

// SaveMessage 保存消息
// 若对应 conversation 不存在则自动创建，兼容前端本地预生成 ID 的场景。
// 当对话标题仍为默认"新对话"且保存的是用户消息时，自动根据内容生成标题，并返回更新后的标题。
func (s *ConversationService) SaveMessage(ctx context.Context, conversationID, userID string, msg *model.ChatMessage) (string, error) {
	conv, err := s.repo.GetByID(conversationID)
	if err != nil {
		conv, err = s.GetOrCreate(ctx, userID, conversationID)
		if err != nil {
			return "", err
		}
	}
	if conv.UserID != userID {
		return "", fmt.Errorf("无权访问该对话")
	}

	updatedTitle := ""
	if conv.Title == "新对话" && msg.Role == "user" && msg.Content != "" {
		if newTitle := generateTitleFromContent(msg.Content); newTitle != "" {
			_ = s.repo.UpdateFields(conv.ID, map[string]interface{}{"title": newTitle, "updated_at": time.Now().Unix()})
			updatedTitle = newTitle
		}
	}

	if msg.ID == "" {
		msg.ID = generateID("msg")
	}
	msg.ConversationID = conversationID
	if msg.CreatedAt == 0 {
		msg.CreatedAt = time.Now().Unix()
	}
	if err := s.repo.SaveMessage(msg); err != nil {
		return "", err
	}
	return updatedTitle, nil
}

// generateTitleFromContent 根据用户消息内容生成对话标题。
// 若 content 是 content parts JSON 数组，会优先提取文本部分。
func generateTitleFromContent(content string) string {
	text := content
	var parts []map[string]interface{}
	if err := json.Unmarshal([]byte(content), &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			if t, ok := p["type"].(string); ok && t == "text" {
				if txt, ok := p["text"].(string); ok {
					sb.WriteString(txt)
				}
			}
		}
		if sb.Len() > 0 {
			text = sb.String()
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 20 {
		return string(runes[:20]) + "…"
	}
	return text
}

// ListMessages 查询消息列表
func (s *ConversationService) ListMessages(ctx context.Context, conversationID, userID string, page, pageSize int) ([]model.ChatMessage, int64, error) {
	conv, err := s.repo.GetByID(conversationID)
	if err != nil {
		return nil, 0, err
	}
	if conv.UserID != userID {
		return nil, 0, fmt.Errorf("无权访问该对话")
	}
	return s.repo.ListMessages(conversationID, page, pageSize)
}

// generateID 生成带前缀的 UUID
func generateID(prefix string) string {
	return prefix + "-" + uuid.New().String()[:8]
}
