package service

import (
	"context"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupConversationService(t *testing.T) *ConversationService {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.ChatConversation{}, &model.ChatMessage{}, &model.VIPPlan{}, &model.VIPSubscription{}))

	userRepo := repository.NewUserRepo(db)
	require.NoError(t, userRepo.Create(&model.User{ID: "u1", Username: "user1@example.com", Role: model.UserRoleUser, Status: 1}))
	require.NoError(t, userRepo.Create(&model.User{ID: "u2", Username: "user2@example.com", Role: model.UserRoleUser, Status: 1}))

	repo := repository.NewChatConversationRepo(db)
	return NewConversationService(repo, newTestVIPService(db), t.TempDir())
}

func TestConversationService_Update(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "old"})
	require.NoError(t, err)

	newTitle := "new"
	err = svc.Update(ctx, conv.ID, "u1", UpdateConversationReq{Title: &newTitle})
	require.NoError(t, err)

	detail, err := svc.GetDetail(ctx, conv.ID, "u1")
	require.NoError(t, err)
	assert.Equal(t, "new", detail.Title)
}

func TestConversationService_Update_OwnerCheck(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	newTitle := "hacked"
	err = svc.Update(ctx, conv.ID, "u2", UpdateConversationReq{Title: &newTitle})
	assert.Error(t, err)
}

func TestConversationService_Update_Conflict(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	oldUpdatedAt := conv.UpdatedAt - 1
	newTitle := "new"
	err = svc.Update(ctx, conv.ID, "u1", UpdateConversationReq{Title: &newTitle, UpdatedAt: &oldUpdatedAt})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "冲突")
}

func TestConversationService_Delete(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	err = svc.Delete(ctx, conv.ID, "u1")
	require.NoError(t, err)

	_, err = svc.GetDetail(ctx, conv.ID, "u1")
	assert.Error(t, err)
}

func TestConversationService_GetDetail(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	detail, err := svc.GetDetail(ctx, conv.ID, "u1")
	require.NoError(t, err)
	assert.Equal(t, conv.ID, detail.ID)

	_, err = svc.GetDetail(ctx, conv.ID, "u2")
	assert.Error(t, err)
}

func TestConversationService_List(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	_, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "a"})
	require.NoError(t, err)
	_, err = svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "b"})
	require.NoError(t, err)

	items, total, err := svc.List(ctx, "u1", "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, items, 2)
}

func TestConversationService_SaveAndListMessages(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	msg := &model.ChatMessage{Role: "user", Content: "hello", ClientMessageID: "c1"}
	updatedTitle, err := svc.SaveMessage(ctx, conv.ID, "u1", msg)
	require.NoError(t, err)
	// 对话标题已自定义，不会自动生成
	assert.Empty(t, updatedTitle)
	assert.NotEmpty(t, msg.ID)

	msgs, total, err := svc.ListMessages(ctx, conv.ID, "u1", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "hello", msgs[0].Content)
}

func TestConversationService_SaveMessage_GeneratesTitle(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "新对话"})
	require.NoError(t, err)

	msg := &model.ChatMessage{Role: "user", Content: "hello world", ClientMessageID: "c1"}
	updatedTitle, err := svc.SaveMessage(ctx, conv.ID, "u1", msg)
	require.NoError(t, err)
	assert.Equal(t, "hello world", updatedTitle)

	// 再次保存不应再生成标题
	msg2 := &model.ChatMessage{Role: "user", Content: "second message", ClientMessageID: "c2"}
	updatedTitle2, err := svc.SaveMessage(ctx, conv.ID, "u1", msg2)
	require.NoError(t, err)
	assert.Empty(t, updatedTitle2)
}

func TestConversationService_SaveMessage_OwnerCheck(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	msg := &model.ChatMessage{Role: "user", Content: "hello"}
	_, err = svc.SaveMessage(ctx, conv.ID, "u2", msg)
	assert.Error(t, err)
}

