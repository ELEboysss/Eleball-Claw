package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// setupVIPTest 构造一个包含 VIP 路由的测试路由，并返回用户/管理员 Token
func setupVIPTest(t *testing.T) (*gin.Engine, *util.JWTUtil, string, string) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(
		&model.User{},
		&model.BalanceTransaction{},
		&model.Order{},
		&model.CDK{},
		&model.VIPPlan{},
		&model.VIPSubscription{},
	)

	jwtUtil := util.NewJWTUtil("test-secret", 24, 168)
	logger := zap.NewNop()
	cfg := &config.AppConfig{Server: config.ServerConfig{Mode: "test"}, Admin: config.AdminConfig{Enabled: true, AllowedIPs: []string{"127.0.0.1"}}}

	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	rechargePackageRepo := repository.NewRechargePackageRepo(db)
	cdkRepo := repository.NewCDKRepo(db)
	vipService := newTestVIPService(db)

	// 普通测试用户
	testUser := &model.User{
		ID:             "user-vip-001",
		Username:       "vipuser@example.com",
		Nickname:       "VIP User",
		Role:           model.UserRoleUser,
		Balance:        100000,
		ElegantBalance: 50000, // 500 优雅弹丸
		Status:         1,
	}
	db.Create(testUser)

	// 管理员测试用户
	adminUser := &model.User{
		ID:       "user-admin-001",
		Username: "admin@example.com",
		Nickname: "Admin",
		Role:     model.UserRoleAdmin,
		Status:   1,
	}
	db.Create(adminUser)

	authService := service.NewAuthService(userRepo, jwtUtil, nil, "http://localhost:8080/v1", newTestEleAgentModelService(), nil)
	billingService := service.NewBillingService(userRepo, billRepo, newTestEleAgentModelService(), nil, vipService)
	chatService := service.NewChatProxyService(nil, nil, newTestEleAgentModelService(), zap.NewNop())
	paymentService := service.NewPaymentService(db, userRepo, rechargePackageRepo, orderRepo, billRepo, vipService, nil)
	cdkService := service.NewCDKService(cdkRepo, userRepo, billRepo, vipService)

	authHandler := handler.NewAuthHandler(authService, vipService)
	billingHandler := handler.NewBillingHandler(billingService)
	chatHandler := handler.NewChatHandler(chatService, billingService, zap.NewNop())
	syncHandler := handler.NewSyncHandler(repository.NewConversationRepo(db))
	paymentHandler := handler.NewPaymentHandler(paymentService)
	adminHandler := handler.NewAdminHandler(service.NewAdminService(db, userRepo, billRepo, orderRepo, nil, vipService))
	agentHandler := handler.NewAgentHandler(service.NewAgentMarketService(db, repository.NewAgentRepo(db), userRepo, vipService, nil))
	withdrawalHandler := handler.NewWithdrawalHandler(service.NewWithdrawalService(db, repository.NewWithdrawalRepo(db), repository.NewAgentRepo(db), service.NewMockPaymentProvider()))
	eleAgentModelService := newTestEleAgentModelService()
	eleAgentHandler := handler.NewEleAgentHandler(service.NewEleAgentService(chatService, eleAgentModelService, nil, "http://localhost:8080/v1"), eleAgentModelService)
	adminKeyHandler := handler.NewAdminKeyHandler(nil)
	adminEleAgentModelHandler := handler.NewAdminEleAgentModelHandler(newTestEleAgentModelService())
	publicSettingHandler := handler.NewPublicSettingHandler(service.NewSettingService(repository.NewSettingRepo(db)))
	adminSettingHandler := handler.NewAdminSettingHandler(service.NewSettingService(repository.NewSettingRepo(db)))
	rechargePackageHandler := handler.NewRechargePackageHandler(service.NewRechargePackageService(rechargePackageRepo))
	cdkHandler := handler.NewCDKHandler(cdkService)
	releaseHandler := handler.NewReleaseHandler(service.NewReleaseService("releases"), logger)
	sttService := service.NewSttService("", "", "", "", "", 0, 0, logger)
	sttHandler := handler.NewSttHandler(sttService, userRepo, vipService, logger)
	vipHandler := handler.NewVIPHandler(vipService)

	r := router.NewRouter(
		cfg, logger, jwtUtil,
		authHandler, chatHandler, billingHandler, syncHandler, paymentHandler,
		adminHandler, adminKeyHandler, adminEleAgentModelHandler, adminSettingHandler, publicSettingHandler,
		agentHandler, withdrawalHandler, eleAgentHandler, rechargePackageHandler, cdkHandler, releaseHandler, sttHandler,
		nil, nil, nil, vipHandler,
		nil, nil,
	)

	userToken, _ := jwtUtil.GenerateAccessToken(testUser.ID, "test-device", testUser.Role)
	adminToken, _ := jwtUtil.GenerateAccessToken(adminUser.ID, "admin-device", adminUser.Role)

	return r, jwtUtil, userToken, adminToken
}

