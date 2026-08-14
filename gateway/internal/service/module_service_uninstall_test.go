package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newUninstallTestSvc 构造卸载测试环境：内存 SQLite（SkillRuntime + AgentItem 表）+ marketplace
// 临时根（CLAW_MARKETPLACE_DIR）。返回 ModuleService（已注入 agentRepo）、runtime repo、agent repo。
func newUninstallTestSvc(t *testing.T) (*ModuleService, *repository.SkillRuntimeRepo, *repository.AgentRepo) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}, &model.AgentItem{}))

	rtRepo := repository.NewSkillRuntimeRepo(db)
	reg := NewSkillRuntimeRegistry(nil)
	reg.SetRepo(rtRepo)
	agentRepo := repository.NewAgentRepo(db)
	modSvc := NewModuleService(reg, nil, rtRepo, agentRepo)

	t.Setenv("CLAW_MARKETPLACE_DIR", t.TempDir())
	return modSvc, rtRepo, agentRepo
}

// writeUserPackageDir 写一个 {slug} 包目录 + module.json（模拟本地 DIY 模块磁盘文件）。
func writeUserPackageDir(t *testing.T, slug string) string {
	t.Helper()
	modDir := filepath.Join(os.Getenv("CLAW_MARKETPLACE_DIR"), slug)
	require.NoError(t, os.MkdirAll(modDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "module.json"),
		[]byte(`{"id":"`+slug+`","name":"`+slug+`","description":"d"}`), 0o644))
	return modDir
}

// registerRuntime 注册一个带包身份的运行时（SourceOrigin/Official 由调用方指定）。
func registerRuntime(t *testing.T, svc *ModuleService, rtID, slug string, origin model.SkillRuntimeSourceOrigin, official bool) {
	t.Helper()
	rt := &model.SkillRuntime{
		ID:                 rtID,
		Name:               "Test Runtime",
		Description:        "desc",
		SourceOrigin:       origin,
		PackageName:        slug,
		PackageTitle:       slug,
		PackageDescription: "pkg desc",
		Transport:          model.SkillRuntimeTransportMCPStdio,
		Deployment:         model.SkillRuntimeDeploymentProcess,
		Status:             model.SkillRuntimeStatusOnline,
		Official:           official,
	}
	require.NoError(t, svc.registry.Register(rt))
}

// createDerivedSKU 造一个派生 SKU（ID 前缀 slug-，manifest 带 auto_sku_module + package_module，status=approved）。
func createDerivedSKU(t *testing.T, agentRepo *repository.AgentRepo, id, slug string) *model.AgentItem {
	t.Helper()
	sku := &model.AgentItem{
		ID:       id,
		Name:     "tool1",
		Category: "互联网",
		Status:   model.AgentStatusApproved,
	}
	mf := model.ToolManifest{
		ID:     id,
		Name:   "tool1",
		Driver: model.ToolDriverType("driver-x"),
		Metadata: map[string]string{
			"auto_sku_module": slug,
			"module":          slug,
			"package_module":  slug,
		},
	}
	b, err := json.Marshal(mf)
	require.NoError(t, err)
	sku.ManifestJSON = string(b)
	require.NoError(t, agentRepo.Create(sku))
	return sku
}

// TestUninstallModule_UserModule 非官方（user）模块真·卸载：目录删、运行时注销、SKU 下架。
func TestUninstallModule_UserModule(t *testing.T) {
	modSvc, rtRepo, agentRepo := newUninstallTestSvc(t)
	slug := "my-mcp"
	rtID := slug
	modDir := writeUserPackageDir(t, slug)
	registerRuntime(t, modSvc, rtID, slug, model.SkillRuntimeOriginUser, false)
	createDerivedSKU(t, agentRepo, slug+"-tool1", slug)

	require.NoError(t, modSvc.UninstallModule(rtID))

	// 目录被删
	_, err := os.Stat(modDir)
	assert.True(t, os.IsNotExist(err), "卸载后 marketplace/<slug>/ 目录应删除")

	// 运行时注销
	_, err = rtRepo.GetByID(rtID)
	assert.Error(t, err, "卸载后运行时记录应注销")

	// SKU 下架（delisted，保留记录不硬删）
	item, err := agentRepo.GetByID(slug + "-tool1")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusDelisted, item.Status)
}