func TestConversationService_Create_WithWebSearchDefaults(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)
	assert.False(t, conv.EnableWebSearch)
	assert.Equal(t, "baidu", conv.SearchProvider)
}

func TestConversationService_Create_WithCustomWebSearch(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{
		Title:           "t",
		EnableWebSearch: true,
		SearchProvider:  "bing",
	})
	require.NoError(t, err)
	assert.True(t, conv.EnableWebSearch)
	assert.Equal(t, "bing", conv.SearchProvider)
}

func TestConversationService_Update_WebSearchAndProvider(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	enable := true
	provider := "bing"
	err = svc.Update(ctx, conv.ID, "u1", UpdateConversationReq{
		EnableWebSearch: &enable,
		SearchProvider:  &provider,
	})
	require.NoError(t, err)

	detail, err := svc.GetDetail(ctx, conv.ID, "u1")
	require.NoError(t, err)
	assert.True(t, detail.EnableWebSearch)
	assert.Equal(t, "bing", detail.SearchProvider)
}

func TestConversationService_UpdateEnableTools(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	err = svc.UpdateEnableTools(ctx, conv.ID, true)
	require.NoError(t, err)

	detail, err := svc.GetDetail(ctx, conv.ID, "u1")
	require.NoError(t, err)
	assert.True(t, detail.EnableTools)
}

func TestConversationService_GetOrCreate(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()
	conv, err := svc.GetOrCreate(ctx, "u1", "")
	require.NoError(t, err)
	assert.NotEmpty(t, conv.ID)

	conv2, err := svc.GetOrCreate(ctx, "u1", conv.ID)
	require.NoError(t, err)
	assert.Equal(t, conv.ID, conv2.ID)

	_, err = svc.GetOrCreate(ctx, "u2", conv.ID)
	assert.Error(t, err)
}

// TestConversationService_ForkConversation AR-12：分叉复制父对话到 entry_id 为止的消息历史，
// 继承模型/工具配置，重写消息 ID，校验所有权与无效分叉点。
func TestConversationService_ForkConversation(t *testing.T) {
	svc := setupConversationService(t)
	ctx := context.Background()

	conv, err := svc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "主线", Model: "gpt-4", EnableTools: true})
	require.NoError(t, err)

	msgs := []model.ChatMessage{
		{ID: "m1", ConversationID: conv.ID, Role: "user", Content: "问题1", CreatedAt: 1000},
		{ID: "m2", ConversationID: conv.ID, Role: "assistant", Content: "回答1", CreatedAt: 2000},
		{ID: "m3", ConversationID: conv.ID, Role: "user", Content: "问题2", CreatedAt: 3000},
	}
	for i := range msgs {
		_, err := svc.SaveMessage(ctx, conv.ID, "u1", &msgs[i])
		require.NoError(t, err)
	}

	// 从 m2 分叉：新对话应含 m1+m2（2 条），不含 m3
	forked, err := svc.ForkConversation(ctx, "u1", conv.ID, "m2")
	require.NoError(t, err)
	assert.NotEqual(t, conv.ID, forked.ID)
	assert.Contains(t, forked.Title, "主线")
	assert.Contains(t, forked.Title, "分叉")
	assert.Equal(t, "gpt-4", forked.Model)
	assert.True(t, forked.EnableTools)

	listed, total, err := svc.repo.ListMessages(forked.ID, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, listed, 2)
	assert.Equal(t, "问题1", listed[0].Content)
	assert.Equal(t, "回答1", listed[1].Content)
	// 复制后消息 ID 重写，不同于父
	assert.NotEqual(t, "m1", listed[0].ID)
	assert.NotEqual(t, "m2", listed[1].ID)

	// 无效分叉点
	_, err = svc.ForkConversation(ctx, "u1", conv.ID, "nope")
	assert.Error(t, err)

	// 所有权校验：u2 不能分叉 u1 的对话
	_, err = svc.ForkConversation(ctx, "u2", conv.ID, "m2")
	assert.Error(t, err)
}
