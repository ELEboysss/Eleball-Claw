package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// newUpdateTestSvc 构造 T4.4 更新检测测试环境：内存 SQLite（SkillRuntime 表）+ marketplace
// 临时根（CLAW_MARKETPLACE_DIR）。返回 ModuleService（rescan/EnrichCloudCatalog）。
func newUpdateTestSvc(t *testing.T) *ModuleService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))

	rtRepo := repository.NewSkillRuntimeRepo(db)
	reg := NewSkillRuntimeRegistry(nil)
	reg.SetRepo(rtRepo)
	modSvc := NewModuleService(reg, nil, rtRepo, nil)

	t.Setenv("CLAW_MARKETPLACE_DIR", t.TempDir())
	return modSvc
}

// writeUpdatePkg 写一个 {name} 版本的 package 目录并 rescan（模拟本地已安装某版本）。
func writeUpdatePkg(t *testing.T, modSvc *ModuleService, pkgID, version string) {
	t.Helper()
	modDir := filepath.Join(os.Getenv("CLAW_MARKETPLACE_DIR"), pkgID)
	require.NoError(t, os.MkdirAll(modDir, 0o755))
	pkgJSON := `{"name":"` + pkgID + `","version":"` + version + `","description":"upd pkg",` +
		`"mcpServers":{"main":{"transport":"http","url":"http://127.0.0.1:1/mcp"}}}`
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "package.json"), []byte(pkgJSON), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, ".origin"), []byte("cloud"), 0o644))
	require.NoError(t, modSvc.RescanPackage("claw", zap.NewNop()))
}

func catalogItem(pkgID, version string) model.PackageCatalogItem {
	return model.PackageCatalogItem{
		PackageID:   pkgID,
		Name:        pkgID,
		Version:     version,
		Origin:      "cloud",
		Official:    true,
		UpdatedAt:   time.Now(),
		Description: "catalog " + pkgID,
	}
}

// TestCompareVersions 宽松 semver 比较（T4.4 更新判定依赖，保证无新版不误报）。
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.0.0", 0},
		{"1.10.0", "1.9.0", 1},
		{"1.9.0", "1.10.0", -1},
		{"2.0.0", "1.99.99", 1},
		{"1.2", "1.2", 0},
		{"1.2", "1.2.0", 0}, // 短串缺省段补 0（"1.2" == "1.2.0"，避免写法差异误报）
		{"", "1.0.0", -1},   // 空串（catalog 未带版本）低于任何版本
		{"1.0.0", "", 1},
		{"1.0.0-beta", "1.0.0", -1}, // 预发布段字典序回退
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		assert.Equal(t, c.want, got, "compareVersions(%q, %q)", c.a, c.b)
	}
}

// TestEnrichCloudCatalog 更新检测（T4.4）：已装同版本→无更新；云端新版→has_update；
// 云端旧版→无更新；未安装→installed=false + local_version 空。
func TestEnrichCloudCatalog(t *testing.T) {
	modSvc := newUpdateTestSvc(t)
	writeUpdatePkg(t, modSvc, "up-pkg", "1.0.0")

	// 本地主 MCP 运行时已物化，version=package.json version
	rt, err := modSvc.GetModule("up-pkg-mcp-main")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", rt.Version)

	items := []model.PackageCatalogItem{
		catalogItem("up-pkg", "1.0.0"),    // 已装且同版本 → 无更新
		catalogItem("up-pkg", "1.1.0"),    // 云端新版 → 有更新
		catalogItem("up-pkg", "0.9.0"),    // 云端旧版 → 无更新
		catalogItem("other-pkg", "1.0.0"), // 未安装 → installed=false
	}
	got := modSvc.EnrichCloudCatalog(items)
	require.Len(t, got, 4)

	// 同版本：installed + local_version 透传，无更新
	assert.True(t, got[0].Installed)
	assert.Equal(t, "1.0.0", got[0].LocalVersion)
	assert.False(t, got[0].HasUpdate)
	assert.NotEmpty(t, got[0].LocalStatus)

	// 云端新版
	assert.True(t, got[1].Installed)
	assert.True(t, got[1].HasUpdate)

	// 云端旧版：不误报
	assert.True(t, got[2].Installed)
	assert.False(t, got[2].HasUpdate)

	// 未安装
	assert.False(t, got[3].Installed)
	assert.Empty(t, got[3].LocalVersion)
	assert.False(t, got[3].HasUpdate)
	assert.Empty(t, got[3].LocalStatus)
}
