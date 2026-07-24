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

func setupBillingTest(t *testing.T) (*gin.Engine, *util.JWTUtil, string) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.User{}, &model.Device{}, &model.BalanceTransaction{}, &model.RechargePackage{}, &model.CDK{}, &model.VIPPlan{}, &model.VIPSubscription{})

	jwtUtil := util.NewJWTUtil("test-secret", 24, 168)
	logger := zap.NewNop()
	cfg := &config.AppConfig{Server: config.ServerConfig{Mode: "test"}, Admin: config.AdminConfig{Enabled: true, AllowedIPs: []string{"127.0.0.1"}}}

	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	rechargePackageRepo := repository.NewRechargePackageRepo(db)
	cdkRepo := repository.NewCDKRepo(db)
	vipService := newTestVIPService(db)

	// 创建一个测试用户并充值
	testUser := &model.User{
		ID:       "user-test-001",
		Username:    "rich@example.com",
		Nickname: "Rich",
		Role:     model.UserRoleUser,
		Balance:  100000, // 1000 元
		Status:   1,
	}
	db.Create(testUser)

	authService := service.NewAuthService(userRepo, jwtUtil, nil, "http://localhost:8080/v1", newTestEleAgentModelService(), nil)
	billingService := service.NewBillingService(userRepo, billRepo, newTestEleAgentModelService(), nil, vipService)
	chatService := service.NewChatProxyService(nil, nil, newTestEleAgentModelService(), zap.NewNop())

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

	// 直接生成 token（测试用户无密码）
	accessToken, _ := jwtUtil.GenerateAccessToken(testUser.ID, "test-device", testUser.Role)

	return r, jwtUtil, accessToken
}

func TestBilling_GetBalance(t *testing.T) {
	r, _, token := setupBillingTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/billing/balance", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(100000), data["danwan"])
}

func TestBilling_GetBalanceUnauthorized(t *testing.T) {
	r, _, _ := setupBillingTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/billing/balance", nil)
	// 不携带 Token
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestBilling_RechargeByAdmin(t *testing.T) {
	r, jwtUtil, _ := setupBillingTest(t)

	// 获取管理员 Token（admin_handler_test 中已验证管理员登录流程）
	adminToken, _ := jwtUtil.GenerateAccessToken("admin-001", "device", "admin")

	body, _ := json.Marshal(map[string]interface{}{
		"user_id": "user-test-001",
		"amount":  5000,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/admin/billing/recharge", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
}