// TestUninstallModule_OfficialRefused 官方模块拒绝卸载。
func TestUninstallModule_OfficialRefused(t *testing.T) {
	modSvc, rtRepo, _ := newUninstallTestSvc(t)
	slug := "agent-reach"
	rtID := slug
	modDir := writeUserPackageDir(t, slug)
	registerRuntime(t, modSvc, rtID, slug, model.SkillRuntimeOriginEleballBuiltin, true)

	err := modSvc.UninstallModule(rtID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "官方模块")

	// 目录保留
	_, statErr := os.Stat(modDir)
	assert.NoError(t, statErr, "官方模块目录应保留")
	// 运行时保留
	_, gErr := rtRepo.GetByID(rtID)
	assert.NoError(t, gErr, "官方模块运行时应保留")
}

// TestUninstallModule_NotFound 不存在的运行时 ID 报错。
func TestUninstallModule_NotFound(t *testing.T) {
	modSvc, _, _ := newUninstallTestSvc(t)
	err := modSvc.UninstallModule("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

// TestUninstallModule_MultiRuntimePackage 多运行时包（{slug}-mcp-main / {slug}-mcp-aux）一并注销。
func TestUninstallModule_MultiRuntimePackage(t *testing.T) {
	modSvc, rtRepo, agentRepo := newUninstallTestSvc(t)
	slug := "my-pkg"
	writeUserPackageDir(t, slug)
	registerRuntime(t, modSvc, slug+"-mcp-main", slug, model.SkillRuntimeOriginUser, false)
	registerRuntime(t, modSvc, slug+"-mcp-aux", slug, model.SkillRuntimeOriginUser, false)
	createDerivedSKU(t, agentRepo, slug+"-mcp-main-tool1", slug)
	createDerivedSKU(t, agentRepo, slug+"-mcp-aux-tool1", slug)

	require.NoError(t, modSvc.UninstallModule(slug+"-mcp-main"))

	_, err := rtRepo.GetByID(slug + "-mcp-main")
	assert.Error(t, err, "主运行时应注销")
	_, err = rtRepo.GetByID(slug + "-mcp-aux")
	assert.Error(t, err, "同包辅助运行时也应注销")

	main, err := agentRepo.GetByID(slug + "-mcp-main-tool1")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusDelisted, main.Status)
	aux, err := agentRepo.GetByID(slug + "-mcp-aux-tool1")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusDelisted, aux.Status)
}

// TestUnregisterModule_CascadeDelistSKU 注销模块（非卸载）也级联下架 SKU 卡片：
// 集市卡片强关联模块，模块注销后对应 SKU 卡片即消失（delisted 保留购买记录）。
func TestUnregisterModule_CascadeDelistSKU(t *testing.T) {
	modSvc, _, agentRepo := newUninstallTestSvc(t)
	slug := "search-web"
	registerRuntime(t, modSvc, slug, slug, model.SkillRuntimeOriginEleballBuiltin, true)
	createDerivedSKU(t, agentRepo, slug+"-search", slug)
	createDerivedSKU(t, agentRepo, slug+"-fetch", slug)

	require.NoError(t, modSvc.UnregisterModule(slug))

	item, err := agentRepo.GetByID(slug + "-search")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusDelisted, item.Status, "模块注销后派生 SKU 应下架")
	item, err = agentRepo.GetByID(slug + "-fetch")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusDelisted, item.Status)

	// 无关模块的 SKU 不受影响
	createDerivedSKU(t, agentRepo, "other-tool", "other")
	other, err := agentRepo.GetByID("other-tool")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, other.Status)
}

// TestUnregisterModule_NonOwnedPrefixNotDelisted 前缀粗筛命中但 manifest 归属他包的 SKU 不被误下架。
func TestUnregisterModule_NonOwnedPrefixNotDelisted(t *testing.T) {
	modSvc, _, agentRepo := newUninstallTestSvc(t)
	slug := "agent"
	registerRuntime(t, modSvc, slug, slug, model.SkillRuntimeOriginEleballBuiltin, true)
	// ID 前缀 agent- 但 manifest 归属 agent-reach 包
	createDerivedSKU(t, agentRepo, "agent-reach-tool", "agent-reach")

	require.NoError(t, modSvc.UnregisterModule(slug))

	item, err := agentRepo.GetByID("agent-reach-tool")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, item.Status, "前缀命中但 manifest 归属他包的 SKU 不应被误下架")
}