// createTestVIPPlan 通过管理接口创建一个测试 VIP 套餐
func createTestVIPPlan(t *testing.T, r *gin.Engine, adminToken string, level int, name string, priceFen int64, discount int) string {
	body, _ := json.Marshal(map[string]interface{}{
		"level":              level,
		"name":               name,
		"price_fen":          priceFen,
		"duration_days":      30,
		"discount_percent":   discount,
		"max_conversations":  200,
		"max_agent_sessions": 100,
		"asr_quota_monthly":  3000,
		"agent_enabled":      true,
		"file_tools_enabled": true,
		"sort_order":         level,
		"is_enabled":         true,
		"description":        "test plan",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/admin/vip/plans", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	return data["id"].(string)
}

func TestVIP_ListPlans(t *testing.T) {
	r, _, userToken, adminToken := setupVIPTest(t)
	createTestVIPPlan(t, r, adminToken, 1, "强力弹丸", 4900, 100)
	createTestVIPPlan(t, r, adminToken, 2, "超级弹丸", 9900, 80)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/vip/plans", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	items := resp["data"].(map[string]interface{})["items"].([]interface{})
	assert.Len(t, items, 2)
}

func TestVIP_GetStatus_DefaultVIP0(t *testing.T) {
	r, _, userToken, _ := setupVIPTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/vip/status", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(0), data["level"])
	assert.Equal(t, false, data["is_vip"])
}

func TestVIP_SubscribeWithElegantBalance(t *testing.T) {
	r, _, userToken, adminToken := setupVIPTest(t)
	planID := createTestVIPPlan(t, r, adminToken, 1, "强力弹丸", 4900, 100)

	body, _ := json.Marshal(map[string]interface{}{
		"plan_id":             planID,
		"channel":             "wechat",
		"use_elegant_balance": true,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/vip/subscribe", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, "vip", data["product_type"])
	assert.Equal(t, float64(0), data["amount"])
	assert.Equal(t, true, data["amount"] == float64(0) && data["elegant_deducted"] == float64(4900))

	// 确认用户已升级
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/vip/status", nil)
	req2.Header.Set("Authorization", "Bearer "+userToken)
	r.ServeHTTP(w2, req2)
	var statusResp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &statusResp)
	statusData := statusResp["data"].(map[string]interface{})
	assert.Equal(t, float64(1), statusData["level"])
	assert.Equal(t, true, statusData["is_vip"])
	assert.Equal(t, float64(100), statusData["discount_percent"])
}

func TestVIP_SubscribeCashThenAdminConfirm(t *testing.T) {
	r, _, userToken, adminToken := setupVIPTest(t)
	planID := createTestVIPPlan(t, r, adminToken, 2, "超级弹丸", 9900, 80)

	// 1. 用户订阅，不抵扣优雅弹丸，生成待支付订单
	body, _ := json.Marshal(map[string]interface{}{
		"plan_id":             planID,
		"channel":             "alipay",
		"use_elegant_balance": false,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/vip/subscribe", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var subResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &subResp)
	data := subResp["data"].(map[string]interface{})
	orderID := data["order_id"].(string)
	assert.NotEmpty(t, orderID)
	assert.Equal(t, float64(9900), data["amount"])

	// 2. 管理员确认收款
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/admin/orders/"+orderID+"/confirm", nil)
	req2.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)

	// 3. 确认用户升级为 VIP2
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/v1/vip/status", nil)
	req3.Header.Set("Authorization", "Bearer "+userToken)
	r.ServeHTTP(w3, req3)
	var statusResp map[string]interface{}
	json.Unmarshal(w3.Body.Bytes(), &statusResp)
	statusData := statusResp["data"].(map[string]interface{})
	assert.Equal(t, float64(2), statusData["level"])
	assert.Equal(t, true, statusData["is_vip"])
	assert.Equal(t, float64(80), statusData["discount_percent"])
}

func TestVIP_AdminGrantVIP(t *testing.T) {
	r, _, userToken, adminToken := setupVIPTest(t)
	planID := createTestVIPPlan(t, r, adminToken, 1, "强力弹丸", 4900, 100)

	body, _ := json.Marshal(map[string]interface{}{
		"plan_id": planID,
		"months":  3,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/admin/users/user-vip-001/vip", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/vip/status", nil)
	req2.Header.Set("Authorization", "Bearer "+userToken)
	r.ServeHTTP(w2, req2)
	var statusResp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &statusResp)
	statusData := statusResp["data"].(map[string]interface{})
	assert.Equal(t, float64(1), statusData["level"])
	assert.Equal(t, true, statusData["is_vip"])

	// 3 个月 = 90 天，到期时间应在 80 天以后
	expireAt, _ := time.Parse(time.RFC3339, statusData["expire_at"].(string))
	assert.True(t, expireAt.After(time.Now().AddDate(0, 0, 80)))
}

func TestVIP_AdminListSubscriptions(t *testing.T) {
	r, _, _, adminToken := setupVIPTest(t)
	planID := createTestVIPPlan(t, r, adminToken, 1, "强力弹丸", 4900, 100)

	// 管理员开通
	body, _ := json.Marshal(map[string]interface{}{"plan_id": planID, "months": 1})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/admin/users/user-vip-001/vip", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 查询订阅记录
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/admin/vip/subscriptions", nil)
	req2.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.Equal(t, float64(0), resp["code"])
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(1), data["total"])
	items := data["items"].([]interface{})
	assert.Len(t, items, 1)
	item := items[0].(map[string]interface{})
	assert.Equal(t, "user-vip-001", item["user_id"])
	assert.Equal(t, float64(1), item["level"])
}
