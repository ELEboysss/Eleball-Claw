package service

import (
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// setupVIPServiceTest 创建基于内存数据库的 VIP 相关服务
func setupVIPServiceTest(t *testing.T) (*gorm.DB, *VIPService, *CDKService) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.User{}, &model.BalanceTransaction{}, &model.Order{}, &model.CDK{}, &model.VIPPlan{}, &model.VIPSubscription{})

	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	cdkRepo := repository.NewCDKRepo(db)

	vipService := newTestVIPService(db)
	cdkService := NewCDKService(cdkRepo, userRepo, billRepo, vipService)
	return db, vipService, cdkService
}

// createVIPPlan 快速创建一个测试 VIP 套餐
func createVIPPlan(t *testing.T, vipService *VIPService, level int, name string, priceFen int64, discount int) *model.VIPPlan {
	plan, err := vipService.CreatePlan(&CreateVIPPlanRequest{
		Level:            level,
		Name:             name,
		PriceFen:         priceFen,
		DurationDays:     30,
		DiscountPercent:  discount,
		MaxConversations: 200 * level,
		MaxAgentSessions: 100,
		AsrQuotaMonthly:  3000,
		AgentEnabled:     true,
		FileToolsEnabled: true,
		SortOrder:        level,
		IsEnabled:        true,
		Description:      "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestVIPService_ApplyDiscount(t *testing.T) {
	db, vipService, _ := setupVIPServiceTest(t)

	user := &model.User{ID: "u1", Username: "u1", Password: "p", Role: "user", ElegantBalance: 100000}
	db.Create(user)

	// 非 VIP：折扣为 100%，费用不变
	cost, err := vipService.ApplyDiscount("u1", 10000)
	assert.NoError(t, err)
	assert.Equal(t, int64(10000), cost)

	// 开通 VIP2（8 折）
	createVIPPlan(t, vipService, 2, "超级弹丸", 9900, 80)
	_, err = vipService.Subscribe("u1", planIDByLevel(t, vipService, 2), "wechat", true)
	assert.NoError(t, err)

	cost, err = vipService.ApplyDiscount("u1", 10000)
	assert.NoError(t, err)
	assert.Equal(t, int64(8000), cost)

	// 管理员不再免费，按 VIP 状态正常计费
	admin := &model.User{ID: "admin1", Username: "admin1", Password: "p", Role: model.UserRoleAdmin, ElegantBalance: 100000}
	db.Create(admin)

	// 未开通 VIP 的管理员按原价
	cost, err = vipService.ApplyDiscount("admin1", 10000)
	assert.NoError(t, err)
	assert.Equal(t, int64(10000), cost)

	// 开通 VIP 后管理员享受折扣
	_, err = vipService.Subscribe("admin1", planIDByLevel(t, vipService, 2), "wechat", true)
	assert.NoError(t, err)
	cost, err = vipService.ApplyDiscount("admin1", 10000)
	assert.NoError(t, err)
	assert.Equal(t, int64(8000), cost)
}

func TestVIPService_CDKActivate(t *testing.T) {
	db, vipService, cdkService := setupVIPServiceTest(t)

	user := &model.User{ID: "u2", Username: "u2", Password: "p", Role: "user"}
	db.Create(user)

	// 创建 VIP1 套餐
	createVIPPlan(t, vipService, 1, "强力弹丸", 4900, 100)

	// 生成 VIP 兑换码
	resp, err := cdkService.BatchGenerate(BatchGenerateRequest{
		Type:            model.CDKTypeVIP,
		VIPLevel:        1,
		VIPDurationDays: 30,
		Count:           1,
		Note:            "test",
	})
	assert.NoError(t, err)
	assert.Len(t, resp.Items, 1)
	code := resp.Items[0].Code

	// 兑换
	redeemResp, err := cdkService.Redeem("u2", code)
	assert.NoError(t, err)
	assert.True(t, redeemResp.VIPActivated)
	assert.Equal(t, 1, redeemResp.VIPLevel)

	// 验证用户 VIP 状态
	status, err := vipService.GetVIPStatus("u2")
	assert.NoError(t, err)
	assert.Equal(t, 1, status.Level)
	assert.True(t, status.IsVIP)
	assert.True(t, status.ExpireAt.After(time.Now().AddDate(0, 0, 25)))
}

func TestVIPService_SubscribeAndChangePlan(t *testing.T) {
	db, vipService, _ := setupVIPServiceTest(t)

	user := &model.User{ID: "u3", Username: "u3", Password: "p", Role: "user", ElegantBalance: 100000}
	db.Create(user)

	plan1 := createVIPPlan(t, vipService, 1, "强力弹丸", 4900, 100)
	plan2 := createVIPPlan(t, vipService, 2, "超级弹丸", 9900, 80)

	// 开通 VIP1
	res1, err := vipService.Subscribe("u3", plan1.ID, "wechat", true)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), res1.Amount)
	assert.Equal(t, int64(4900), res1.ElegantDeducted)

	status1, _ := vipService.GetVIPStatus("u3")
	assert.Equal(t, 1, status1.Level)

	// 更换到 VIP2：应扣除差额或生成新订单
	res2, err := vipService.Subscribe("u3", plan2.ID, "wechat", true)
	assert.NoError(t, err)
	// 新套餐 9900，旧套餐剩余价值按周退费，优雅弹丸应继续抵扣
	assert.Equal(t, "vip", res2.ProductType)
	assert.True(t, res2.ElegantDeducted >= 0)
}

// planIDByLevel 根据等级查找上架套餐 ID，用于测试断言
func planIDByLevel(t *testing.T, vipService *VIPService, level int) string {
	plan, err := vipService.vipRepo.GetPlanByLevel(level)
	if err != nil {
		t.Fatal(err)
	}
	return plan.ID
}
