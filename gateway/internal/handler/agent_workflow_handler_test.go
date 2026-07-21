package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func setupAgentWorkflowTest(t *testing.T) (*gin.Engine, string) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	db.AutoMigrate(&model.User{}, &model.ChatConversation{}, &model.ChatMessage{}, &model.AgentSession{}, &model.AgentSessionOutput{}, &model.BalanceTransaction{}, &model.Order{}, &model.RechargePackage{}, &model.CDK{}, &model.VIPPlan{}, &model.VIPSubscription{})
	require.NoError(t, test.SeedTestData(db))
	// Agent 模式需要 VIP/管理员权限，将种子用户提升为管理员以便测试 session 流程
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", "u_1001").Update("role", model.UserRoleAdmin).Error)

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
	// 显式传入 nil LLM 客户端，模拟未配置 API Key 场景
	agentWorkflowService := service.NewAgentService(conversationService, repository.NewAgentSessionRepo(db), userRepo, vipService, nil, service.NewNoOpEleAgentModelService(), service.NewFileSandbox("", ""), service.NewToolRegistry(), service.NewToolSchemaBuilder(service.NewToolRegistry()), service.NewAgentTrigger(), nil, "", 10, logger)
	conversationHandler := handler.NewConversationHandler(conversationService, agentWorkflowService)
	agentWorkflowHandler := handler.NewAgentWorkflowHandler(agentWorkflowService)

	r := router.NewRouter(cfg, logger, jwtUtil, authHandler, chatHandler, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, conversationHandler, nil, agentWorkflowHandler, nil, nil, nil)

	accessToken, _ := jwtUtil.GenerateAccessToken("u_1001", "test-device", model.UserRoleAdmin)
	return r, accessToken
}

func TestAgentWorkflow_ExecuteWithoutLLMClient(t *testing.T) {
	r, token := setupAgentWorkflowTest(t)

	body, _ := json.Marshal(map[string]interface{}{
		"title":    "agent-test",
		"model":    "gpt-4o-mini",
		"provider": "openai",
		"message":  "hello",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/agent/execute", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	bodyStr := w.Body.String()
	assert.True(t, strings.Contains(bodyStr, "Agent LLM 客户端未初始化"))
	assert.True(t, strings.Contains(bodyStr, "event: done"))
}

func TestAgentWorkflow_Sessions(t *testing.T) {
	r, token := setupAgentWorkflowTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/agent/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(0), data["total"])
}
