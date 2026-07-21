package service

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupAgentQuotaTest(t *testing.T) (*AgentService, *ConversationService) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.ChatConversation{}, &model.ChatMessage{}, &model.AgentSession{}, &model.AgentSessionOutput{}, &model.VIPPlan{}, &model.VIPSubscription{}))

	userRepo := repository.NewUserRepo(db)
	require.NoError(t, userRepo.Create(&model.User{ID: "u1", Username: "alice", Role: model.UserRoleUser, Status: 1}))
	require.NoError(t, userRepo.Create(&model.User{ID: "u2", Username: "admin", Role: model.UserRoleAdmin, Status: 1}))

	convRepo := repository.NewChatConversationRepo(db)
	vipService := newTestVIPService(db)
	convSvc := NewConversationService(convRepo, vipService, t.TempDir())
	sessionRepo := repository.NewAgentSessionRepo(db)

	resolver := func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return nil, fmt.Errorf("测试未配置 LLM 客户端")
	}
	agentSvc := NewAgentService(convSvc, sessionRepo, userRepo, vipService, nil, NewNoOpEleAgentModelService(), NewFileSandbox(t.TempDir(), ""), NewToolRegistry(), NewToolSchemaBuilder(NewToolRegistry()), NewAgentTrigger(), resolver, "", 10, nil)
	return agentSvc, convSvc
}

func TestConversationService_EnsureQuota(t *testing.T) {
	_, convSvc := setupAgentQuotaTest(t)
	ctx := context.Background()

	// 普通用户默认 VIP0 上限为 100，创建 102 个后应被裁剪到上限
	for i := 0; i < 102; i++ {
		_, err := convSvc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "test"})
		require.NoError(t, err)
	}

	// 最终数量应等于上限
	count, err := convSvc.repo.CountByUser("u1")
	require.NoError(t, err)
	assert.Equal(t, int64(100), count)
}

func TestAgentService_Execute_NonVIPWithAttachment(t *testing.T) {
	agentSvc, convSvc := setupAgentQuotaTest(t)
	ctx := context.Background()

	conv, err := convSvc.CreateConversation(ctx, "u1", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	// 非 VIP 用户开启 Agent 并上传图片附件，应触发升级提示
	var buf bytes.Buffer
	ctxWithUser := context.WithValue(ctx, "user_id", "u1")
	err = agentSvc.Execute(ctxWithUser, AgentExecuteRequest{
		ConversationID: conv.ID,
		Message:        "总结这张图片",
		EnableTools:    boolPtr(true),
		Attachments:    []AgentAttachment{{Type: "image", URL: "https://example.com/a.png"}},
	}, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "升级弹丸VIP")
}

func boolPtr(b bool) *bool { return &b }
