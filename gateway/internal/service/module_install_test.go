package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupCloudInstallTest 构造云端秘技安装测试环境：内存 SQLite + 预置 official 模块 + 模拟 /health 服务
func setupCloudInstallTest(t *testing.T) (*ModuleService, *AgentMarketService, *repository.AgentRepo, *repository.DriverRepo) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	// :memory: 数据库按连接隔离，限制单连接避免「no such table」
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}, &model.AgentItem{}, &model.AgentPurchase{}, &model.AgentUserTool{}, &model.ModuleRecord{}, &model.DriverRecord{}))

	moduleRepo := repository.NewModuleRepo(db)
	driverRepo := repository.NewDriverRepo(db)
	agentRepo := repository.NewAgentRepo(db)

	// 模拟模块 /health 服务（official 模块不拉镜像，但安装后会做健康探测）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"module_id":    "search-web",
				"version":      "1.0.0",
				"status":       "ok",
				"capabilities": []string{"search"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	skillRuntimeRepo := repository.NewSkillRuntimeRepo(db)
	registry := NewSkillRuntimeRegistry(&config.AgentReachConfig{HealthCheckInterval: time.Hour})
	registry.SetRepo(skillRuntimeRepo)
	manager := NewSkillRuntimeManager(registry, nil)

	// 预置 official 模块（marketplace 扫描的等价物）
	rt := &model.SkillRuntime{
		ID:           "search-web",
		Name:         "Search Web",
		Endpoint:     server.URL,
		Transport:    model.SkillRuntimeTransportExecute,
		Deployment:   model.SkillRuntimeDeploymentDocker,
		Official:     true,
		DriverID:     "search-web",
		Status:       model.SkillRuntimeStatusOffline,
		Capabilities: "[]",
	}
	require.NoError(t, registry.Register(rt))

	moduleSvc := NewModuleService(registry, manager, skillRuntimeRepo, agentRepo)
	moduleSvc.SetModuleRepo(moduleRepo)
	moduleSvc.SetDriverRepo(driverRepo)

	agentSvc := NewAgentMarketService(db, agentRepo, nil, nil, registry)
	agentSvc.SetLocalFreeOnly(true)
	agentSvc.SetModuleRepo(moduleRepo)

	return moduleSvc, agentSvc, agentRepo, driverRepo
}

// cloudInstallMeta 构造一份 official 云端秘技安装 meta
func cloudInstallMeta() ModuleInstallMeta {
	manifest := map[string]interface{}{
		"id":          "com.eleball.tools.search_web",
		"name":        "联网搜索",
		"description": "搜索互联网内容",
		"driver":      "module",
		"category":    "search",
		"level":       2,
		"parameters":  map[string]interface{}{"type": "object"},
		"metadata":    map[string]string{"module": "search-web"},
	}
	raw, _ := json.Marshal(manifest)
	return ModuleInstallMeta{
		ModuleID:      "search-web",
		AgentID:       "agent-search-web",
		Name:          "联网搜索",
		Description:   "搜索互联网内容",
		Version:       "1.0.0",
		TransportType: "module",
		DriverID:      "search-web",
		Official:      true,
		Manifest:      raw,
		AuthToken:     "tok-search-web",
	}
}

// TestInstallFromCloudMeta_OfficialUpsertsDriver 官方模块安装成功后 upsert 本地驱动别名并绑定模块
func TestInstallFromCloudMeta_OfficialUpsertsDriver(t *testing.T) {
	moduleSvc, _, _, driverRepo := setupCloudInstallTest(t)
	meta := cloudInstallMeta()

	record, err := moduleSvc.InstallFromCloudMeta(meta)
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.True(t, record.Official)
	assert.Equal(t, "cloud-purchased", record.InstallSource)

	driver, err := driverRepo.GetByID("search-web")
	require.NoError(t, err)
	assert.Equal(t, "search-web", driver.ModuleID)
	assert.Equal(t, "module", driver.TransportType)
	assert.Equal(t, "tok-search-web", driver.AuthToken)
	assert.Equal(t, "联网搜索", driver.Name)

	// 幂等：重复安装不报错，驱动绑定仍在
	_, err = moduleSvc.InstallFromCloudMeta(meta)
	require.NoError(t, err)
	driver, err = driverRepo.GetByID("search-web")
	require.NoError(t, err)
	assert.Equal(t, "search-web", driver.ModuleID)
}

