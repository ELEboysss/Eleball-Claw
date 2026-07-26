package service

import (
	"context"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// teamTestEnv 对话分组测试的公共装配
type teamTestEnv struct {
	db     *gorm.DB
	team   *TeamService
	conv   *ConversationService
	convDB *repository.ChatConversationRepo
}

func setupTeamTest(t *testing.T) *teamTestEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// :memory: 数据库按连接隔离，限制单连接避免「no such table」
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Team{}, &model.ChatConversation{}))

	teamSvc := NewTeamService(db, repository.NewTeamRepo(db))
	convRepo := repository.NewChatConversationRepo(db)
	// vipService 传 nil：本组测试不触发对话创建配额链路
	convSvc := NewConversationService(convRepo, nil, t.TempDir())
	convSvc.SetTeamService(teamSvc)

	return &teamTestEnv{db: db, team: teamSvc, conv: convSvc, convDB: convRepo}
}

// insertConversation 直接落一条对话记录（绕过配额/磁盘目录链路，专注分组逻辑）
func (e *teamTestEnv) insertConversation(t *testing.T, id, userID, teamID string) {
	t.Helper()
	require.NoError(t, e.db.Create(&model.ChatConversation{
		ID:        id,
		UserID:    userID,
		Title:     "对话 " + id,
		Status:    "active",
		TeamID:    teamID,
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}).Error)
}

const teamTestUser = "user-1"

// TestTeamService_CRUD 分组增删改查与用户隔离
func TestTeamService_CRUD(t *testing.T) {
	env := setupTeamTest(t)

	// name 必填
	_, err := env.team.Create(teamTestUser, "", "")
	require.Error(t, err)

	team, err := env.team.Create(teamTestUser, "项目 A", "项目 A 的相关对话")
	require.NoError(t, err)
	require.NotEmpty(t, team.ID)
	assert.Equal(t, "项目 A", team.Name)

	// 更新名称/描述
	newName, newDesc := "项目 A+", "更新后的描述"
	updated, err := env.team.Update(teamTestUser, team.ID, &newName, &newDesc)
	require.NoError(t, err)
	assert.Equal(t, newName, updated.Name)
	assert.Equal(t, newDesc, updated.Description)

	// 空名称拒绝
	empty := ""
	_, err = env.team.Update(teamTestUser, team.ID, &empty, nil)
	require.Error(t, err)

	// 列表（含对话数）
	env.insertConversation(t, "conv-1", teamTestUser, team.ID)
	list, err := env.team.List(teamTestUser)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, team.ID, list[0].ID)
	assert.Equal(t, int64(1), list[0].ConversationCount)

	// 详情（含组内对话摘要列表）
	detail, err := env.team.Get(teamTestUser, team.ID)
	require.NoError(t, err)
	require.Len(t, detail.Conversations, 1)
	assert.Equal(t, "conv-1", detail.Conversations[0].ID)

	// 用户隔离：他人不可见/不可改/不可删
	_, err = env.team.Get("user-2", team.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "无权访问该组")
	_, err = env.team.Update("user-2", team.ID, &newName, nil)
	require.Error(t, err)
	require.Error(t, env.team.Delete("user-2", team.ID))

	// 不存在的组
	_, err = env.team.Get(teamTestUser, "team-x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "组不存在")

	// 删除
	require.NoError(t, env.team.Delete(teamTestUser, team.ID))
	list, err = env.team.List(teamTestUser)
	require.NoError(t, err)
	assert.Empty(t, list)
}

// TestTeamService_DeleteClearsConversationTeamID 删除组时清空组内对话的 team_id（不删对话）
func TestTeamService_DeleteClearsConversationTeamID(t *testing.T) {
	env := setupTeamTest(t)

	team, err := env.team.Create(teamTestUser, "临时组", "")
	require.NoError(t, err)
	env.insertConversation(t, "conv-1", teamTestUser, team.ID)
	env.insertConversation(t, "conv-2", teamTestUser, "")

	require.NoError(t, env.team.Delete(teamTestUser, team.ID))

	var c1, c2 model.ChatConversation
	require.NoError(t, env.db.First(&c1, "id = ?", "conv-1").Error)
	require.NoError(t, env.db.First(&c2, "id = ?", "conv-2").Error)
	assert.Empty(t, c1.TeamID) // 组内对话 team_id 已清空，对话本身保留
	assert.Empty(t, c2.TeamID)
}

// TestConversationService_TeamAssign 对话归组：归属校验与移出分组
func TestConversationService_TeamAssign(t *testing.T) {
	env := setupTeamTest(t)
	ctx := context.Background()

	team, err := env.team.Create(teamTestUser, "项目组", "")
	require.NoError(t, err)
	otherTeam, err := env.team.Create("user-2", "他人组", "")
	require.NoError(t, err)
	env.insertConversation(t, "conv-1", teamTestUser, "")

	// 归组成功
	err = env.conv.Update(ctx, "conv-1", teamTestUser, UpdateConversationReq{TeamID: &team.ID})
	require.NoError(t, err)
	detail, err := env.conv.GetDetail(ctx, "conv-1", teamTestUser)
	require.NoError(t, err)
	assert.Equal(t, team.ID, detail.TeamID)

	// 归到他人的组：拒绝
	err = env.conv.Update(ctx, "conv-1", teamTestUser, UpdateConversationReq{TeamID: &otherTeam.ID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "无权访问该组")

	// 归到不存在的组：拒绝
	missing := "team-x"
	err = env.conv.Update(ctx, "conv-1", teamTestUser, UpdateConversationReq{TeamID: &missing})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "组不存在")

	// 移出分组（空字符串）
	empty := ""
	err = env.conv.Update(ctx, "conv-1", teamTestUser, UpdateConversationReq{TeamID: &empty})
	require.NoError(t, err)
	detail, err = env.conv.GetDetail(ctx, "conv-1", teamTestUser)
	require.NoError(t, err)
	assert.Empty(t, detail.TeamID)
}

// TestConversationService_ListByTeam 对话列表按组过滤
func TestConversationService_ListByTeam(t *testing.T) {
	env := setupTeamTest(t)
	ctx := context.Background()

	team, err := env.team.Create(teamTestUser, "项目组", "")
	require.NoError(t, err)
	env.insertConversation(t, "conv-1", teamTestUser, team.ID)
	env.insertConversation(t, "conv-2", teamTestUser, "")

	// 按组过滤：仅返回组内对话
	items, total, err := env.conv.List(ctx, teamTestUser, team.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "conv-1", items[0].ID)

	// 不过滤：返回全部
	_, total, err = env.conv.List(ctx, teamTestUser, "", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
}
