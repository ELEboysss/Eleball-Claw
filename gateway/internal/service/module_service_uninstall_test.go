package service

import (
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

// writeUserPackageDir 写一个 {slug} 包目录 + .origin 侧车（模拟本地 DIY 模块磁盘文件）。
func writeUserPackageDir(t *testing.T, slug, origin string) string {
	t.Helper()
	modDir := filepath.Join(os.Getenv("CLAW_MARKETPLACE_DIR"), slug)
	require.NoError(t, os.MkdirAll(modDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "package.json"),
		[]byte(`{"name":"`+slug+`","version":"0.1.0"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, ".origin"), []byte(origin), 0o644))
	return modDir
}

// TestUninstallModule_UserModule 非官方（user）模块真·卸载：目录删、运行时注销、SKU 下架。
func TestUninstallModule_UserModule(t *testing.T) {
	modSvc, rtRepo, agentRepo := newUninstallTestSvc(t)
	slug := "my-mcp"
	rtID := slug + "-mcp-main"
	modDir := writeUserPackageDir(t, slug, "user")

	// 注册运行时（PackageName=slug，Origin=user）
	rt := &model.SkillRuntime{
		ID:          rtID,
		Name:        "My MCP",
		Origin:      model.SkillRuntimeOriginUser,
		PackageName: slug,
		Transport:   model.SkillRuntimeTransportMCPStdio,
		Deployment:  model.SkillRuntimeDeploymentProcess,
		Status:      model.SkillRuntimeStatusActive,
	}
	require.NoError(t, modSvc.registry.Register(rt))

	// 造一个派生 SKU（ID 前缀 slug-，status=approved）
	sku := &model.AgentItem{
		ID:       slug + "-mcp-main-tool1",
		Name:     "tool1",
		Category: "互联网",
		Status:   model.AgentStatusApproved,
	}
	require.NoError(t, agentRepo.Create(sku))

	require.NoError(t, modSvc.UninstallModule(rtID))

	// 目录被删
	_, err := os.Stat(modDir)
	assert.True(t, os.IsNotExist(err), "卸载后 marketplace/<slug>/ 目录应删除")

	// 运行时注销
	_, err = rtRepo.GetByID(rtID)
	assert.Error(t, err, "卸载后运行时记录应注销")

	// SKU 下架（delisted，保留记录不硬删）
	item, err := agentRepo.GetByID(sku.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusDelisted, item.Status)
}

// TestUninstallModule_CloudOfficialRefused 官方云端模块（cloud + OfficialModuleIDs）拒绝卸载。
func TestUninstallModule_CloudOfficialRefused(t *testing.T) {
	modSvc, rtRepo, _ := newUninstallTestSvc(t)
	slug := "agent-reach"
	rtID := slug + "-mcp-main"
	modDir := writeUserPackageDir(t, slug, "cloud")

	rt := &model.SkillRuntime{
		ID:          rtID,
		Name:        "Agent Reach",
		Origin:      model.SkillRuntimeOriginCloud,
		PackageName: slug,
		Transport:   model.SkillRuntimeTransportMCPHTTP,
		Deployment:  model.SkillRuntimeDeploymentDocker,
		Status:      model.SkillRuntimeStatusActive,
	}
	require.NoError(t, modSvc.registry.Register(rt))

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

// TestUninstallModule_BuiltinRefused 内置模块（builtin）拒绝卸载。
func TestUninstallModule_BuiltinRefused(t *testing.T) {
	modSvc, rtRepo, _ := newUninstallTestSvc(t)
	slug := "search-web"
	rtID := slug

	rt := &model.SkillRuntime{
		ID:         rtID,
		Name:       "Search Web",
		Origin:     model.SkillRuntimeOriginBuiltin,
		Transport:  model.SkillRuntimeTransportExecute,
		Deployment: model.SkillRuntimeDeploymentProcess,
		Status:     model.SkillRuntimeStatusActive,
	}
	require.NoError(t, modSvc.registry.Register(rt))

	err := modSvc.UninstallModule(rtID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "官方模块")

	_, gErr := rtRepo.GetByID(rtID)
	assert.NoError(t, gErr, "内置模块运行时应保留")
}

// TestUninstallModule_NotFound 不存在的运行时 ID 报错。
func TestUninstallModule_NotFound(t *testing.T) {
	modSvc, _, _ := newUninstallTestSvc(t)
	err := modSvc.UninstallModule("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}
