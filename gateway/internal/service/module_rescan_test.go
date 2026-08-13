package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// newRescanTestSvc 构造隔离的 ModuleService（内存 SQLite + temp marketplace 根）。
// claw 无 SetMarketplaceRoot，经 CLAW_MARKETPLACE_DIR 指向 temp 根（包级 ResolveMarketplaceRoot 读取）；
// RescanPackage 只做纯扫描（播种由 seed 包装器负责），故 temp 根即全量事实源。
// 单连接避免「no such table」；agentRepo 注入使 RescanPackage 物化 AgentItem。
func newRescanTestSvc(t *testing.T) (*ModuleService, *repository.AgentRepo, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.AgentItem{}, &model.SkillRuntime{}))

	agentRepo := repository.NewAgentRepo(db)
	rtRepo := repository.NewSkillRuntimeRepo(db)
	reg := NewSkillRuntimeRegistry(nil)
	reg.SetRepo(rtRepo)
	svc := NewModuleService(reg, nil, rtRepo, agentRepo)
	root := t.TempDir()
	t.Setenv("CLAW_MARKETPLACE_DIR", root)
	return svc, agentRepo, root
}

// writeModuleDir 写 legacy module.json 目录（含可选 skus/*.json）。
func writeModuleDir(t *testing.T, root, id, origin string, skus map[string]string) {
	t.Helper()
	modDir := filepath.Join(root, id)
	require.NoError(t, os.MkdirAll(modDir, 0o755))
	modJSON := `{"id":"` + id + `","name":"` + id + `","origin":"` + origin + `","transport":"execute","deployment":"process","command":"python3","args":["main.py"],"driver":{"driver_id":"` + id + `"}}`
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "module.json"), []byte(modJSON), 0o644))
	if len(skus) > 0 {
		require.NoError(t, os.MkdirAll(filepath.Join(modDir, "skus"), 0o755))
		for name, content := range skus {
			require.NoError(t, os.WriteFile(filepath.Join(modDir, "skus", name), []byte(content), 0o644))
		}
	}
}

// TestRescanPackage_LegacyModuleDir 物化 legacy module.json 目录：SkillRuntime + 手写 SKU。
// 重复扫描幂等，不产生重复记录。
func TestRescanPackage_LegacyModuleDir(t *testing.T) {
	svc, agentRepo, root := newRescanTestSvc(t)
	writeModuleDir(t, root, "demo-mod", "cloud", map[string]string{
		"hello.json": `{"id":"demo-mod-hello","name":"Hello","description":"demo","driver":"demo-mod","parameters":{"type":"object","properties":{}}}`,
	})

	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))

	// SkillRuntime 物化：origin=cloud，非官方列表（demo-mod）→ Official=false
	rt, err := svc.GetModule("demo-mod")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeStatusInstalled, rt.Status)
	assert.Equal(t, model.SkillRuntimeOriginCloud, rt.Origin)
	assert.False(t, rt.Official)
	assert.Equal(t, model.SkillRuntimeTransportExecute, rt.Transport)
	assert.Equal(t, model.SkillRuntimeDeploymentProcess, rt.Deployment)
	assert.Equal(t, "python3", rt.Command)

	// 手写 SKU 物化
	item, err := agentRepo.GetByID("demo-mod-hello")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, item.Status)
	assert.Equal(t, "Hello", item.Name)
	assert.Equal(t, "官方", item.CreatorName)
	mf, err := item.Manifest()
	require.NoError(t, err)
	assert.Equal(t, model.ToolDriverType("demo-mod"), mf.Driver)

	// 幂等：重扫不产生重复
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))
	totalRT, err := svc.repo.List()
	require.NoError(t, err)
	assert.Len(t, totalRT, 1)
	totalSKU, err := agentRepo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(1), totalSKU)
}

