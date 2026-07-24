package handler_test

import (
	"bytes"
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
	"github.com/eleball/gateway/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupAdminKeyTest(t *testing.T) (*gin.Engine, *util.JWTUtil, string) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.User{}, &model.Device{}, &model.ProviderApiKey{}, &model.RechargePackage{}, &model.CDK{}, &model.VIPPlan{}, &model.VIPSubscription{})

	jwtUtil := util.NewJWTUtil("test-secret", 24, 168)
	logger := zap.NewNop()
	cfg := &config.AppConfig{Server: config.ServerConfig{Mode: "test"}, Admin: config.AdminConfig{Enabled: true, AllowedIPs: []string{"127.0.0.1"}}}

	userRepo := repository.NewUserRepo(db)
	apiKeyRepo := repository.NewApiKeyRepo(db)
	billRepo := repository.NewBillingRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	rechargePackageRepo := repository.NewRechargePackageRepo(db)
	cdkRepo := repository.NewCDKRepo(db)
	vipService := newTestVIPService(db)

	// 创建管理员用户
	adminUser := &model.User{
		ID:       "admin-001",
		Username:    "admin@example.com",
		Nickname: "Admin",
		Role:     model.UserRoleAdmin,
		Status:   1,
	}
	db.Create(adminUser)

	masterKey := "0000000000000000000000000000000000000000000000000000000000000000"
	keyManager, err := service.NewKeyManagerService(apiKeyRepo, masterKey)
	if err != nil {
		t.Fatal(err)
	}

	authService := service.NewAuthService(userRepo, jwtUtil, nil, "http://localhost:8080/v1", newTestEleAgentModelService(), nil)
	billingService := service.NewBillingService(userRepo, repository.NewBillingRepo(db), newTestEleAgentModelService(), nil, vipService)
	chatService := service.NewChatProxyService(nil, nil, newTestEleAgentModelService(), zap.NewNop())

	authHandler := handler.NewAuthHandler(authService, vipService)
	billingHandler := handler.NewBillingHandler(billingService)
	chatHandler := handler.NewChatHandler(chatService, billingService, zap.NewNop())
	syncHandler := handler.NewSyncHandler(repository.NewConversationRepo(db))
	paymentService := service.NewPaymentService(db, userRepo, rechargePackageRepo, orderRepo, billRepo, vipService, nil)
	paymentHandler := handler.NewPaymentHandler(paymentService)
	adminHandler := handler.NewAdminHandler(service.NewAdminService(db, userRepo, repository.NewBillingRepo(db), repository.NewOrderRepo(db), nil, vipService))
	adminKeyHandler := handler.NewAdminKeyHandler(keyManager)
	agentHandler := handler.NewAgentHandler(service.NewAgentMarketService(db, repository.NewAgentRepo(db), userRepo, vipService, nil))
	withdrawalHandler := handler.NewWithdrawalHandler(service.NewWithdrawalService(db, repository.NewWithdrawalRepo(db), repository.NewAgentRepo(db), service.NewMockPaymentProvider()))
	eleAgentModelService := newTestEleAgentModelService()
	eleAgentHandler := handler.NewEleAgentHandler(service.NewEleAgentService(chatService, eleAgentModelService, nil, "http://localhost:8080/v1"), eleAgentModelService)

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

	accessToken, _ := jwtUtil.GenerateAccessToken(adminUser.ID, "test-device", adminUser.Role)
	return r, jwtUtil, accessToken
}

func TestAdminKey_CreateAndList(t *testing.T) {
	r, _, token := setupAdminKeyTest(t)

	// 创建 Key
	createBody, _ := json.Marshal(handler.CreateKeyRequest{
		Provider: "openai",
		Name:     "OpenAI-Test",
		ApiKey:   "sk-test-key",
		Priority: 1,
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/v1/admin/keys", bytes.NewBuffer(createBody))
	req1.Header.Set("Authorization", "Bearer "+token)
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)

	assert.Equal(t, http.StatusOK, w1.Code)
	var createResp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &createResp)
	assert.Equal(t, float64(0), createResp["code"])

	// 列表查询
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/admin/keys", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	var listResp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &listResp)
	assert.Equal(t, float64(0), listResp["code"])
	data := listResp["data"].(map[string]interface{})
	assert.Equal(t, float64(1), data["total"])
}

func TestAdminKey_ListProviders(t *testing.T) {
	r, _, token := setupAdminKeyTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/admin/keys/providers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
}

func TestAdminKey_Unauthorized(t *testing.T) {
	r, _, _ := setupAdminKeyTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/admin/keys", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
