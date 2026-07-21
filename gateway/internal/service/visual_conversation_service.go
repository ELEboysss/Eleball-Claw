package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
)

// VisualConversationService 视觉创作会话服务
type VisualConversationService struct {
	convRepo      *repository.VisualConversationRepo
	taskRepo      *repository.VisualTaskRepo
	uploadService *VisualUploadService
}

// NewVisualConversationService 创建服务
func NewVisualConversationService(convRepo *repository.VisualConversationRepo, taskRepo *repository.VisualTaskRepo, uploadService *VisualUploadService) *VisualConversationService {
	return &VisualConversationService{
		convRepo:      convRepo,
		taskRepo:      taskRepo,
		uploadService: uploadService,
	}
}

// ensureConversation 确保任务有所属会话；若未指定则自动创建
// mediaType 用于新建会话时标记会话类型，并在复用会话时校验类型一致。
func (s *VisualConversationService) ensureConversation(userID, conversationID, prompt, mediaType string) (string, error) {
	if conversationID != "" {
		conv, err := s.convRepo.GetByIDAndUser(conversationID, userID)
		if err != nil {
			return "", fmt.Errorf("会话不存在或无权访问: %w", err)
		}
		if mediaType != "" && conv.MediaType != "" && conv.MediaType != mediaType {
			return "", fmt.Errorf("会话 %s 为 %s 类型，不能用于 %s 生成", conversationID, conv.MediaType, mediaType)
		}
		return conversationID, nil
	}

	// 自动生成标题：取 prompt 前 20 字
	title := prompt
	if len(title) > 20 {
		title = title[:20] + "..."
	}
	if strings.TrimSpace(title) == "" {
		title = "未命名视觉创作"
	}

	conv := &model.VisualConversation{
		ID:        uuid.NewString(),
		UserID:    userID,
		Title:     title,
		MediaType: mediaType,
		Status:    "active",
	}
	if conv.MediaType == "" {
		conv.MediaType = "image"
	}
	if err := s.convRepo.Create(conv); err != nil {
		return "", fmt.Errorf("创建会话失败: %w", err)
	}
	return conv.ID, nil
}

// CreateConversation 手动创建视觉会话
func (s *VisualConversationService) CreateConversation(userID, title, mediaType string) (*model.VisualConversation, error) {
	if strings.TrimSpace(title) == "" {
		title = "未命名视觉创作"
	}
	conv := &model.VisualConversation{
		ID:        uuid.NewString(),
		UserID:    userID,
		Title:     title,
		MediaType: mediaType,
		Status:    "active",
	}
	if conv.MediaType == "" {
		conv.MediaType = "image"
	}
	if err := s.convRepo.Create(conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// GetConversation 获取会话详情（含任务列表）
func (s *VisualConversationService) GetConversation(id, userID string) (*model.VisualConversation, []model.VisualGenerationTask, error) {
	conv, err := s.convRepo.GetByIDAndUser(id, userID)
	if err != nil {
		return nil, nil, err
	}
	tasks, err := s.taskRepo.ListByConversation(id, userID)
	if err != nil {
		return nil, nil, err
	}
	return conv, tasks, nil
}

// ListConversations 列出用户会话，可按 media_type 过滤
func (s *VisualConversationService) ListConversations(userID, mediaType string, page, pageSize int) ([]*model.VisualConversation, int64, error) {
	return s.convRepo.ListByUser(userID, mediaType, page, pageSize)
}

// DeleteConversation 删除会话及其下任务，并级联清理本地资源
func (s *VisualConversationService) DeleteConversation(id, userID string) error {
	_, err := s.convRepo.GetByIDAndUser(id, userID)
	if err != nil {
		return errors.New("会话不存在或无权访问")
	}

	tasks, err := s.taskRepo.ListByConversation(id, userID)
	if err != nil {
		return fmt.Errorf("查询会话任务失败: %w", err)
	}

	// 先软删除会话
	if err := s.convRepo.Delete(id, userID); err != nil {
		return err
	}

	// 删除会话下任务
	if err := s.taskRepo.DeleteByConversation(id, userID); err != nil {
		return err
	}

	// 级联清理本地资源（任务结果图、输入参考图等）
	if s.uploadService != nil {
		for _, task := range tasks {
			assetIDs := collectLocalAssetIDs(&task)
			for _, assetID := range assetIDs {
				if err := s.uploadService.Delete(assetID); err != nil {
					// 资源清理失败不影响主流程，记录即可
					fmt.Printf("清理视觉资源失败: %s, %v\n", assetID, err)
				}
			}
		}
	}

	return nil
}

// collectLocalAssetIDs 从任务中收集所有本地资源文件 ID
func collectLocalAssetIDs(task *model.VisualGenerationTask) []string {
	seen := make(map[string]struct{})
	var ids []string

	add := func(raw string) {
		if raw == "" {
			return
		}
		id := extractVisualFileID(raw)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	// 本地转存的结果资源
	if task.LocalAssetIDs != "" {
		var localIDs []string
		if err := json.Unmarshal([]byte(task.LocalAssetIDs), &localIDs); err == nil {
			for _, id := range localIDs {
				add(id)
			}
		}
	}

	// 输入资源（参考图/首帧图）
	if task.InputAssets != "" {
		var assets []VisualInputAsset
		if err := json.Unmarshal([]byte(task.InputAssets), &assets); err == nil {
			for _, asset := range assets {
				add(asset.URL)
			}
		}
	}

	return ids
}

// extractVisualFileID 从 /v1/visual/files/{id} 或完整 URL 中提取文件 ID
func extractVisualFileID(raw string) string {
	idx := strings.LastIndex(raw, "/v1/visual/files/")
	if idx == -1 {
		return ""
	}
	id := raw[idx+len("/v1/visual/files/"):]
	// 去掉可能的查询参数
	if q := strings.Index(id, "?"); q != -1 {
		id = id[:q]
	}
	return id
}

// UpdateConversationTime 更新会话更新时间（任务完成时调用）
func (s *VisualConversationService) UpdateConversationTime(id, userID string) error {
	conv, err := s.convRepo.GetByIDAndUser(id, userID)
	if err != nil {
		return err
	}
	conv.UpdatedAt = time.Now()
	return s.convRepo.Update(conv)
}

// UpdateConversationTitle 更新视觉会话标题
func (s *VisualConversationService) UpdateConversationTitle(id, userID, title string) error {
	if strings.TrimSpace(title) == "" {
		return errors.New("标题不能为空")
	}
	conv, err := s.convRepo.GetByIDAndUser(id, userID)
	if err != nil {
		return errors.New("会话不存在或无权访问")
	}
	conv.Title = strings.TrimSpace(title)
	conv.UpdatedAt = time.Now()
	return s.convRepo.Update(conv)
}
