package service

import (
	"errors"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"gorm.io/gorm"
)

// TeamService 对话分组业务逻辑（Agent Team，组严格按 user_id 隔离）
type TeamService struct {
	db         *gorm.DB
	repo       *repository.TeamRepo
	memoryRepo *repository.TeamMemoryRepo
}

// NewTeamService 创建服务
func NewTeamService(db *gorm.DB, repo *repository.TeamRepo) *TeamService {
	return &TeamService{db: db, repo: repo}
}

// SetTeamMemoryRepo 装配组记忆仓库（Agent Team P2；设置后删除组时级联清理组记忆）
func (s *TeamService) SetTeamMemoryRepo(memoryRepo *repository.TeamMemoryRepo) {
	s.memoryRepo = memoryRepo
}

// TeamListItem 组列表项（含组内对话数）
type TeamListItem struct {
	*model.Team
	ConversationCount int64 `json:"conversation_count"`
}

// TeamDetail 组详情（含组内对话摘要列表）
type TeamDetail struct {
	*model.Team
	Conversations []model.ChatConversation `json:"conversations"`
}

// getOwned 查询分组并校验归属
func (s *TeamService) getOwned(userID, id string) (*model.Team, error) {
	t, err := s.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("组不存在")
		}
		return nil, err
	}
	if t.UserID != userID {
		return nil, errors.New("无权访问该组")
	}
	return t, nil
}

// List 查询用户分组列表（含组内对话数）
func (s *TeamService) List(userID string) ([]*TeamListItem, error) {
	teams, err := s.repo.ListByUser(userID)
	if err != nil {
		return nil, err
	}
	items := make([]*TeamListItem, 0, len(teams))
	for _, t := range teams {
		count, err := s.repo.CountConversations(t.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, &TeamListItem{Team: t, ConversationCount: count})
	}
	return items, nil
}

// Create 创建分组
func (s *TeamService) Create(userID, name, description string) (*model.Team, error) {
	if name == "" {
		return nil, errors.New("组名称不能为空")
	}
	now := time.Now().Unix()
	t := &model.Team{
		ID:          generateID("team"),
		UserID:      userID,
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.Create(t); err != nil {
		return nil, err
	}
	return t, nil
}

// Get 查询分组详情（含组内对话摘要列表）
func (s *TeamService) Get(userID, id string) (*TeamDetail, error) {
	t, err := s.getOwned(userID, id)
	if err != nil {
		return nil, err
	}
	convs, err := s.repo.ListConversations(id)
	if err != nil {
		return nil, err
	}
	if convs == nil {
		convs = []model.ChatConversation{}
	}
	return &TeamDetail{Team: t, Conversations: convs}, nil
}

// Update 更新分组名称/描述（nil 字段不更新）
func (s *TeamService) Update(userID, id string, name, description *string) (*model.Team, error) {
	t, err := s.getOwned(userID, id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		if *name == "" {
			return nil, errors.New("组名称不能为空")
		}
		t.Name = *name
	}
	if description != nil {
		t.Description = *description
	}
	t.UpdatedAt = time.Now().Unix()
	if err := s.repo.Update(t); err != nil {
		return nil, err
	}
	return t, nil
}

// Delete 删除分组：组内对话 team_id 置空（不删对话），级联清理组共享记忆；
// Agent Team P3：组内助手 team_id 一并置空（不删助手，文档 §3）
func (s *TeamService) Delete(userID, id string) error {
	if _, err := s.getOwned(userID, id); err != nil {
		return err
	}
	if err := s.repo.Delete(id); err != nil {
		return err
	}
	// Agent Team P2：删除组记忆（memoryRepo 未装配时跳过，保持 P1 行为）
	if s.memoryRepo != nil {
		if err := s.memoryRepo.DeleteByTeam(id); err != nil {
			return err
		}
	}
	// Agent Team P3：清助手组归属
	if err := s.repo.ClearAssistantRefs(s.db, id); err != nil {
		return err
	}
	return s.repo.ClearConversationRefs(s.db, id)
}

// CheckOwned 校验分组存在且属于该用户（对话归组时校验用）
func (s *TeamService) CheckOwned(userID, id string) error {
	_, err := s.getOwned(userID, id)
	return err
}
