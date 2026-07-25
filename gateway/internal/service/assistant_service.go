package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AssistantService 助手业务逻辑（助手 = 已激活秘技的命名组合，按会话应用）
type AssistantService struct {
	db        *gorm.DB
	repo      *repository.AssistantRepo
	agentRepo *repository.AgentRepo
}

// NewAssistantService 创建服务
func NewAssistantService(db *gorm.DB, repo *repository.AssistantRepo, agentRepo *repository.AgentRepo) *AssistantService {
	return &AssistantService{db: db, repo: repo, agentRepo: agentRepo}
}

// AssistantAgentItem 助手条目展开视图（供前端展示秘技概要）
type AssistantAgentItem struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	IconURL string `json:"icon_url"`
}

// AssistantView 助手 + 条目展开
type AssistantView struct {
	*model.Assistant
	Items []AssistantAgentItem `json:"items"`
}

// getOwned 查询助手并校验归属
func (s *AssistantService) getOwned(userID, id string) (*model.Assistant, error) {
	a, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("助手不存在")
		}
		return nil, err
	}
	if a.UserID != userID {
		return nil, errors.New("无权访问该助手")
	}
	return a, nil
}

// expandItems 展开助手条目为秘技概要
func (s *AssistantService) expandItems(assistantID string) []AssistantAgentItem {
	agentIDs, err := s.repo.ListAgentIDs(assistantID)
	if err != nil || len(agentIDs) == 0 {
		return []AssistantAgentItem{}
	}
	items := make([]AssistantAgentItem, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		view := AssistantAgentItem{AgentID: agentID}
		if agent, err := s.agentRepo.GetByID(agentID); err == nil && agent != nil {
			view.Name = agent.Name
			view.IconURL = agent.IconURL
		}
		items = append(items, view)
	}
	return items
}

// List 查询用户助手列表（含条目展开）
func (s *AssistantService) List(userID string) ([]*AssistantView, error) {
	assistants, err := s.repo.ListByUser(userID)
	if err != nil {
		return nil, err
	}
	views := make([]*AssistantView, 0, len(assistants))
	for _, a := range assistants {
		views = append(views, &AssistantView{Assistant: a, Items: s.expandItems(a.ID)})
	}
	return views, nil
}

// Create 创建助手
func (s *AssistantService) Create(userID, name, description string) (*AssistantView, error) {
	if name == "" {
		return nil, errors.New("助手名称不能为空")
	}
	a := &model.Assistant{
		ID:          uuid.New().String(),
		UserID:      userID,
		Name:        name,
		Description: description,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.repo.Create(a); err != nil {
		return nil, err
	}
	return &AssistantView{Assistant: a, Items: []AssistantAgentItem{}}, nil
}

// Get 查询助手详情（含条目展开）
func (s *AssistantService) Get(userID, id string) (*AssistantView, error) {
	a, err := s.getOwned(userID, id)
	if err != nil {
		return nil, err
	}
	return &AssistantView{Assistant: a, Items: s.expandItems(a.ID)}, nil
}

// Update 更新助手名称/描述（nil 字段不更新）
func (s *AssistantService) Update(userID, id string, name, description *string) (*AssistantView, error) {
	a, err := s.getOwned(userID, id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		if *name == "" {
			return nil, errors.New("助手名称不能为空")
		}
		a.Name = *name
	}
	if description != nil {
		a.Description = *description
	}
	a.UpdatedAt = time.Now()
	if err := s.repo.Update(a); err != nil {
		return nil, err
	}
	return &AssistantView{Assistant: a, Items: s.expandItems(a.ID)}, nil
}

// Delete 删除助手，并清除引用它的会话绑定
func (s *AssistantService) Delete(userID, id string) error {
	if _, err := s.getOwned(userID, id); err != nil {
		return err
	}
	if err := s.repo.Delete(id); err != nil {
		return err
	}
	return s.repo.ClearConversationRefs(s.db, id)
}

// SetItems 全量替换助手条目。
// 逐项校验：必须是该用户已购买且已激活的秘技，否则返回明确错误。
func (s *AssistantService) SetItems(userID, id string, agentIDs []string) (*AssistantView, error) {
	a, err := s.getOwned(userID, id)
	if err != nil {
		return nil, err
	}
	for _, agentID := range agentIDs {
		if agentID == "" {
			return nil, errors.New("agent_id 不能为空")
		}
		purchased, err := s.agentRepo.HasPurchased(agentID, userID)
		if err != nil {
			return nil, err
		}
		if !purchased {
			return nil, fmt.Errorf("秘技 %s 未购买，请先购买后再加入助手", agentID)
		}
		active, err := s.agentRepo.IsToolActive(userID, agentID)
		if err != nil {
			return nil, err
		}
		if !active {
			return nil, fmt.Errorf("秘技 %s 未激活，请先激活后再加入助手", agentID)
		}
	}
	if err := s.repo.SetItems(id, agentIDs); err != nil {
		return nil, err
	}
	a.UpdatedAt = time.Now()
	if err := s.repo.Update(a); err != nil {
		return nil, err
	}
	return &AssistantView{Assistant: a, Items: s.expandItems(a.ID)}, nil
}

// AgentIDsFor 返回助手包含的秘技 ID 集合（Agent 执行过滤用）。
// 助手不存在或不属于该用户时返回空集合（不报错，由调用方降级为无动态工具）。
func (s *AssistantService) AgentIDsFor(userID, assistantID string) ([]string, error) {
	if _, err := s.getOwned(userID, assistantID); err != nil {
		return []string{}, nil
	}
	ids, err := s.repo.ListAgentIDs(assistantID)
	if err != nil {
		return nil, err
	}
	return ids, nil
}
