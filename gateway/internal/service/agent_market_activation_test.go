package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// newActivationTestSvc 构造 T4.3 激活测试环境：内存 SQLite（含 purchase/user_tool 表）+
// marketplace 临时根（CLAW_MARKETPLACE_DIR）。返回 ModuleService（rescan/Start 分派目标）
// + AgentMarketService + agentRepo。
func newActivationTestSvc(t *testing.T) (*ModuleService, *AgentMarketService, *repository.AgentRepo, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.AgentItem{}, &model.SkillRuntime{}, &model.AgentPurchase{}, &model.AgentUserTool{}))

	agentRepo := repository.NewAgentRepo(db)
	rtRepo := repository.NewSkillRuntimeRepo(db)
	reg := NewSkillRuntimeRegistry(nil)
	reg.SetRepo(rtRepo)
	modSvc := NewModuleService(reg, nil, rtRepo, agentRepo)

	root := t.TempDir()
	t.Setenv("CLAW_MARKETPLACE_DIR", root)

	market := NewAgentMarketService(db, agentRepo, nil, nil, reg)
	market.SetLocalFreeOnly(true)
	market.SetModuleService(modSvc)
	return modSvc, market, agentRepo, root
}

// writeActivationPkg 写一个含 tool(http)/mcp(http)/skill 三段的 package 目录。
// 端点指向不可达端口（127.0.0.1:1），Start/ForceProbe 快速失败为 degraded，不 spawn 子进程。
func writeActivationPkg(t *testing.T, root string) {
	t.Helper()
	modDir := filepath.Join(root, "act-pkg")
	require.NoError(t, os.MkdirAll(filepath.Join(modDir, "skills", "writer"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "skills", "writer", "SKILL.md"),
		[]byte("---\nname: writer\ndescription: 写作专家\n---\n\n你是写作专家\n"), 0o644))
	pkgJSON := `{
	  "name":"act-pkg","version":"1.0.0","description":"act pkg","level":2,
	  "tools":[{"name":"fetch","description":"抓取","transport":"http","endpoint":"http://127.0.0.1:1/fetch"}],
	  "mcpServers":{"main":{"transport":"http","url":"http://127.0.0.1:1/mcp"}},
	  "skills":[{"name":"writer","description":"写作"}]
	}`
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "package.json"), []byte(pkgJSON), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, ".origin"), []byte("cloud"), 0o644))
}

// purchaseSKUs 直接建购买记录（绕过 PurchaseAgent 的驱动注册/余额门禁，聚焦激活行为）。
func purchaseSKUs(t *testing.T, agentRepo *repository.AgentRepo, userID string, agentIDs ...string) {
	t.Helper()
	for _, id := range agentIDs {
		require.NoError(t, agentRepo.CreatePurchase(&model.AgentPurchase{
			ID:        uuid.New().String(),
			AgentID:   id,
			BuyerID:   userID,
			PricePaid: 0,
		}))
	}
}

// TestActivation_KindDispatch 三类 SKU 激活各自生效（T4.3）：
// tool→Start 拉起 runtime、mcp→Start 连接、skill→载入 prompt（无 runtime，不触发 Start）。
// 未购买（D9 门禁）拒绝激活并返回 ErrNotPurchased。
func TestActivation_KindDispatch(t *testing.T) {
	modSvc, market, agentRepo, root := newActivationTestSvc(t)
	writeActivationPkg(t, root)
	require.NoError(t, modSvc.RescanPackage("claw", zap.NewNop()))
	const userID = "u1"

	// D9 门禁：未购买 → ErrNotPurchased
	_, err := market.ToggleAgentActive(userID, "act-pkg-fetch")
	require.ErrorIs(t, err, ErrNotPurchased)

	// 购买三类 SKU
	purchaseSKUs(t, agentRepo, userID, "act-pkg-fetch", "act-pkg-mcp-main", "act-pkg-skill-writer")

	// tool SKU：激活 → active 置位 + Start 分派（runtime 被探活，状态离开 installed）
	on, err := market.ToggleAgentActive(userID, "act-pkg-fetch")
	require.NoError(t, err)
	require.True(t, on)
	active, _ := agentRepo.IsToolActive(userID, "act-pkg-fetch")
	assert.True(t, active, "tool SKU 激活后 active 置位")
	st := modSvc.CheckRuntime("act-pkg-fetch")
	require.NotNil(t, st, "tool 派生 SKU 的 Driver=运行时 ID，Start 应命中该 runtime")
	assert.NotEqual(t, model.SkillRuntimeStatusInstalled, st.Status, "tool 激活应触发 Start 探活（degraded/active）")

	// mcp SKU：激活 → Start 连接（external MCP → ForceProbe → manager.connect 或直连探活）
	on, err = market.ToggleAgentActive(userID, "act-pkg-mcp-main")
	require.NoError(t, err)
	require.True(t, on)
	active, _ = agentRepo.IsToolActive(userID, "act-pkg-mcp-main")
	assert.True(t, active, "mcp SKU 激活后 active 置位")
	st = modSvc.CheckRuntime("act-pkg-mcp-main")
	require.NotNil(t, st, "mcp 派生 SKU 的 Driver=运行时 ID，Start 应命中该 runtime")
	assert.NotEqual(t, model.SkillRuntimeStatusInstalled, st.Status, "mcp 激活应触发 Start 连接探活")

	// skill SKU：激活 → 无 runtime（Driver=none），纯 prompt 载入，不触发 Start 也不报错
	on, err = market.ToggleAgentActive(userID, "act-pkg-skill-writer")
	require.NoError(t, err)
	require.True(t, on)
	active, _ = agentRepo.IsToolActive(userID, "act-pkg-skill-writer")
	assert.True(t, active, "skill SKU 激活后 active 置位")

	// 停用：仅清 active，不触碰 runtime
	on, err = market.ToggleAgentActive(userID, "act-pkg-fetch")
	require.NoError(t, err)
	require.False(t, on)
	active, _ = agentRepo.IsToolActive(userID, "act-pkg-fetch")
	assert.False(t, active, "停用后 active 清除")
}

// TestActivation_PackageBatch 整包激活（T4.3 快捷入口）：批量激活包内全部已购 SKU。
// 未购买 SKU 跳过不阻断；重复调用幂等（已激活不计入）。
func TestActivation_PackageBatch(t *testing.T) {
	modSvc, market, agentRepo, root := newActivationTestSvc(t)
	writeActivationPkg(t, root)
	require.NoError(t, modSvc.RescanPackage("claw", zap.NewNop()))
	const userID = "u1"

	// 只购买 tool+skill（mcp 未购买 → 整包激活跳过）
	purchaseSKUs(t, agentRepo, userID, "act-pkg-fetch", "act-pkg-skill-writer")

	n, err := market.ActivatePackageSKUs(userID, "act-pkg")
	require.NoError(t, err)
	assert.Equal(t, 2, n, "未购买的 mcp SKU 跳过，tool+skill 激活")

	for _, id := range []string{"act-pkg-fetch", "act-pkg-skill-writer"} {
		active, _ := agentRepo.IsToolActive(userID, id)
		assert.True(t, active, "整包激活后 %s active 置位", id)
	}
	active, _ := agentRepo.IsToolActive(userID, "act-pkg-mcp-main")
	assert.False(t, active, "未购买的 mcp SKU 不被激活")

	// 幂等：重复整包激活不计入（均已激活）
	n, err = market.ActivatePackageSKUs(userID, "act-pkg")
	require.NoError(t, err)
	assert.Equal(t, 0, n, "重复整包激活幂等")
}