// TestInstallFromCloudMeta_PersistsSourceOrigin 云端下发的来源属性持久化到本地运行时
// （type3：official 模块扫描默认 eleball_builtin，云端下发 eleball_cloud 时校正）。
func TestInstallFromCloudMeta_PersistsSourceOrigin(t *testing.T) {
	moduleSvc, _, _, _ := setupCloudInstallTest(t)

	// 预置模块经 Register 兜底默认为 eleball_builtin（claw 侧）
	rt0, err := moduleSvc.repo.GetByID("search-web")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeOriginEleballBuiltin, rt0.SourceOrigin)

	// 云端下发 eleball_cloud -> 幂等路径校正并持久化
	meta := cloudInstallMeta()
	meta.SourceOrigin = "eleball_cloud"
	_, err = moduleSvc.InstallFromCloudMeta(meta)
	require.NoError(t, err)
	rt, err := moduleSvc.repo.GetByID("search-web")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeOriginEleballCloud, rt.SourceOrigin)

	// 用户共享模块（user + actor）-> 幂等重装校正
	meta2 := cloudInstallMeta()
	meta2.SourceOrigin = "user"
	meta2.SourceActor = "alice"
	_, err = moduleSvc.InstallFromCloudMeta(meta2)
	require.NoError(t, err)
	rt2, err := moduleSvc.repo.GetByID("search-web")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeOriginUser, rt2.SourceOrigin)
	assert.Equal(t, "alice", rt2.SourceActor)
}

// TestIsCloudPurchasedAgent_LocalPresetExempt 本地扫描预置模块（InstallSource 为空）的秘技免 VIP 门控
func TestIsCloudPurchasedAgent_LocalPresetExempt(t *testing.T) {
	_, agentSvc, agentRepo, _ := setupCloudInstallTest(t)

	// 本地内置秘技：manifest 指向本地预置模块，但未经云端 installed 接口安装
	meta := cloudInstallMeta()
	var manifest map[string]interface{}
	require.NoError(t, json.Unmarshal(meta.Manifest, &manifest))
	raw, _ := json.Marshal(manifest)
	require.NoError(t, agentRepo.Create(&model.AgentItem{
		ID:           "agent-local-search-web",
		Name:         "联网搜索（本地内置）",
		Status:       model.AgentStatusApproved,
		ManifestJSON: string(raw),
	}))

	assert.False(t, agentSvc.IsCloudPurchasedAgent("agent-local-search-web"))
}

// TestEnsureCloudAgentProvision 安装后落库 AgentItem + 幂等 AgentPurchase，且激活链路闭环
func TestEnsureCloudAgentProvision(t *testing.T) {
	moduleSvc, agentSvc, agentRepo, _ := setupCloudInstallTest(t)
	meta := cloudInstallMeta()

	_, err := moduleSvc.InstallFromCloudMeta(meta)
	require.NoError(t, err)

	// 未安装 provision 前，未购买用户无法激活
	_, err = agentSvc.ToggleAgentActive("u1", "agent-search-web")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未购买")

	require.NoError(t, moduleSvc.EnsureCloudAgentProvision(meta, "u1"))

	// AgentItem 落库：approved + manifest 完整
	item, err := agentRepo.GetByID("agent-search-web")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, item.Status)
	assert.Equal(t, "联网搜索", item.Name)
	assert.Equal(t, "search", item.Category)
	assert.Equal(t, model.AgentLevelXuan, item.Level)
	assert.NotEmpty(t, item.ManifestJSON)

	// AgentPurchase 幂等：重复 provision 不产生第二条
	purchased, err := agentRepo.HasPurchased("agent-search-web", "u1")
	require.NoError(t, err)
	assert.True(t, purchased)
	require.NoError(t, moduleSvc.EnsureCloudAgentProvision(meta, "u1"))
	count, err := agentRepo.CountPurchases("agent-search-web")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// 首次激活：ToggleAgentActive 创建 active=true 的 AgentUserTool
	active, err := agentSvc.ToggleAgentActive("u1", "agent-search-web")
	require.NoError(t, err)
	assert.True(t, active)
	hasTool, err := agentRepo.HasUserTool("u1", "agent-search-web")
	require.NoError(t, err)
	assert.True(t, hasTool)

	// ListPurchasedExecutableTools 能查到该秘技（purchases + active + approved + manifest 非空）
	tools, err := agentRepo.ListPurchasedExecutableTools("u1")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "agent-search-web", tools[0].ID)

	// 云端安装的官方模块 InstallSource=cloud-purchased，provenance 判定为云端来源（激活需 VIP1+）
	assert.True(t, agentSvc.IsCloudPurchasedAgent("agent-search-web"))

	// 再次 toggle 为关闭
	active, err = agentSvc.ToggleAgentActive("u1", "agent-search-web")
	require.NoError(t, err)
	assert.False(t, active)
	tools, err = agentRepo.ListPurchasedExecutableTools("u1")
	require.NoError(t, err)
	assert.Empty(t, tools)
}

// TestEnsureCloudAgentProvision_PreserveStats AgentItem 已存在时不清零统计字段
func TestEnsureCloudAgentProvision_PreserveStats(t *testing.T) {
	moduleSvc, _, agentRepo, _ := setupCloudInstallTest(t)
	meta := cloudInstallMeta()

	existing := &model.AgentItem{
		ID:            "agent-search-web",
		Name:          "旧名称",
		CreatorID:     "someone",
		Status:        model.AgentStatusApproved,
		PurchaseCount: 42,
		AvgRating:     4.5,
		FavoriteCount: 7,
		UseCount:      9,
	}
	require.NoError(t, agentRepo.Create(existing))

	require.NoError(t, moduleSvc.EnsureCloudAgentProvision(meta, "u1"))

	item, err := agentRepo.GetByID("agent-search-web")
	require.NoError(t, err)
	assert.Equal(t, "联网搜索", item.Name)
	assert.NotEmpty(t, item.ManifestJSON)
	assert.Equal(t, int64(42), item.PurchaseCount)
	assert.Equal(t, 4.5, item.AvgRating)
	assert.Equal(t, int64(7), item.FavoriteCount)
	assert.Equal(t, int64(9), item.UseCount)
}

