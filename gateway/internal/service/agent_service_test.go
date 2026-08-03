package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentService(t *testing.T) (*AgentService, *ConversationService, *mockAgentLLM) {
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

	mockClient := &mockAgentLLM{}
	resolver := func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return mockClient, nil
	}
	agentSvc := NewAgentService(convSvc, sessionRepo, userRepo, vipService, nil, NewNoOpEleAgentModelService(), NewFileSandbox(t.TempDir(), ""), NewToolRegistry(), NewToolSchemaBuilder(NewToolRegistry()), NewAgentTrigger(), resolver, "", 10, nil)
	return agentSvc, convSvc, mockClient
}

// TestAgentService_GetResource_CwdReadBack AR-27：claw（unrestricted）下 WriteFile 写到 cwd 内，
// DiskPath 是 cwd 下绝对路径。GetResource 须按所属 session 的 cwd 克隆沙箱读回真实字节，
// 否则共享沙箱单例（projectRoot 为空）拒绝读取 -> 下载返回 JSON 404（浏览器存成 ar-xxxxx.json）。
func TestAgentService_GetResource_CwdReadBack(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	agentSvc.SetUnrestricted(true) // claw 模式：启用 cwd 解析

	cwd := t.TempDir()
	outPath := filepath.Join(cwd, "out.md")
	require.NoError(t, os.WriteFile(outPath, []byte("# hello"), 0o640))

	// session 记录 cwd；output 记录 cwd 内绝对路径（模拟 toolWriteFile 的 SaveOutput）
	require.NoError(t, agentSvc.sessionRepo.Create(&model.AgentSession{ID: "s1", UserID: "u1", Status: "succeeded", Cwd: cwd}))
	require.NoError(t, agentSvc.sessionRepo.SaveOutput(&model.AgentSessionOutput{
		ID: "o1", SessionID: "s1", ResourceID: "ar-test1234",
		FileName: "out.md", MimeType: "text/markdown", DiskPath: outPath,
	}))

	data, mime, name, err := agentSvc.GetResource(context.Background(), "ar-test1234")
	require.NoError(t, err)
	assert.Equal(t, "# hello", string(data))
	assert.Equal(t, "text/markdown", mime)
	assert.Equal(t, "out.md", name)
}

func TestAgentService_Execute_Unauthorized(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	var buf bytes.Buffer
	ctx := context.Background()
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "hello"}, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "未登录")
	assert.Contains(t, buf.String(), "event: done")
}

func TestAgentService_Execute_ToolsDisabled(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u1")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "hello", EnableTools: boolPtr(false)}, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "event: final_answer")
	assert.Contains(t, buf.String(), "stream")
}

func TestAgentService_Execute_ResolverError(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	agentSvc.clientResolver = func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return nil, fmt.Errorf("自定义模型需传入 base_url 与 api_key")
	}
	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u1")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "hello", Provider: "openai", EnableTools: boolPtr(false)}, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "自定义模型需传入 base_url 与 api_key")
}

func TestAgentService_Execute_NonVIPWithServerTool(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u1")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "读取文件 test.txt", EnableTools: boolPtr(true)}, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "升级弹丸VIP")
}

func TestAgentService_Execute_BYOKAgentNotSupported(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u2")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "hello", Provider: "openai", EnableTools: boolPtr(true)}, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Agent 工具当前仅支持 Ele Agent 模型")
}

func TestAgentService_Execute_VIPDirectAnswer(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	client := &mockAgentLLM{responses: []llm.ChatChunk{{Delta: "hello"}}}
	agentSvc.clientResolver = func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return client, nil
	}

	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u2")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "hi", EnableTools: boolPtr(true)}, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "event: final_answer")
	assert.Contains(t, buf.String(), "hello")
}

func TestAgentService_Execute_VIPToolCall(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	registry := NewToolRegistryWithDeps(&mockRunner{shellOutput: "ok"}, &mockSearchProvider{result: map[string]interface{}{"results": []string{"r1"}}})
	agentSvc.registry = registry
	agentSvc.schemaBuilder = NewToolSchemaBuilder(registry)
	client := &mockAgentLLM{
		responses: []llm.ChatChunk{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "Shell", Arguments: `{"command":"echo","args":["hello"]}`}},
				},
			},
			{Delta: "done"},
		},
	}
	agentSvc.clientResolver = func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return client, nil
	}

	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u2")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "run shell", EnableTools: boolPtr(true)}, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "event: tool_call")
	assert.Contains(t, buf.String(), "event: tool_result")
	assert.Contains(t, buf.String(), "Shell")
}

