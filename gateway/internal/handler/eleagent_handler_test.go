package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/handler"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/router"
	"github.com/eleball/gateway/internal/service"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/eleball/gateway/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupEleAgentHandlerTest(t *testing.T) (*gin.Engine, string) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.User{}, &model.Device{}, &model.RechargePackage{}, &model.CDK{}, &model.VIPPlan{}, &model.VIPSubscription{})

	jwtUtil := util.NewJWTUtil("test-secret", 24, 168)
	logger := zap.NewNop()
	cfg := &config.AppConfig{Server: config.ServerConfig{Mode: "test"}, Admin: config.AdminConfig{Enabled: true, AllowedIPs: []string{"127.0.0.1"}}}

	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	rechargePackageRepo := repository.NewRechargePackageRepo(db)
	cdkRepo := repository.NewCDKRepo(db)
	vipService := newTestVIPService(db)

	testUser := &model.User{
		ID:       "user-eleagent-001",
		Username:    "eleagent@example.com",
		Nickname: "EleAgentUser",
		Role:     model.UserRoleUser,
		Balance:  100000,
		Status:   1,
	}
	db.Create(testUser)

	authService := service.NewAuthService(userRepo, jwtUtil, nil, "http://localhost:8080/v1", newTestEleAgentModelService(), nil)
	billingService := service.NewBillingService(userRepo, billRepo, newTestEleAgentModelService(), nil, vipService)
	chatService := service.NewChatProxyService(nil, nil, newTestEleAgentModelService(), zap.NewNop())
	// 注册 openai 子平台客户端，使凭证接口能校验通过
	chatService.RegisterFallbackClient(llm.ProviderOpenAI, &mockLLMClient{})

	authHandler := handler.NewAuthHandler(authService, vipService)
	billingHandler := handler.NewBillingHandler(billingService)
	chatHandler := handler.NewChatHandler(chatService, billingService, zap.NewNop())
	syncHandler := handler.NewSyncHandler(repository.NewConversationRepo(db))
	paymentService := service.NewPaymentService(db, userRepo, rechargePackageRepo, orderRepo, billRepo, vipService, nil)
	paymentHandler := handler.NewPaymentHandler(paymentService)
	adminHandler := handler.NewAdminHandler(service.NewAdminService(db, userRepo, billRepo, repository.NewOrderRepo(db), nil, vipService))
	agentHandler := handler.NewAgentHandler(service.NewAgentMarketService(db, repository.NewAgentRepo(db), userRepo, vipService, nil))
	withdrawalHandler := handler.NewWithdrawalHandler(service.NewWithdrawalService(db, repository.NewWithdrawalRepo(db), repository.NewAgentRepo(db), service.NewMockPaymentProvider()))
	eleAgentModelService := newTestEleAgentModelService()
	eleAgentHandler := handler.NewEleAgentHandler(service.NewEleAgentService(chatService, eleAgentModelService, nil, "http://localhost:8080/v1"), eleAgentModelService)

	adminKeyHandler := handler.NewAdminKeyHandler(nil)
	adminEleAgentModelHandler := handler.NewAdminEleAgentModelHandler(newTestEleAgentModelService())
publicSettingHandler := handler.NewPublicSettingHandler(service.NewSettingService(repository.NewSettingRepo(db)))
	adminSettingHandler := handler.NewAdminSettingHandler(service.NewSettingService(repository.NewSettingRepo(db)))
	rechargePackageService := service.NewRechargePackageService(rechargePackageRepo)
	rechargePackageHandler := handler.NewRechargePackageHandler(rechargePackageService)
	cdkService := service.NewCDKService(cdkRepo, userRepo, billRepo, vipService)
	cdkHandler := handler.NewCDKHandler(cdkService)
	releaseService := service.NewReleaseService("releases")
	releaseHandler := handler.NewReleaseHandler(releaseService, logger)
	sttService := service.NewSttService("", "", "", "", "", 0, 0, logger)
	sttHandler := handler.NewSttHandler(sttService, userRepo, vipService, logger)
	r := router.NewRouter(cfg, logger, jwtUtil, authHandler, chatHandler, billingHandler, syncHandler, paymentHandler, adminHandler, adminKeyHandler, adminEleAgentModelHandler, adminSettingHandler, publicSettingHandler, agentHandler, withdrawalHandler, eleAgentHandler, rechargePackageHandler, cdkHandler, releaseHandler, sttHandler, nil, nil, nil, nil, nil, nil)

	accessToken, _ := jwtUtil.GenerateAccessToken(testUser.ID, "test-device", testUser.Role)
	return r, accessToken
}

// mockLLMClient 模拟 LLM 客户端，复用 chat_proxy_service_test 中的模式
type mockLLMClient struct {
	response string
	usage    *llm.Usage
	err      error
}

func (m *mockLLMClient) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
	return &llm.ChatChunk{Delta: m.response, Usage: m.usage}, m.err
}

func (m *mockLLMClient) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	return nil, nil
}

func TestEleAgent_GetCredentials_Success(t *testing.T) {
	r, token := setupEleAgentHandlerTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/eleagent/credentials?subProvider=openai&subModel=gpt-4o", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, "http://localhost:8080/v1", data["baseUrl"])
	assert.NotEmpty(t, data["apiKey"])
	assert.NotEmpty(t, data["expiresAt"])
}

func TestEleAgent_GetCredentials_MissingParams(t *testing.T) {
	r, token := setupEleAgentHandlerTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/eleagent/credentials?subProvider=openai", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(4001), resp["code"])
}

func TestEleAgent_GetCredentials_UnsupportedProvider(t *testing.T) {
	r, token := setupEleAgentHandlerTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/eleagent/credentials?subProvider=nonexist&subModel=model", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(4001), resp["code"])
}

func TestEleAgent_GetCredentials_Unauthorized(t *testing.T) {
	r, _ := setupEleAgentHandlerTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/eleagent/credentials?subProvider=openai&subModel=gpt-4o", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
