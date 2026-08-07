package service

import (
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newSKUServiceTestDB 建立内存 SQLite + AgentItem 表，返回 AgentRepo。
func newSKUServiceTestDB(t *testing.T) *repository.AgentRepo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AgentItem{}))
	return repository.NewAgentRepo(db)
}

func autoSKUTestRuntime(id, driverID string) *model.SkillRuntime {
	return &model.SkillRuntime{
		ID:         id,
		Name:       id,
		DriverID:   driverID,
		Transport:  model.SkillRuntimeTransportMCPStdio,
		Deployment: model.SkillRuntimeDeploymentProcess,
		AutoSKU:    true,
	}
}

func mcpTestTools(names ...string) []MCPTool {
	tools := make([]MCPTool, 0, len(names))
	for _, n := range names {
		tools = append(tools, MCPTool{
			Name:        n,
			Description: n + " tool",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"msg": map[string]interface{}{"type": "string"},
				},
			},
		})
	}
	return tools
}

// TestDeriveSKUs_CreatesAndSyncs 首次派生：每个 tool 合成一个 approved 官方 SKU，
// manifest 的 driver/metadata.module/actions/parameters 正确。
func TestDeriveSKUs_CreatesAndSyncs(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mcp-stdio-echo", "mcp_stdio_echo")

	svc.DeriveSKUs(rt, mcpTestTools("echo", "ping"))

	echo, err := repo.GetByID("mcp-stdio-echo-echo")
	require.NoError(t, err)
	require.Equal(t, model.AgentStatusApproved, echo.Status)
	require.Equal(t, "官方", echo.CreatorName)
	require.Equal(t, int64(0), echo.PriceDanwan)

	mf, err := echo.Manifest()
	require.NoError(t, err)
	require.Equal(t, model.ToolDriverType("mcp_stdio_echo"), mf.Driver)
	require.Equal(t, "mcp-stdio-echo", mf.Metadata["module"])
	require.Equal(t, "mcp-stdio-echo", mf.Metadata["auto_sku_module"])
	require.Equal(t, "echo", mf.Actions[0].Name)
	require.Equal(t, "object", mf.Parameters["type"])

	// S1：派生 SKU 的 Name 应为工具名（非描述），Description 仍为工具描述。
	require.Equal(t, "echo", echo.Name, "AgentItem.Name 应为工具名")
	require.Equal(t, "echo", mf.Name, "manifest.Name 应为工具名")
	require.Equal(t, "echo tool", echo.Description, "AgentItem.Description 应为工具描述")
	require.Equal(t, "echo tool", mf.Description, "manifest.Description 应为工具描述")

	_, err = repo.GetByID("mcp-stdio-echo-ping")
	require.NoError(t, err)
}

// TestDeriveSKUs_Idempotent 工具集签名未变时跳过，不重复写库。
func TestDeriveSKUs_Idempotent(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mod-a", "drv_a")
	tools := mcpTestTools("echo", "ping")

	svc.DeriveSKUs(rt, tools)
	svc.DeriveSKUs(rt, tools) // 签名未变 -> 跳过

	items, err := repo.ListByModuleSKUs("mod-a")
	require.NoError(t, err)
	require.Len(t, items, 2) // 仍是 2 个，无重复
	for _, it := range items {
		require.Equal(t, model.AgentStatusApproved, it.Status)
	}
}

// TestDeriveSKUs_DelistAndReactivate 消失工具->下架（保留记录）；再次出现->重新上架。
func TestDeriveSKUs_DelistAndReactivate(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mod-b", "drv_b")

	svc.DeriveSKUs(rt, mcpTestTools("echo", "ping"))
	ping, _ := repo.GetByID("mod-b-ping")
	require.Equal(t, model.AgentStatusApproved, ping.Status)

	// ping 消失 -> 下架，echo 保留
	svc.DeriveSKUs(rt, mcpTestTools("echo"))
	ping, _ = repo.GetByID("mod-b-ping")
	require.Equal(t, model.AgentStatusDelisted, ping.Status)
	echo, _ := repo.GetByID("mod-b-echo")
	require.Equal(t, model.AgentStatusApproved, echo.Status)

	// ping 再次出现 -> 重新上架
	svc.DeriveSKUs(rt, mcpTestTools("echo", "ping"))
	ping, _ = repo.GetByID("mod-b-ping")
	require.Equal(t, model.AgentStatusApproved, ping.Status)
}

