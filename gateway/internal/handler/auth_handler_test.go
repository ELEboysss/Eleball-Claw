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

func setupAuthTest(t *testing.T) (*gin.Engine, *util.JWTUtil) {
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
	authService := service.NewAuthService(userRepo, jwtUtil, nil, "http://localhost:8080/v1", newTestEleAgentModelService(), nil)
	authHandler := handler.NewAuthHandler(authService, vipService)

	// 只注册公开路由（无需其他 handler）
	r := gin.New()
	r.Use(gin.Recovery())
	r.POST("/v1/auth/register", authHandler.Register)
	r.POST("/v1/auth/login", authHandler.Login)
	r.POST("/v1/auth/refresh", authHandler.Refresh)

	// billing 等 handler 需要完整路由，这里直接用完整路由
	chatService := service.NewChatProxyService(nil, nil, newTestEleAgentModelService(), zap.NewNop())
	billingService := service.NewBillingService(userRepo, billRepo, newTestEleAgentModelService(), nil, vipService)
	billingHandler := handler.NewBillingHandler(billingService)
	chatHandler := handler.NewChatHandler(chatService, billingService, zap.NewNop())
	syncHandler := handler.NewSyncHandler(repository.NewConversationRepo(db))
	paymentService := service.NewPaymentService(db, userRepo, rechargePackageRepo, orderRepo, billRepo, vipService, nil)
	paymentHandler := handler.NewPaymentHandler(paymentService)
	adminHandler := handler.NewAdminHandler(service.NewAdminService(db, userRepo, repository.NewBillingRepo(db), repository.NewOrderRepo(db), nil, vipService))
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
	r = router.NewRouter(cfg, logger, jwtUtil, authHandler, chatHandler, billingHandler, syncHandler, paymentHandler, adminHandler, adminKeyHandler, adminEleAgentModelHandler, adminSettingHandler, publicSettingHandler, agentHandler, withdrawalHandler, eleAgentHandler, rechargePackageHandler, cdkHandler, releaseHandler, sttHandler, nil, nil, nil, nil, nil, nil)

	return r, jwtUtil
}

func TestAuth_Register(t *testing.T) {
	r, _ := setupAuthTest(t)

	body, _ := json.Marshal(service.RegisterRequest{
		Username:    "newuser@example.com",
		Password: "password123",
		DeviceID: "device-001",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	assert.NotNil(t, resp["data"])
	data := resp["data"].(map[string]interface{})
	assert.NotEmpty(t, data["access_token"])
	assert.NotEmpty(t, data["refresh_token"])
	assert.NotEmpty(t, data["user_id"])
	assert.NotNil(t, data["default_model_profile"])
	defaultProfile := data["default_model_profile"].(map[string]interface{})
	assert.Equal(t, "eleagent", defaultProfile["provider"])
	assert.Equal(t, "qwen/Qwen/Qwen3.5-4B", defaultProfile["model_name"])
	assert.NotEmpty(t, defaultProfile["api_key"])
}

func TestAuth_RegisterDuplicateUsername(t *testing.T) {
	r, _ := setupAuthTest(t)

	body, _ := json.Marshal(service.RegisterRequest{
		Username:    "dup@example.com",
		Password: "password123",
		DeviceID: "device-001",
	})

	// 第一次注册
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(body))
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// 重复注册
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(body))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusBadRequest, w2.Code)
	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.Equal(t, float64(1000), resp["code"])
}

func TestAuth_Login(t *testing.T) {
	r, _ := setupAuthTest(t)

	// 先注册
	regBody, _ := json.Marshal(service.RegisterRequest{
		Username:    "login@example.com",
		Password: "mypassword",
		DeviceID: "device-002",
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(regBody))
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// 再登录
	loginBody, _ := json.Marshal(service.LoginRequest{
		Username:    "login@example.com",
		Password: "mypassword",
		DeviceID: "device-002",
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/auth/login", bytes.NewBuffer(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	assert.NotEmpty(t, data["access_token"])
	assert.NotNil(t, data["default_model_profile"])
}

func TestAuth_LoginWrongPassword(t *testing.T) {
	r, _ := setupAuthTest(t)

	// 先注册
	regBody, _ := json.Marshal(service.RegisterRequest{
		Username:    "wrong@example.com",
		Password: "correctpass",
		DeviceID: "device-003",
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(regBody))
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)

	// 错误密码登录
	loginBody, _ := json.Marshal(service.LoginRequest{
		Username:    "wrong@example.com",
		Password: "wrongpass",
		DeviceID: "device-003",
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/auth/login", bytes.NewBuffer(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusUnauthorized, w2.Code)
}

func TestAuth_RefreshToken(t *testing.T) {
	r, _ := setupAuthTest(t)

	// 注册并获取 refresh_token
	regBody, _ := json.Marshal(service.RegisterRequest{
		Username:    "refresh@example.com",
		Password: "password123",
		DeviceID: "device-004",
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(regBody))
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)

	var regResp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &regResp)
	refreshToken := regResp["data"].(map[string]interface{})["refresh_token"].(string)

	// 刷新
	refreshBody, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/auth/refresh", bytes.NewBuffer(refreshBody))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	assert.NotEmpty(t, data["access_token"])
}

func TestAuth_Me(t *testing.T) {
	r, _ := setupAuthTest(t)

	// 注册并获取 access_token
	regBody, _ := json.Marshal(service.RegisterRequest{
		Username: "meuser",
		Password: "password123",
		DeviceID: "device-005",
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(regBody))
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)

	var regResp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &regResp)
	accessToken := regResp["data"].(map[string]interface{})["access_token"].(string)

	// 获取当前用户信息
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/auth/me", nil)
	req2.Header.Set("Authorization", "Bearer "+accessToken)
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, "meuser", data["username"])
	assert.NotEmpty(t, data["user_id"])

	// 无 Token 访问应返回 401
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/v1/auth/me", nil)
	r.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusUnauthorized, w3.Code)
}