// TestRescanPackage_PackageDir 物化 package.json 目录（T2.2 主路径）：
// tools/mcpServers → SkillRuntime；tool/skill/mcp 派生 AgentItem。幂等。
func TestRescanPackage_PackageDir(t *testing.T) {
	svc, agentRepo, root := newRescanTestSvc(t)
	modDir := filepath.Join(root, "demo-pkg")
	require.NoError(t, os.MkdirAll(filepath.Join(modDir, "skills", "writer"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "skills", "writer", "SKILL.md"), []byte("---\nname: writer\ndescription: 写作专家\n---\n\n你是写作专家\n"), 0o644))
	pkgJSON := `{
  "name": "demo-pkg",
  "version": "1.0.0",
  "description": "demo package",
  "category": "工具",
  "level": 2,
  "tools": [
    {"name": "calc", "description": "计算器", "transport": "process", "command": ["python3", "calc.py"]},
    {"name": "web_fetch", "description": "抓取", "transport": "http", "endpoint": "https://api.example.com/fetch"}
  ],
  "mcpServers": {
    "search": {"transport": "http", "url": "https://mcp.example.com/search"}
  },
  "skills": [
    {"name": "writer", "description": "写作"}
  ]
}`
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "package.json"), []byte(pkgJSON), 0o644))

	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))

	// tools → SkillRuntime
	calcRT, err := svc.GetModule("demo-pkg-calc")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeTransportExecute, calcRT.Transport)
	assert.Equal(t, model.SkillRuntimeDeploymentProcess, calcRT.Deployment)
	assert.Equal(t, "python3", calcRT.Command)
	assert.Equal(t, modDir, calcRT.WorkDir)
	assert.Equal(t, "1.0.0", calcRT.Version) // T2.3：SkillRuntime 记录 package version

	fetchRT, err := svc.GetModule("demo-pkg-web_fetch")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeTransportRawHTTP, fetchRT.Transport)
	assert.Equal(t, model.SkillRuntimeDeploymentExternal, fetchRT.Deployment)
	assert.Equal(t, "https://api.example.com/fetch", fetchRT.Endpoint)
	assert.Equal(t, "1.0.0", fetchRT.Version)

	// mcpServers → SkillRuntime
	mcpRT, err := svc.GetModule("demo-pkg-mcp-search")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeTransportMCPHTTP, mcpRT.Transport)
	cfg := mcpRT.GetMCPServerConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "https://mcp.example.com/search", cfg.URL)
	assert.Equal(t, "1.0.0", mcpRT.Version)

	// 派生 SKU
	calc, err := agentRepo.GetByID("demo-pkg-calc")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, calc.Status)
	assert.Equal(t, "工具", calc.Category)
	assert.Equal(t, model.AgentLevelXuan, calc.Level)
	assert.Equal(t, "1.0.0", calc.Version) // T2.3：AgentItem 记录派生源版本
	mc, err := calc.Manifest()
	require.NoError(t, err)
	assert.Equal(t, model.ToolDriverType("demo-pkg-calc"), mc.Driver)
	assert.Equal(t, "demo-pkg", mc.Metadata["package_module"])
	assert.Equal(t, "1.0.0", mc.Version)
	assert.Equal(t, "tool", mc.Metadata["package_derived"])

	fetch, err := agentRepo.GetByID("demo-pkg-web_fetch")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, fetch.Status)
	assert.Equal(t, "1.0.0", fetch.Version)
	mf, err := fetch.Manifest()
	require.NoError(t, err)
	assert.Equal(t, "remote", mf.RuntimeType)

	mcp, err := agentRepo.GetByID("demo-pkg-mcp-search")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, mcp.Status)
	assert.Equal(t, "1.0.0", mcp.Version)
	mm, err := mcp.Manifest()
	require.NoError(t, err)
	assert.Equal(t, "mcp", mm.Metadata["package_derived"])

	// skills → prompt-only 派生 SKU（body 即 SystemPrompt）
	writer, err := agentRepo.GetByID("demo-pkg-skill-writer")
	require.NoError(t, err)
	assert.Equal(t, "你是写作专家", writer.SystemPrompt)
	assert.Equal(t, "1.0.0", writer.Version)
	mw, err := writer.Manifest()
	require.NoError(t, err)
	assert.Equal(t, model.ToolDriverNone, mw.Driver)

	// 幂等
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))
	totalRT, err := svc.repo.List()
	require.NoError(t, err)
	assert.Len(t, totalRT, 3)
	totalSKU, err := agentRepo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(4), totalSKU)
}

// TestRescanPackage_VersionBump 升级 package.json version 后重扫 → SkillRuntime 与派生 SKU 的
// Version 同步刷新（T2.3「version 驱动更新检测」的数据地基；比对逻辑在 T4.4 消费）。
func TestRescanPackage_VersionBump(t *testing.T) {
	svc, agentRepo, root := newRescanTestSvc(t)
	modDir := filepath.Join(root, "ver-pkg")
	require.NoError(t, os.MkdirAll(modDir, 0o755))
	writeVerPkg := func(version string) {
		pkgJSON := `{"name":"ver-pkg","version":"` + version + `","description":"ver pkg","tools":[{"name":"t1","description":"t","transport":"process","command":["python3","t.py"]}]}`
		require.NoError(t, os.WriteFile(filepath.Join(modDir, "package.json"), []byte(pkgJSON), 0o644))
	}

	writeVerPkg("1.0.0")
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))
	rt, err := svc.GetModule("ver-pkg-t1")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", rt.Version)
	sku, err := agentRepo.GetByID("ver-pkg-t1")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", sku.Version)

	// 升级到 2.0.0：重扫后版本同步刷新
	writeVerPkg("2.0.0")
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))
	rt2, err := svc.GetModule("ver-pkg-t1")
	require.NoError(t, err)
	assert.Equal(t, "2.0.0", rt2.Version)
	sku2, err := agentRepo.GetByID("ver-pkg-t1")
	require.NoError(t, err)
	assert.Equal(t, "2.0.0", sku2.Version)
	mf2, err := sku2.Manifest()
	require.NoError(t, err)
	assert.Equal(t, "2.0.0", mf2.Version)
}