// TestReconcileOrphanedSKUs_DelistsOrphan 旧版本残留：模块被注销（旧版只删运行时、不级联下架 SKU），
// 对账后其 approved SKU 卡片应下架（delisted 保留购买记录）。
func TestReconcileOrphanedSKUs_DelistsOrphan(t *testing.T) {
	modSvc, _, agentRepo := newUninstallTestSvc(t)
	slug := "old-module"
	// 派生 SKU（driver=非 none，manifest 归属旧模块）
	createDerivedSKU(t, agentRepo, slug+"-tool1", slug)
	// 模拟旧版本注销：只删运行时，无级联（registry.Unregister 直接调，绕过新级联逻辑）
	rt := &model.SkillRuntime{
		ID:          slug,
		Name:        "Old Module",
		Transport:   model.SkillRuntimeTransportExecute,
		Deployment:  model.SkillRuntimeDeploymentDocker,
		Status:      model.SkillRuntimeStatusOffline,
		PackageName: slug,
	}
	require.NoError(t, modSvc.registry.Register(rt))
	require.NoError(t, modSvc.registry.Unregister(slug)) // 旧版语义：无 SKU 级联

	require.NoError(t, modSvc.reconcileOrphanedSKUs(nil))

	item, err := agentRepo.GetByID(slug + "-tool1")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusDelisted, item.Status, "引用已不存在运行时的 approved SKU 应下架")
}

// TestReconcileOrphanedSKUs_KeepsValid 引用现存运行时 / prompt-only（driver=none）/ 无归属元数据的 SKU 保留。
func TestReconcileOrphanedSKUs_KeepsValid(t *testing.T) {
	modSvc, _, agentRepo := newUninstallTestSvc(t)
	// 1) 现存运行时 + 其 SKU
	registerRuntime(t, modSvc, "search-web", "search-web", model.SkillRuntimeOriginEleballBuiltin, true)
	createDerivedSKU(t, agentRepo, "search-web-baidu", "search-web")
	// 2) prompt-only 秘技（driver=none，metadata.module 指向无运行时的 skill 目录）
	promptSKU := &model.AgentItem{
		ID:       "skillmd-copywriting",
		Name:     "Copywriting",
		Category: "文案",
		Status:   model.AgentStatusApproved,
	}
	mf := model.ToolManifest{
		ID:       "skillmd-copywriting",
		Name:     "Copywriting",
		Driver:   model.ToolDriverNone,
		Category: "文案",
		Metadata: map[string]string{"module": "copywriting", "package_module": "copywriting"},
	}
	b, err := json.Marshal(mf)
	require.NoError(t, err)
	promptSKU.ManifestJSON = string(b)
	require.NoError(t, agentRepo.Create(promptSKU))
	// 3) 无归属元数据的 SKU（legacy 行，保守保留）
	legacySKU := &model.AgentItem{ID: "legacy-no-meta", Name: "Legacy", Status: model.AgentStatusApproved}
	lb, _ := json.Marshal(model.ToolManifest{ID: "legacy-no-meta", Name: "Legacy", Driver: model.ToolDriverType("x")})
	legacySKU.ManifestJSON = string(lb)
	require.NoError(t, agentRepo.Create(legacySKU))

	require.NoError(t, modSvc.reconcileOrphanedSKUs(nil))

	for _, id := range []string{"search-web-baidu", "skillmd-copywriting", "legacy-no-meta"} {
		item, err := agentRepo.GetByID(id)
		require.NoError(t, err)
		assert.Equal(t, model.AgentStatusApproved, item.Status, "%s 不应被下架", id)
	}

	// 幂等：再次对账无副作用
	require.NoError(t, modSvc.reconcileOrphanedSKUs(nil))
	for _, id := range []string{"search-web-baidu", "skillmd-copywriting"} {
		item, err := agentRepo.GetByID(id)
		require.NoError(t, err)
		assert.Equal(t, model.AgentStatusApproved, item.Status)
	}
}