// TestDeriveSKUs_Guards AutoSKU=false 不派生；空工具列表不误下架。
func TestDeriveSKUs_Guards(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)

	// AutoSKU=false -> 不派生
	rt := autoSKUTestRuntime("mod-c", "drv_c")
	rt.AutoSKU = false
	svc.DeriveSKUs(rt, mcpTestTools("echo"))
	items, _ := repo.ListByModuleSKUs("mod-c")
	require.Empty(t, items)

	// 已有 SKU 后空工具列表 -> 跳过，不误下架（视为探活异常）
	rt2 := autoSKUTestRuntime("mod-e", "drv_e")
	svc.DeriveSKUs(rt2, mcpTestTools("echo", "ping"))
	svc.DeriveSKUs(rt2, []MCPTool{})
	echo, _ := repo.GetByID("mod-e-echo")
	require.Equal(t, model.AgentStatusApproved, echo.Status)
	ping, _ := repo.GetByID("mod-e-ping")
	require.Equal(t, model.AgentStatusApproved, ping.Status)
}

// TestDeriveSKUs_PreservesHandwrittenSKU 同前缀但非自动派生（无 auto_sku_module 标记）的 SKU 不被误下架。
func TestDeriveSKUs_PreservesHandwrittenSKU(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mod-f", "drv_f")

	handwritten := &model.AgentItem{
		ID:           "mod-f-custom",
		Name:         "Custom",
		ManifestJSON: `{"id":"mod-f-custom","name":"Custom","driver":"drv_f","parameters":{"type":"object"}}`,
		Status:       model.AgentStatusApproved,
	}
	require.NoError(t, repo.Create(handwritten))

	// 派生 echo；custom 不在新工具集，但无 auto_sku_module 标记 -> 保留
	svc.DeriveSKUs(rt, mcpTestTools("echo"))
	custom, _ := repo.GetByID("mod-f-custom")
	require.Equal(t, model.AgentStatusApproved, custom.Status)

	_, err := repo.GetByID("mod-f-echo")
	require.NoError(t, err)
}

// TestFilterTools 验证 G2 allow/deny 过滤：无配置全保留；白名单仅留白名单内；
// 黑名单排除；黑名单优先于白名单；nil rt / 空工具原样返回。
func TestFilterTools(t *testing.T) {
	tools := mcpTestTools("alpha", "beta", "gamma", "delta")

	// 无配置 -> 全保留
	rt := autoSKUTestRuntime("mod-t", "drv_t")
	require.Len(t, FilterTools(rt, tools), 4)

	// 白名单：仅保留 alpha/gamma
	rt.SetAllowedTools([]string{"alpha", "gamma"})
	got := FilterTools(rt, tools)
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0].Name)
	require.Equal(t, "gamma", got[1].Name)

	// 黑名单：排除 beta/delta
	rt2 := autoSKUTestRuntime("mod-t2", "drv_t2")
	rt2.SetDisallowedTools([]string{"beta", "delta"})
	got2 := FilterTools(rt2, tools)
	require.Len(t, got2, 2)
	require.Equal(t, "alpha", got2[0].Name)
	require.Equal(t, "gamma", got2[1].Name)

	// 白名单 + 黑名单：黑名单优先（gamma 在白名单但被黑名单排除 -> 仅 alpha）
	rt3 := autoSKUTestRuntime("mod-t3", "drv_t3")
	rt3.SetAllowedTools([]string{"alpha", "gamma"})
	rt3.SetDisallowedTools([]string{"gamma"})
	got3 := FilterTools(rt3, tools)
	require.Len(t, got3, 1)
	require.Equal(t, "alpha", got3[0].Name)

	// nil rt -> 原样返回；空工具列表 -> 空
	require.Len(t, FilterTools(nil, tools), 4)
	require.Len(t, FilterTools(rt, nil), 0)
}