// TestRescanPackage_DelistsStaleHandwritten 源 skus/*.json 删除后重扫 → 手写 SKU 下架
// （保留购买记录不硬删）；仍存在的 SKU 不受影响。
func TestRescanPackage_DelistsStaleHandwritten(t *testing.T) {
	svc, agentRepo, root := newRescanTestSvc(t)
	writeModuleDir(t, root, "demo-mod", "cloud", map[string]string{
		"hello.json": `{"id":"demo-mod-hello","name":"Hello","description":"demo","driver":"demo-mod","parameters":{"type":"object","properties":{}}}`,
		"keep.json":  `{"id":"demo-mod-keep","name":"Keep","description":"keep","driver":"demo-mod","parameters":{"type":"object","properties":{}}}`,
	})
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))

	// 删除 hello.json（keep 保留）
	require.NoError(t, os.Remove(filepath.Join(root, "demo-mod", "skus", "hello.json")))
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))

	got, err := agentRepo.GetByID("demo-mod-hello")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusDelisted, got.Status)

	keep, err := agentRepo.GetByID("demo-mod-keep")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, keep.Status)
}

// TestRescanPackage_PromptOnlySkill 仅有 SKILL.md 的目录 → prompt-only SKU（driver=none）。
func TestRescanPackage_PromptOnlySkill(t *testing.T) {
	svc, agentRepo, root := newRescanTestSvc(t)
	skillDir := filepath.Join(root, "copywriting")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	skillMD := "---\nname: copywriting\ndescription: 文案撰写专家\nmetadata:\n  category: 写作\n---\n\n你是文案撰写专家。\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644))

	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))

	item, err := agentRepo.GetByID("skillmd-copywriting")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, item.Status)
	assert.Equal(t, "写作", item.Category) // metadata.category 覆盖默认「提示」
	assert.Equal(t, "你是文案撰写专家。", item.SystemPrompt)
	mf, err := item.Manifest()
	require.NoError(t, err)
	assert.Equal(t, model.ToolDriverNone, mf.Driver)

	// 不建 SkillRuntime（纯 prompt 无进程/docker）
	totalRT, err := svc.repo.List()
	require.NoError(t, err)
	assert.Empty(t, totalRT)
}

// TestRescanPackage_SideFiltersBuiltin cloud 侧不收录 builtin 模块的 SKU（但仍物化 SkillRuntime）；
// claw 侧收录全部。
func TestRescanPackage_SideFiltersBuiltin(t *testing.T) {
	svc, agentRepo, root := newRescanTestSvc(t)
	writeModuleDir(t, root, "builtin-mod", "builtin", map[string]string{
		"one.json": `{"id":"builtin-mod-one","name":"One","description":"builtin","driver":"builtin-mod","parameters":{"type":"object","properties":{}}}`,
	})

	// cloud 侧：SkillRuntime 物化（builtin 恒官方），SKU 不收录
	require.NoError(t, svc.RescanPackage("cloud", zap.NewNop()))
	rt, err := svc.GetModule("builtin-mod")
	require.NoError(t, err)
	assert.True(t, rt.Official)
	_, err = agentRepo.GetByID("builtin-mod-one")
	require.Error(t, err) // 未收录

	// claw 侧：SKU 收录
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))
	item, err := agentRepo.GetByID("builtin-mod-one")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, item.Status)
}

