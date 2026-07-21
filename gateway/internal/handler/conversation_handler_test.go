package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	handler "github.com/eleball/gateway/internal/handler"
	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/router"
	"github.com/eleball/gateway/internal/service"
	"github.com/eleball/gateway/pkg/util"
	"github.com/eleball/gateway/test"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupConversationTest(t *testing.T) (*gin.Engine, *util.JWTUtil, string) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// 自动迁移新增表及 seed 所需表
	db.AutoMigrate(&model.User{}, &model.ChatConversation{}, &model.ChatMessage{}, &model.AgentSession{}, &model.AgentSessionOutput{}, &model.BalanceTransaction{}, &model.Order{}, &model.RechargePackage{}, &model.CDK{}, &model.VIPPlan{}, &model.VIPSubscription{})
	require.NoError(t, test.SeedTestData(db))

	jwtUtil := util.NewJWTUtil("test-secret", 24, 168)
	logger := zap.NewNop()
	cfg := &config.AppConfig{
		Server: config.ServerConfig{Mode: "test"},
		Admin:  config.AdminConfig{Enabled: true, AllowedIPs: []string{"127.0.0.1"}},
		Agent:  config.AgentConfig{Enabled: true},
	}

	userRepo := repository.NewUserRepo(db)
	conversationRepo := repository.NewChatConversationRepo(db)
	vipService := newTestVIPService(db)
	conversationService := service.NewConversationService(conversationRepo, vipService, "")

	authHandler := handler.NewAuthHandler(service.NewAuthService(userRepo, jwtUtil, nil, "", newTestEleAgentModelService(), nil), vipService)
	chatHandler := handler.NewChatHandler(service.NewChatProxyService(nil, nil, newTestEleAgentModelService()), nil, nil)
	agentWorkflowService := service.NewAgentService(conversationService, repository.NewAgentSessionRepo(db), userRepo, vipService, nil, service.NewNoOpEleAgentModelService(), service.NewFileSandbox("", ""), service.NewToolRegistry(), service.NewToolSchemaBuilder(service.NewToolRegistry()), service.NewAgentTrigger(), nil, "", 10, logger)
	conversationHandler := handler.NewConversationHandler(conversationService, agentWorkflowService)
	agentWorkflowHandler := handler.NewAgentWorkflowHandler(agentWorkflowService)

	r := router.NewRouter(cfg, logger, jwtUtil, authHandler, chatHandler, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, conversationHandler, nil, agentWorkflowHandler, nil, nil, nil)

	// 使用普通用户 alice 的 token
	accessToken, _ := jwtUtil.GenerateAccessToken("u_1001", "test-device", model.UserRoleUser)
	return r, jwtUtil, accessToken
}

func TestConversation_CreateAndList(t *testing.T) {
	r, _, token := setupConversationTest(t)

	body, _ := json.Marshal(map[string]interface{}{
		"title":        "测试对话",
		"model":        "gpt-4o-mini",
		"provider":     "openai",
		"enable_tools": true,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/conversations", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var createResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	assert.Equal(t, float64(0), createResp["code"])
	assert.NotNil(t, createResp["data"])

	// 列表查询
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/conversations", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	var listResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &listResp))
	assert.Equal(t, float64(0), listResp["code"])
	data := listResp["data"].(map[string]interface{})
	assert.Equal(t, float64(1), data["total"])
}

func TestConversation_MessageCRUD(t *testing.T) {
	r, _, token := setupConversationTest(t)

	// 创建对话
	body, _ := json.Marshal(map[string]interface{}{
		"title":    "msg-test",
		"model":    "gpt-4o-mini",
		"provider": "openai",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/conversations", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	var createResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	convID := createResp["data"].(map[string]interface{})["id"].(string)

	// 创建消息
	msgBody, _ := json.Marshal(map[string]interface{}{
		"role":              "user",
		"content":           "hello",
		"client_message_id": "c-1",
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/conversations/"+convID+"/messages", bytes.NewBuffer(msgBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)

	// 列表查询消息
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/v1/conversations/"+convID+"/messages", nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusOK, w3.Code)

	var listResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &listResp))
	data := listResp["data"].(map[string]interface{})
	assert.Equal(t, float64(1), data["total"])
}

func TestConversation_ClientMessageIDDedup(t *testing.T) {
	r, _, token := setupConversationTest(t)

	// 创建对话
	body, _ := json.Marshal(map[string]interface{}{"title": "dedup-test", "model": "gpt-4o-mini", "provider": "openai"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/conversations", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	var createResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	convID := createResp["data"].(map[string]interface{})["id"].(string)

	// 第一次创建消息
	msgBody, _ := json.Marshal(map[string]interface{}{
		"role": "user", "content": "first", "client_message_id": "c-1",
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/conversations/"+convID+"/messages", bytes.NewBuffer(msgBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)

	// 第二次用相同 client_message_id 创建消息（应更新内容，不新增）
	msgBody2, _ := json.Marshal(map[string]interface{}{
		"role": "user", "content": "second", "client_message_id": "c-1",
	})
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("POST", "/v1/conversations/"+convID+"/messages", bytes.NewBuffer(msgBody2))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusOK, w3.Code)

	// 查询消息列表，总数应为 1
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest("GET", "/v1/conversations/"+convID+"/messages", nil)
	req4.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w4, req4)
	var listResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w4.Body.Bytes(), &listResp))
	data := listResp["data"].(map[string]interface{})
	assert.Equal(t, float64(1), data["total"])
	items := data["items"].([]interface{})
	require.Len(t, items, 1)
	assert.Equal(t, "second", items[0].(map[string]interface{})["content"])
}

func TestConversation_SoftDelete(t *testing.T) {
	r, _, token := setupConversationTest(t)

	// 创建对话
	body, _ := json.Marshal(map[string]interface{}{"title": "delete-test", "model": "gpt-4o-mini", "provider": "openai"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/conversations", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	var createResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	convID := createResp["data"].(map[string]interface{})["id"].(string)

	// 删除对话
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("DELETE", "/v1/conversations/"+convID, nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)

	// 列表查询应为空
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/v1/conversations", nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w3, req3)
	var listResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &listResp))
	data := listResp["data"].(map[string]interface{})
	assert.Equal(t, float64(0), data["total"])
}