func TestAgentService_ListSessions(t *testing.T) {
	agentSvc, convSvc, _ := setupAgentService(t)
	ctx := context.Background()
	conv, err := convSvc.CreateConversation(ctx, "u2", CreateConversationReq{Title: "t"})
	require.NoError(t, err)

	_, err = agentSvc.createSession(ctx, "u2", conv.ID, "test", "")
	require.NoError(t, err)

	items, total, err := agentSvc.ListSessions(ctx, "u2", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, items, 1)
}

func TestAgentService_GetSession_OwnerCheck(t *testing.T) {
	agentSvc, convSvc, _ := setupAgentService(t)
	ctx := context.Background()
	conv, err := convSvc.CreateConversation(ctx, "u2", CreateConversationReq{Title: "t"})
	require.NoError(t, err)
	session, err := agentSvc.createSession(ctx, "u2", conv.ID, "test", "")
	require.NoError(t, err)

	_, err = agentSvc.GetSession(ctx, session.ID, "u1")
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "无权访问"))

	item, err := agentSvc.GetSession(ctx, session.ID, "u2")
	require.NoError(t, err)
	assert.Equal(t, session.ID, item.ID)
}

func TestAgentService_DeleteSession(t *testing.T) {
	agentSvc, convSvc, _ := setupAgentService(t)
	ctx := context.Background()
	conv, err := convSvc.CreateConversation(ctx, "u2", CreateConversationReq{Title: "t"})
	require.NoError(t, err)
	session, err := agentSvc.createSession(ctx, "u2", conv.ID, "test", "")
	require.NoError(t, err)

	err = agentSvc.DeleteSession(ctx, session.ID, "u2")
	require.NoError(t, err)

	_, err = agentSvc.GetSession(ctx, session.ID, "u2")
	assert.Error(t, err)
}

func TestAgentService_buildInitialMessages(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	msgs, ids := agentSvc.buildInitialMessages(context.Background(), AgentExecuteRequest{Message: "hello", History: []AgentHistoryMessage{{Message: llm.Message{Role: "user", Content: "prev"}, ID: "msg-prev"}}}, nil, "u1", "", "", false, false, model.PermissionModeDefault, "")
	require.Len(t, msgs, 3)
	require.Len(t, ids, 3)
	assert.Equal(t, "system", msgs[0].Role)
	assert.Equal(t, "", ids[0])
	assert.Equal(t, "prev", msgs[1].Content)
	assert.Equal(t, "msg-prev", ids[1])
	assert.Equal(t, "hello", msgs[2].Content)
	assert.Equal(t, "", ids[2])
}

func TestAgentService_buildInitialMessages_ToolsEnabled(t *testing.T) {
	// AR-26：toolsEnabled=true 时 system prompt 含 FunctionGet 拉取说明；false 时不含。
	agentSvc, _, _ := setupAgentService(t)
	msgs, _ := agentSvc.buildInitialMessages(context.Background(), AgentExecuteRequest{Message: "hi"}, nil, "u1", "", "", true, false, model.PermissionModeDefault, "")
	require.NotEmpty(t, msgs)
	sys, ok := msgs[0].Content.(string)
	require.True(t, ok)
	assert.Contains(t, sys, "FunctionGet")
	assert.Contains(t, sys, "FunctionCallBegin")

	msgs2, _ := agentSvc.buildInitialMessages(context.Background(), AgentExecuteRequest{Message: "hi"}, nil, "u1", "", "", false, false, model.PermissionModeDefault, "")
	sys2, _ := msgs2[0].Content.(string)
	assert.NotContains(t, sys2, "FunctionGet")
}

func TestAgentService_normalizeRequest(t *testing.T) {
	req := AgentExecuteRequest{Content: "hello"}
	req.normalize()
	assert.Equal(t, "hello", req.Message)
}
