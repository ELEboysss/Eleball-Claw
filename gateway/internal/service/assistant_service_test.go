package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// assistantTestEnv 助手/本地购买/工具过滤测试的公共装配
type assistantTestEnv struct {
	db          *gorm.DB
	agentRepo   *repository.AgentRepo
	assistant   *AssistantService
	loader      *AgentToolLoader
	market      *AgentMarketService
}

func setupAssistantTest(t *testing.T) *assistantTestEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// :memory: 数据库按连接隔离，限制单连接避免「no such table」
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.AgentItem{}, &model.AgentPurchase{}, &model.AgentUserTool{},
		&model.Assistant{}, &model.AssistantItem{}, &model.ChatConversation{},
	))

	agentRepo := repository.NewAgentRepo(db)
	assistantSvc := NewAssistantService(db, repository.NewAssistantRepo(db), agentRepo)

	driverRegistry := NewToolDriverRegistry()
	driverRegistry.Register(NewModuleDriver(nil, nil))
	// moduleRegistry=nil：跳过模块在线探测，专注购买/激活/过滤链路
	loader := NewAgentToolLoader(agentRepo, driverRegistry, nil)

	// claw 本地购买语义：仅放行免费 SKU（userRepo/vipService 为 nil，免费路径不触碰）
	marketSvc := NewAgentMarketService(db, agentRepo, nil, nil, nil)
	marketSvc.SetLocalFreeOnly(true)
	marketSvc.SetAgentToolLoader(loader)

	return &assistantTestEnv{db: db, agentRepo: agentRepo, assistant: assistantSvc, loader: loader, market: marketSvc}
}

// createTestSKU 造一条 approved 秘技（module 驱动 manifest）
func (e *assistantTestEnv) createTestSKU(t *testing.T, id string, price int64) *model.AgentItem {
	t.Helper()
	manifest := map[string]interface{}{
		"id":          "com.eleball.tools.test." + id,
		"name":        id,
		"description": "测试秘技 " + id,
		"driver":      "module",
		"parameters": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"query": map[string]string{"type": "string"}},
		},
		"metadata": map[string]string{"module": "test-mod"},
	}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	item := &model.AgentItem{
		ID:           id,
		Name:         id,
		Category:     "搜索",
		PriceDanwan:  price,
		Status:       model.AgentStatusApproved,
		Level:        model.AgentLevelHuang,
		CreatorID:    "official",
		CreatorName:  "官方",
		CreatedAt:    time.Now(),
		ManifestJSON: string(raw),
	}
	require.NoError(t, e.db.Create(item).Error)
	return item
}

const assistantTestUser = "user-1"

