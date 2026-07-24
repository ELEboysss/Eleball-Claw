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
	"go.uber.org/zap"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupAdminTest 初始化测试环境
func setupAdminTest(t *testing.T) (*gin.Engine, *util.JWTUtil, string) {
	gin.SetMode(gin.TestMode)

	// 内存数据库
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	// 自动迁移
	db.AutoMigrate(&model.User{}, &model.Device{}, &model.Conversation{}, &model.TokenUsage{}, &model.BalanceTransaction{}, &model.Order{}, &model.RechargePackage{}, &model.CDK{}, &model.VIPPlan{}, &model.VIPSubscription{})

	// 种子数据
	if err := test.SeedTestData(db); err != nil {
		t.Fatal(err)
	}

	// 基础设施
	jwtUtil := util.NewJWTUtil("test-secret", 24, 168)
	logger := zap.NewNop()
	cfg := &config.AppConfig{Server: config.ServerConfig{Mode: "test"}, Admin: config.AdminConfig{Enabled: true, AllowedIPs: []string{"127.0.0.1"}}}

	// 仓库
	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	rechargePackageRepo := repository.NewRechargePackageRepo(db)
	cdkRepo := repository.NewCDKRepo(db)
	conversationRepo := repository.NewConversationRepo(db)
	vipService := newTestVIPService(db)

	// 服务
	authService := service.NewAuthService(userRepo, jwtUtil, nil, "http://localhost:8080/v1", newTestEleAgentModelService(), nil)
	chatService := service.NewChatProxyService(nil, nil, newTestEleAgentModelService(), zap.NewNop())
	billingService := service.NewBillingService(userRepo, billRepo, newTestEleAgentModelService(), nil, vipService)
	paymentService := service.NewPaymentService(db, userRepo, rechargePackageRepo, orderRepo, billRepo, vipService, nil)
	adminService := service.NewAdminService(db, userRepo, billRepo, orderRepo, nil, vipService)

	// Handler
	authHandler := handler.NewAuthHandler(authService, vipService)
	chatHandler := handler.NewChatHandler(chatService, billingService, zap.NewNop())
	billingHandler := handler.NewBillingHandler(billingService)
	syncHandler := handler.NewSyncHandler(conversationRepo)
	paymentHandler := handler.NewPaymentHandler(paymentService)
	adminHandler := handler.NewAdminHandler(adminService)
	agentHandler := handler.NewAgentHandler(service.NewAgentMarketService(db, repository.NewAgentRepo(db), userRepo, vipService, nil))
	withdrawalHandler := handler.NewWithdrawalHandler(service.NewWithdrawalService(db, repository.NewWithdrawalRepo(db), repository.NewAgentRepo(db), service.NewMockPaymentProvider()))
	eleAgentModelService := newTestEleAgentModelService()
	eleAgentHandler := handler.NewEleAgentHandler(service.NewEleAgentService(chatService, eleAgentModelService, nil, "http://localhost:8080/v1"), eleAgentModelService)

	rechargePackageService := service.NewRechargePackageService(rechargePackageRepo)
	rechargePackageHandler := handler.NewRechargePackageHandler(rechargePackageService)
	cdkService := service.NewCDKService(cdkRepo, userRepo, billRepo, vipService)
	cdkHandler := handler.NewCDKHandler(cdkService)

	// 路由
	adminKeyHandler := handler.NewAdminKeyHandler(nil)
	adminEleAgentModelHandler := handler.NewAdminEleAgentModelHandler(newTestEleAgentModelService())
publicSettingHandler := handler.NewPublicSettingHandler(service.NewSettingService(repository.NewSettingRepo(db)))
	adminSettingHandler := handler.NewAdminSettingHandler(service.NewSettingService(repository.NewSettingRepo(db)))
	releaseService := service.NewReleaseService("releases")
	releaseHandler := handler.NewReleaseHandler(releaseService, logger)
	sttService := service.NewSttService("", "", "", "", "", 0, 0, logger)
	sttHandler := handler.NewSttHandler(sttService, userRepo, vipService, logger)
	r := router.NewRouter(cfg, logger, jwtUtil, authHandler, chatHandler, billingHandler, syncHandler, paymentHandler, adminHandler, adminKeyHandler, adminEleAgentModelHandler, adminSettingHandler, publicSettingHandler, agentHandler, withdrawalHandler, eleAgentHandler, rechargePackageHandler, cdkHandler, releaseHandler, sttHandler, nil, nil, nil, nil, nil, nil)

	// 获取管理员 Token
	adminUser, err := userRepo.GetByUsername("admin")
	if err != nil {
		t.Fatalf("获取管理员用户失败: %v", err)
	}

	// 种子数据中管理员密码为空，需手动设置
	hash, _ := util.HashPassword("password")
	db.Model(&model.User{}).Where("id = ?", adminUser.ID).Update("password", hash)

	tokenPair, err := authService.Login(service.LoginRequest{Username: adminUser.Username, Password: "password", DeviceID: "test-device"})
	if err != nil {
		t.Fatalf("管理员登录失败: %v", err)
	}

	return r, jwtUtil, tokenPair.AccessToken
}

func TestAdmin_GetStats(t *testing.T) {
	r, _, token := setupAdminTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/admin/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	assert.NotNil(t, resp["data"])
}

func TestAdmin_ListUsers(t *testing.T) {
	r, _, token := setupAdminTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/admin/users?page=1&page_size=10", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
}

func TestAdmin_Recharge(t *testing.T) {
	r, _, token := setupAdminTest(t)

	body, _ := json.Marshal(map[string]interface{}{
		"user_id": "u_1001",
		"amount":  10000,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/admin/billing/recharge", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAdmin_RefundOrder(t *testing.T) {
	r, _, token := setupAdminTest(t)

	// 先创建订单再退款
	// 简化：测试路由可达性和权限校验
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/admin/orders/non-exist/refund", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	// 订单不存在返回 500，但说明路由和权限正确
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAdmin_RejectNonAdmin(t *testing.T) {
	r, jwtUtil, _ := setupAdminTest(t)

	// 生成普通用户 Token
	userToken, _ := jwtUtil.GenerateAccessToken("normal-user", "device", "user")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/admin/stats", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