// TestEnsureCloudAgentProvision_AgentIDFallback agent_id 为空时回退 manifest.id
func TestEnsureCloudAgentProvision_AgentIDFallback(t *testing.T) {
	moduleSvc, _, agentRepo, _ := setupCloudInstallTest(t)
	meta := cloudInstallMeta()
	meta.AgentID = ""

	require.NoError(t, moduleSvc.EnsureCloudAgentProvision(meta, "u1"))

	item, err := agentRepo.GetByID("com.eleball.tools.search_web")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, item.Status)
}

// TestEnsureCloudAgentProvision_NoManifest 无 manifest 时跳过，不报错
func TestEnsureCloudAgentProvision_NoManifest(t *testing.T) {
	moduleSvc, _, agentRepo, _ := setupCloudInstallTest(t)
	meta := cloudInstallMeta()
	meta.Manifest = nil

	require.NoError(t, moduleSvc.EnsureCloudAgentProvision(meta, "u1"))

	_, err := agentRepo.GetByID("agent-search-web")
	assert.Error(t, err)
}

// TestInstallFromCloudMeta_CloudManifestDisablesAutoSKU 云端下发 manifest 后：
// auto_sku 关闭（DB + 内存副本）、遗留派生 SKU 下架、手写 SKU 保留。
// 对应 S2：消除「auto-derive 用 MCP 工具名与云端 manifest 定名共存」的命名错位。
func TestInstallFromCloudMeta_CloudManifestDisablesAutoSKU(t *testing.T) {
	moduleSvc, _, agentRepo, _ := setupCloudInstallTest(t)

	// 模拟 auto_sku 模块：开启自动派生并持久化（DB + 内存副本同步）。
	rt, err := moduleSvc.repo.GetByID("search-web")
	require.NoError(t, err)
	rt.AutoSKU = true
	require.NoError(t, moduleSvc.repo.CreateOrUpdate(rt))
	if inMem := moduleSvc.registry.Get("search-web"); inMem != nil {
		inMem.AutoSKU = true
	}

	// 遗留派生 SKU：manifest 带 auto_sku_module 标记，名取 MCP 工具名（与云端 manifest 定名不一致）。
	derivedMF := map[string]interface{}{
		"id":          "search-web-echo",
		"name":        "echo",
		"description": "echo tool",
		"driver":      "search-web",
		"parameters":  map[string]interface{}{"type": "object"},
		"metadata":    map[string]string{"module": "search-web", "auto_sku_module": "search-web"},
	}
	derivedRaw, _ := json.Marshal(derivedMF)
	require.NoError(t, agentRepo.Create(&model.AgentItem{
		ID:           "search-web-echo",
		Name:         "echo",
		Status:       model.AgentStatusApproved,
		ManifestJSON: string(derivedRaw),
	}))

	// 手写 SKU：无 auto_sku_module 标记，不应被下架。
	handMF := map[string]interface{}{
		"id":          "search-web-custom",
		"name":        "自定义工具",
		"description": "手写 SKU",
		"driver":      "search-web",
		"parameters":  map[string]interface{}{"type": "object"},
		"metadata":    map[string]string{"module": "search-web"},
	}
	handRaw, _ := json.Marshal(handMF)
	require.NoError(t, agentRepo.Create(&model.AgentItem{
		ID:           "search-web-custom",
		Name:         "自定义工具",
		Status:       model.AgentStatusApproved,
		ManifestJSON: string(handRaw),
	}))

	// 云端 manifest 安装（idempotent 路径，search-web 已预置）。
	record, err := moduleSvc.InstallFromCloudMeta(cloudInstallMeta())
	require.NoError(t, err)
	require.NotNil(t, record)

	// auto_sku 已关闭：DB + 内存副本。
	rtAfter, err := moduleSvc.repo.GetByID("search-web")
	require.NoError(t, err)
	assert.False(t, rtAfter.AutoSKU, "云端 manifest 接管后 auto_sku 应关闭")
	inMem := moduleSvc.registry.Get("search-web")
	if assert.NotNil(t, inMem) {
		assert.False(t, inMem.AutoSKU, "内存副本 auto_sku 应同步关闭（supervisor 立即停止派生）")
	}

	// 遗留派生 SKU 已下架。
	derived, err := agentRepo.GetByID("search-web-echo")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusDelisted, derived.Status, "遗留派生 SKU 应下架")

	// 手写 SKU 不受影响。
	hand, err := agentRepo.GetByID("search-web-custom")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, hand.Status, "手写 SKU 不应被下架")
}