// TestReconcileOrphanedSKUs_PackageSlugMatch 引用包 slug（非运行时 ID）的 SKU 命中现存包则保留。
func TestReconcileOrphanedSKUs_PackageSlugMatch(t *testing.T) {
	modSvc, _, agentRepo := newUninstallTestSvc(t)
	slug := "agent-reach"
	// 多运行时包：运行时 ID 带后缀，SKU manifest 用 package_module=slug 归属
	registerRuntime(t, modSvc, slug+"-mcp-main", slug, model.SkillRuntimeOriginEleballCloud, false)
	createDerivedSKU(t, agentRepo, slug+"-mcp-main-tool1", slug)

	require.NoError(t, modSvc.reconcileOrphanedSKUs(nil))

	item, err := agentRepo.GetByID(slug + "-mcp-main-tool1")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, item.Status, "包 slug 命中现存运行时应保留")
}

// 手写 SKU manifest 无 package_module 时，rescan 回填后补齐
// （运行时回填 PackageName/Title/Description + SKU manifest 注入包级元数据），使存量模块进入秘技包体系。
// TestBackfillPackageIdentity_UpgradeCompat 存量数据升级兼容：老库运行时无包身份、
// 手写 SKU manifest 无 package_module 时，rescan 回填后补齐
// （运行时回填 PackageName/Title/Description + SKU manifest 注入包级元数据），使存量模块进入秘技包体系。
func TestBackfillPackageIdentity_UpgradeCompat(t *testing.T) {
	modSvc, _, agentRepo := newUninstallTestSvc(t)
	// 老库：运行时无 PackageName（模拟历史版本物化的行）
	rt := &model.SkillRuntime{
		ID:          "search-web",
		Name:        "联网搜索（本地）",
		Description: "本地运行的网页搜索工具。",
		Transport:   model.SkillRuntimeTransportExecute,
		Deployment:  model.SkillRuntimeDeploymentDocker,
		Status:      model.SkillRuntimeStatusOffline,
		Official:    true, // 官方内置：无 category 时按兜底表回填「搜索」
	}
	require.NoError(t, modSvc.registry.Register(rt))
	// 老库：手写 SKU，manifest 只有 metadata.module，无 package_module
	sku := &model.AgentItem{
		ID:       "search-web-baidu",
		Name:     "百度千帆搜索",
		Category: "搜索",
		Status:   model.AgentStatusApproved,
	}
	mf := model.ToolManifest{
		ID:       "search-web-baidu",
		Name:     "百度千帆搜索",
		Driver:   model.ToolDriverType("search_web"),
		Metadata: map[string]string{"module": "search-web"},
	}
	b, err := json.Marshal(mf)
	require.NoError(t, err)
	sku.ManifestJSON = string(b)
	require.NoError(t, agentRepo.Create(sku))

	require.NoError(t, modSvc.backfillPackageIdentity(nil))

	// 运行时回填
	rt2, err := modSvc.repo.GetByID("search-web")
	require.NoError(t, err)
	assert.Equal(t, "search-web", rt2.PackageName)
	assert.Equal(t, "联网搜索（本地）", rt2.PackageTitle)
	assert.Equal(t, "本地运行的网页搜索工具。", rt2.PackageDescription)
	assert.Equal(t, "搜索", rt2.Category, "官方内置模块无 category 时按兜底表回填")

	// SKU manifest 注入包元数据
	item, err := agentRepo.GetByID("search-web-baidu")
	require.NoError(t, err)
	itemMF, err := item.Manifest()
	require.NoError(t, err)
	assert.Equal(t, "search-web", itemMF.Metadata["package_module"])
	assert.Equal(t, "联网搜索（本地）", itemMF.Metadata["package_title"])
	assert.Equal(t, "本地运行的网页搜索工具。", itemMF.Metadata["package_description"])

	// 幂等：再次回填无副作用（package_module 已存在则跳过）
	before := item.ManifestJSON
	require.NoError(t, modSvc.backfillPackageIdentity(nil))
	item2, err := agentRepo.GetByID("search-web-baidu")
	require.NoError(t, err)
	assert.Equal(t, before, item2.ManifestJSON)
}