// TestRescanPackage_PackageOriginSidecar package.json 目录的 origin 走 .origin 侧车；
// 缺失按 side 默认（cloud→cloud，claw→builtin）。
func TestRescanPackage_PackageOriginSidecar(t *testing.T) {
	svc, agentRepo, root := newRescanTestSvc(t)
	modDir := filepath.Join(root, "u-pkg")
	require.NoError(t, os.MkdirAll(modDir, 0o755))
	pkgJSON := `{"name":"u-pkg","version":"1.0.0","description":"user pkg","tools":[{"name":"t1","description":"t","transport":"http","endpoint":"https://x"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "package.json"), []byte(pkgJSON), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, ".origin"), []byte("user"), 0o644))

	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))
	rt, err := svc.GetModule("u-pkg-t1")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeOriginUser, rt.Origin)
	assert.False(t, rt.Official)

	item, err := agentRepo.GetByID("u-pkg-t1")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, item.Status)

	// 无 .origin：claw 默认 builtin
	modDir2 := filepath.Join(root, "b-pkg")
	require.NoError(t, os.MkdirAll(modDir2, 0o755))
	pkgJSON2 := `{"name":"b-pkg","version":"1.0.0","description":"b pkg","tools":[{"name":"t1","description":"t","transport":"http","endpoint":"https://x"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(modDir2, "package.json"), []byte(pkgJSON2), 0o644))
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))
	rt2, err := svc.GetModule("b-pkg-t1")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeOriginBuiltin, rt2.Origin)
}

// TestApplyPackage_FullLanding 验证 T4.2 整包落盘：ApplyPackage 把 PackageBundle 解包到
// marketplace/<id>/（package.json + .origin + skills/*/SKILL.md + skus/*.json + tools_files），
// RescanPackage 物化 MCP 运行时 + 派生 SKU + 手写 SKU。skill/mcp/tool 三类全落盘。
// mcpServers 用 http（External）避免测试 spawn 子进程；探活指向不可达端口快速失败。
func TestApplyPackage_FullLanding(t *testing.T) {
	svc, agentRepo, root := newRescanTestSvc(t)
	bundle := model.PackageBundle{
		PackageVersion: 2,
		PackageID:      "demo-pkg",
		Version:        "1.0.0",
		PackageJSON: json.RawMessage(`{
		  "name":"demo-pkg","version":"1.0.0","description":"demo package","level":2,
		  "mcpServers":{"main":{"transport":"http","url":"http://127.0.0.1:1/mcp"}},
		  "skills":[{"name":"writer","description":"写作"}]
		}`),
		Skills: []model.PackageBundleSkill{{Name: "writer", Content: "---\nname: writer\ndescription: 写作专家\n---\n\n你是写作专家\n"}},
		ToolsFiles: map[string]string{
			".origin": "user",
			"main.py": "print('hi')\n",
		},
		SKUs: []model.PackageBundleSKU{{FileName: "echo", Content: json.RawMessage(`{"id":"demo-pkg-echo","name":"Echo","description":"回显","driver":"demo-pkg-mcp-main"}`)}},
	}

	rec, err := svc.ApplyPackage(bundle)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, "demo-pkg-mcp-main", rec.ID, "package 布局运行时 ID = {pkg}-mcp-main")

	// 落盘完整性
	modDir := filepath.Join(root, "demo-pkg")
	assert.FileExists(t, filepath.Join(modDir, "package.json"))
	originB, err := os.ReadFile(filepath.Join(modDir, ".origin"))
	require.NoError(t, err)
	assert.Equal(t, "user", strings.TrimSpace(string(originB)))
	assert.FileExists(t, filepath.Join(modDir, "skills", "writer", "SKILL.md"))
	assert.FileExists(t, filepath.Join(modDir, "main.py"))
	assert.FileExists(t, filepath.Join(modDir, "skus", "echo.json"))

	// MCP 运行时物化（http → MCPHTTP/External）
	rt, err := svc.GetModule("demo-pkg-mcp-main")
	require.NoError(t, err)
	assert.Equal(t, model.SkillRuntimeOriginUser, rt.Origin, ".origin 侧车决定 provenance")
	assert.Equal(t, model.SkillRuntimeTransportMCPHTTP, rt.Transport)
	assert.Equal(t, model.SkillRuntimeDeploymentExternal, rt.Deployment)
	assert.Equal(t, "http://127.0.0.1:1/mcp", rt.Endpoint)
	assert.Equal(t, "1.0.0", rt.Version)

	// 派生 MCP SKU + skill SKU + 手写 echo SKU 全部物化（skill/mcp/tool 三类）
	for _, skuID := range []string{"demo-pkg-mcp-main", "demo-pkg-skill-writer", "demo-pkg-echo"} {
		item, err := agentRepo.GetByID(skuID)
		require.NoError(t, err, "SKU %s 应物化", skuID)
		require.NotNil(t, item, "SKU %s 应物化", skuID)
		assert.Equal(t, model.AgentStatusApproved, item.Status)
	}
	writer, err := agentRepo.GetByID("demo-pkg-skill-writer")
	require.NoError(t, err)
	assert.Equal(t, "你是写作专家", writer.SystemPrompt)

	// 幂等：重复 ApplyPackage（同版本覆盖）不产生重复记录
	_, err = svc.ApplyPackage(bundle)
	require.NoError(t, err)
	totalSKU, err := agentRepo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(3), totalSKU)
}