// TestPurchaseAgent_LocalFreeOnly claw 本地购买：免费 SKU 成功并自动激活；付费 SKU 拒绝并引导云端
func TestPurchaseAgent_LocalFreeOnly(t *testing.T) {
	env := setupAssistantTest(t)
	freeSKU := env.createTestSKU(t, "sku-free", 0)

	// 免费 SKU 本地购买成功（不触碰 billing/余额）
	err := env.market.PurchaseAgent(assistantTestUser, PurchaseAgentRequest{AgentID: freeSKU.ID, Currency: "danwan"})
	require.NoError(t, err)

	// 购买记录存在且自动激活（ActivateToolOnPurchase）
	purchased, err := env.agentRepo.HasPurchased(freeSKU.ID, assistantTestUser)
	require.NoError(t, err)
	assert.True(t, purchased)
	active, err := env.agentRepo.IsToolActive(assistantTestUser, freeSKU.ID)
	require.NoError(t, err)
	assert.True(t, active)

	// 购买后可走既有激活切换
	newActive, err := env.market.ToggleAgentActive(assistantTestUser, freeSKU.ID)
	require.NoError(t, err)
	assert.False(t, newActive)

	// 重复购买报错
	err = env.market.PurchaseAgent(assistantTestUser, PurchaseAgentRequest{AgentID: freeSKU.ID, Currency: "danwan"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "已购买")

	// 付费 SKU 本地拒绝，引导云端购买
	paidSKU := env.createTestSKU(t, "sku-paid", 100)
	err = env.market.PurchaseAgent(assistantTestUser, PurchaseAgentRequest{AgentID: paidSKU.ID, Currency: "danwan"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "付费秘技请到云端购买")
	paidPurchased, err := env.agentRepo.HasPurchased(paidSKU.ID, assistantTestUser)
	require.NoError(t, err)
	assert.False(t, paidPurchased)
}

// TestAssistantService_CRUD 助手增删改查与用户隔离
func TestAssistantService_CRUD(t *testing.T) {
	env := setupAssistantTest(t)

	// name 必填
	_, err := env.assistant.Create(assistantTestUser, "", "")
	require.Error(t, err)

	view, err := env.assistant.Create(assistantTestUser, "搜索助手", "只做搜索")
	require.NoError(t, err)
	require.NotEmpty(t, view.ID)
	assert.Equal(t, "搜索助手", view.Name)
	assert.Empty(t, view.Items)

	// 更新名称/描述
	newName, newDesc := "搜索助手 Pro", "搜索 + 阅读"
	updated, err := env.assistant.Update(assistantTestUser, view.ID, AssistantUpdateInput{Name: &newName, Description: &newDesc})
	require.NoError(t, err)
	assert.Equal(t, newName, updated.Name)
	assert.Equal(t, newDesc, updated.Description)

	// 列表
	list, err := env.assistant.List(assistantTestUser)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, view.ID, list[0].ID)

	// 用户隔离：他人不可见/不可改
	_, err = env.assistant.Get("user-2", view.ID)
	require.Error(t, err)
	_, err = env.assistant.Update("user-2", view.ID, AssistantUpdateInput{Name: &newName})
	require.Error(t, err)

	// 删除
	require.NoError(t, env.assistant.Delete(assistantTestUser, view.ID))
	list, err = env.assistant.List(assistantTestUser)
	require.NoError(t, err)
	assert.Empty(t, list)
}

// TestAssistantService_SetItemsRequiresActive SetItems 逐项校验已购且已激活
func TestAssistantService_SetItemsRequiresActive(t *testing.T) {
	env := setupAssistantTest(t)
	skuA := env.createTestSKU(t, "sku-a", 0)
	skuB := env.createTestSKU(t, "sku-b", 0)

	// 仅购买激活 skuA
	require.NoError(t, env.market.PurchaseAgent(assistantTestUser, PurchaseAgentRequest{AgentID: skuA.ID, Currency: "danwan"}))

	view, err := env.assistant.Create(assistantTestUser, "组合", "")
	require.NoError(t, err)

	// 未购买的秘技拒绝
	_, err = env.assistant.SetItems(assistantTestUser, view.ID, []string{skuB.ID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未购买")

	// 已购已激活可通过，条目展开含秘技概要
	view, err = env.assistant.SetItems(assistantTestUser, view.ID, []string{skuA.ID})
	require.NoError(t, err)
	require.Len(t, view.Items, 1)
	assert.Equal(t, skuA.ID, view.Items[0].AgentID)
	assert.Equal(t, skuA.Name, view.Items[0].Name)

	// 已购但未激活拒绝
	require.NoError(t, env.agentRepo.SetToolActive(assistantTestUser, skuA.ID, "tool-a", false))
	_, err = env.assistant.SetItems(assistantTestUser, view.ID, []string{skuA.ID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未激活")
}

// TestAssistantService_DeleteClearsConversationRefs 删除助手时清空引用它的会话绑定
func TestAssistantService_DeleteClearsConversationRefs(t *testing.T) {
	env := setupAssistantTest(t)

	view, err := env.assistant.Create(assistantTestUser, "组合", "")
	require.NoError(t, err)

	conv := &model.ChatConversation{
		ID:          "conv-1",
		UserID:      assistantTestUser,
		Title:       "测试对话",
		Status:      "active",
		AssistantID: view.ID,
		CreatedAt:   time.Now().Unix(),
		UpdatedAt:   time.Now().Unix(),
	}
	require.NoError(t, env.db.Create(conv).Error)

	require.NoError(t, env.assistant.Delete(assistantTestUser, view.ID))

	var reloaded model.ChatConversation
	require.NoError(t, env.db.First(&reloaded, "id = ?", "conv-1").Error)
	assert.Empty(t, reloaded.AssistantID)
}

// TestLoadToolsForUserFiltered 助手过滤：子集 / 空集合 / 全部
func TestLoadToolsForUserFiltered(t *testing.T) {
	env := setupAssistantTest(t)
	skuA := env.createTestSKU(t, "sku-a", 0)
	skuB := env.createTestSKU(t, "sku-b", 0)
	skuC := env.createTestSKU(t, "sku-c", 0)
	for _, sku := range []*model.AgentItem{skuA, skuB, skuC} {
		require.NoError(t, env.market.PurchaseAgent(assistantTestUser, PurchaseAgentRequest{AgentID: sku.ID, Currency: "danwan"}))
	}

	ctx := context.Background()

	// 子集过滤
	tools, err := env.loader.LoadToolsForUserFiltered(ctx, assistantTestUser, []string{skuA.ID, skuB.ID})
	require.NoError(t, err)
	require.Len(t, tools, 2)
	agentIDs := map[string]bool{tools[0].AgentID: true, tools[1].AgentID: true}
	assert.True(t, agentIDs[skuA.ID])
	assert.True(t, agentIDs[skuB.ID])

	// 空集合 -> 空工具列表（不报错）
	tools, err = env.loader.LoadToolsForUserFiltered(ctx, assistantTestUser, nil)
	require.NoError(t, err)
	assert.Empty(t, tools)

	// 全部
	tools, err = env.loader.LoadToolsForUserFiltered(ctx, assistantTestUser, []string{skuA.ID, skuB.ID, skuC.ID})
	require.NoError(t, err)
	assert.Len(t, tools, 3)

	// 对照：未指定助手走 LoadToolsForUser，加载全部已激活
	tools, err = env.loader.LoadToolsForUser(ctx, assistantTestUser)
	require.NoError(t, err)
	assert.Len(t, tools, 3)
}
