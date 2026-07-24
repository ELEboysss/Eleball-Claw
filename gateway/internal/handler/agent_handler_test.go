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

// agentTestFixture 聚合 Agent 市场测试所需的全部依赖
type agentTestFixture struct {
	r        *gin.Engine
	jwtUtil  *util.JWTUtil
	db       *gorm.DB
	userRepo *repository.UserRepo
}

func setupAgentTest(t *testing.T) *agentTestFixture {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(
		&model.User{}, &model.Device{},
		&model.AgentItem{}, &model.AgentPurchase{},
		&model.AgentReview{}, &model.AgentFavorite{},
		&model.DeveloperAccount{}, &model.WithdrawalRecord{},
		&model.RechargePackage{}, &model.CDK{},
		&model.VIPPlan{}, &model.VIPSubscription{},
	)

	jwtUtil := util.NewJWTUtil("test-secret", 24, 168)
	logger := zap.NewNop()
	cfg := &config.AppConfig{Server: config.ServerConfig{Mode: "test"}, Admin: config.AdminConfig{Enabled: true, AllowedIPs: []string{"127.0.0.1"}}}

	userRepo := repository.NewUserRepo(db)
	vipService := newTestVIPService(db)
	authService := service.NewAuthService(userRepo, jwtUtil, nil, "http://localhost:8080/v1", newTestEleAgentModelService(), nil)
	authHandler := handler.NewAuthHandler(authService, vipService)

	chatService := service.NewChatProxyService(nil, nil, newTestEleAgentModelService(), zap.NewNop())
	billRepo := repository.NewBillingRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	rechargePackageRepo := repository.NewRechargePackageRepo(db)
	cdkRepo := repository.NewCDKRepo(db)
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
	r := router.NewRouter(cfg, logger, jwtUtil, authHandler, chatHandler, billingHandler, syncHandler, paymentHandler, adminHandler, adminKeyHandler, adminEleAgentModelHandler, adminSettingHandler, publicSettingHandler, agentHandler, withdrawalHandler, eleAgentHandler, rechargePackageHandler, cdkHandler, releaseHandler, sttHandler, nil, nil, nil, nil, nil, nil)

	return &agentTestFixture{
		r:        r,
		jwtUtil:  jwtUtil,
		db:       db,
		userRepo: userRepo,
	}
}

// registerUserWithRole 注册一个指定角色的测试用户，返回 accessToken
// 注意：若修改了 role，会重新登录以获取携带新 role 的 JWT。
func registerUserWithRole(t *testing.T, f *agentTestFixture, email, role string) string {
	body, _ := json.Marshal(service.RegisterRequest{
		Username:    email,
		Password: "password123",
		DeviceID: "device-" + email,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	f.r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	accessToken := resp["data"].(map[string]interface{})["access_token"].(string)

	// 通过 /auth/me 获取 user_id，再按需求修改 role
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/auth/me", nil)
	req2.Header.Set("Authorization", "Bearer "+accessToken)
	f.r.ServeHTTP(w2, req2)

	var meResp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &meResp)
	userID := meResp["data"].(map[string]interface{})["user_id"].(string)

	if role != "" {
		f.db.Model(&model.User{}).Where("id = ?", userID).Update("role", role)
		// 重新登录，让 JWT 携带最新 role
		loginBody, _ := json.Marshal(service.LoginRequest{
			Username:    email,
			Password: "password123",
			DeviceID: "device-" + email,
		})
		w3 := httptest.NewRecorder()
		req3, _ := http.NewRequest("POST", "/v1/auth/login", bytes.NewBuffer(loginBody))
		req3.Header.Set("Content-Type", "application/json")
		f.r.ServeHTTP(w3, req3)
		assert.Equal(t, http.StatusOK, w3.Code)

		var loginResp map[string]interface{}
		json.Unmarshal(w3.Body.Bytes(), &loginResp)
		accessToken = loginResp["data"].(map[string]interface{})["access_token"].(string)
	}

	return accessToken
}

func TestAgent_GetCapabilities_UserEnabled(t *testing.T) {
	f := setupAgentTest(t)
	accessToken := registerUserWithRole(t, f, "user_enabled@example.com", model.UserRoleUser)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	f.r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	agentMarket := data["agent_market"].(map[string]interface{})
	assert.Equal(t, true, agentMarket["enabled"])
	assert.Equal(t, "", agentMarket["reason"])
}

func TestAgent_GetCapabilities_AdminEnabled(t *testing.T) {
	f := setupAgentTest(t)
	accessToken := registerUserWithRole(t, f, "admin_enabled@example.com", model.UserRoleAdmin)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	f.r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	agentMarket := data["agent_market"].(map[string]interface{})
	assert.Equal(t, true, agentMarket["enabled"])
	assert.Equal(t, "", agentMarket["reason"])
}
