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

// TestDeriveSKUs_PseudoToolMetadata 验证 read_resource/get_prompt 伪工具派生的 SKU
// manifest.metadata 标注 pseudo_tool（M5，供 UI 区分「资源读取器/提示获取器」）。
func TestDeriveSKUs_PseudoToolMetadata(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mod-res", "drv_res")

	tools := []MCPTool{
		{Name: "echo", Description: "echo tool"},
		mcpReadResourcePseudoTool([]MCPResource{{URI: "file:///a", Name: "A"}}),
		mcpGetPromptPseudoTool([]MCPPrompt{{Name: "greet"}}),
	}
	svc.DeriveSKUs(rt, tools)

	rr, err := repo.GetByID("mod-res-read_resource")
	require.NoError(t, err)
	mf, err := rr.Manifest()
	require.NoError(t, err)
	require.Equal(t, "resource", mf.Metadata["pseudo_tool"])

	gp, err := repo.GetByID("mod-res-get_prompt")
	require.NoError(t, err)
	mf2, err := gp.Manifest()
	require.NoError(t, err)
	require.Equal(t, "prompt", mf2.Metadata["pseudo_tool"])

	// 普通工具无 pseudo_tool 标注
	echo, _ := repo.GetByID("mod-res-echo")
	mfE, _ := echo.Manifest()
	_, hasPseudo := mfE.Metadata["pseudo_tool"]
	require.False(t, hasPseudo)
}

// TestDeriveSKUs_TitleAsDisplayName MCP 工具的 title 字段作为 SKU 显示名（Name），
// identifier name 仍用于 Actions[0].Name 与 manifest.ID（dispatch/LLM 调用名不变）。
// 无 title 时回退工具名（TestDeriveSKUs_CreatesAndSyncs 已覆盖）。
func TestDeriveSKUs_TitleAsDisplayName(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("firecrawl", "firecrawl")

	tools := []MCPTool{{
		Name:        "scrape",
		Title:       "Firecrawl Scrape",
		Description: "基于 Firecrawl 的网页抓取：将单个网页转换为干净 Markdown",
		InputSchema: map[string]interface{}{"type": "object"},
	}}
	svc.DeriveSKUs(rt, tools)

	item, err := repo.GetByID("firecrawl-scrape")
	require.NoError(t, err)
	require.Equal(t, "Firecrawl Scrape", item.Name, "AgentItem.Name 应取 title")
	require.Equal(t, "基于 Firecrawl 的网页抓取：将单个网页转换为干净 Markdown", item.Description)

	mf, err := item.Manifest()
	require.NoError(t, err)
	require.Equal(t, "Firecrawl Scrape", mf.Name, "manifest.Name 应取 title")
	require.Equal(t, "scrape", mf.Actions[0].Name, "Actions[0].Name 仍为 identifier name")
	require.Equal(t, "firecrawl-scrape", mf.ID, "manifest.ID 用 identifier name 派生，不含 title")
}

// TestDeriveSKUs_SyncsPriceOnUpdate 回归：已存在的 SKU 行（如 firecrawl 从手写迁移到 auto_sku
// 前残留的 price=50 行）被 DeriveSKUs 复用时，price/level 列必须同步到 manifest 值（auto_sku 派生
// price=0）。此前 update 路径漏同步 PriceDanwan -> DB 列残留旧价，卡片显示非免费价。
func TestDeriveSKUs_SyncsPriceOnUpdate(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mod-p", "drv_p")

	// 预置旧手写 SKU 行：price=50、旧名、已 approved（模拟 firecrawl 迁 auto_sku 前的残留行）。
	require.NoError(t, repo.Create(&model.AgentItem{
		ID:           "mod-p-scrape",
		Name:         "旧手写名",
		Description:  "旧描述",
		PriceDanwan:  50,
		ManifestJSON: `{"id":"mod-p-scrape","name":"旧手写名","driver":"drv_p","price_danwan":50,"parameters":{"type":"object"}}`,
		Status:       model.AgentStatusApproved,
	}))

	tools := []MCPTool{{
		Name:        "scrape",
		Title:       "Firecrawl Scrape",
		Description: "基于 Firecrawl 的网页抓取",
		InputSchema: map[string]interface{}{"type": "object"},
	}}
	svc.DeriveSKUs(rt, tools)

	item, err := repo.GetByID("mod-p-scrape")
	require.NoError(t, err)
	require.Equal(t, int64(0), item.PriceDanwan, "PriceDanwan 应同步为 manifest 的 0（auto_sku 免费），不应残留旧价 50")
	require.Equal(t, "Firecrawl Scrape", item.Name, "Name 应取 title")
	require.Equal(t, model.AgentStatusApproved, item.Status, "复用行应重新置 approved")
}

// TestDeriveSKUs_PinProtectsOverriddenFields 回归：admin 钉住(pinned)的展示字段(name/price)
// 在 DeriveSKUs 派生同步时不被覆写，保留 admin 改的值；未 pin 的字段(description)仍随 manifest
// 派生值刷新；manifest_json(派生源)始终同步（不受 pin 影响）。此为 admin-web SKU 展示管理的基础不变量。
func TestDeriveSKUs_PinProtectsOverriddenFields(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mod-q", "drv_q")

	// 预置已 approved 的 SKU：admin 已改 name+price 并 pin 住；description 未 pin（将随派生刷新）。
	item := &model.AgentItem{
		ID:           "mod-q-scrape",
		Name:         "Admin自定义名",
		Description:  "旧描述",
		PriceDanwan:  999,
		ManifestJSON: `{"id":"mod-q-scrape","name":"旧派生名","driver":"drv_q","price_danwan":0,"description":"旧派生描述","parameters":{"type":"object"}}`,
		Status:       model.AgentStatusApproved,
	}
	item.AddPin("name", "price_danwan") // 钉住 name + price_danwan
	require.Equal(t, `["name","price_danwan"]`, item.PinnedFields)
	require.NoError(t, repo.Create(item))

	tools := []MCPTool{{
		Name:        "scrape",
		Title:       "派生名",
		Description: "派生描述",
		InputSchema: map[string]interface{}{"type": "object"},
	}}
	svc.DeriveSKUs(rt, tools)

	got, err := repo.GetByID("mod-q-scrape")
	require.NoError(t, err)
	require.Equal(t, "Admin自定义名", got.Name, "pinned name 不应被派生名覆写")
	require.Equal(t, int64(999), got.PriceDanwan, "pinned price 不应被派生 0 覆写")
	require.Equal(t, "派生描述", got.Description, "未 pin 的 description 应随派生刷新")
	require.Equal(t, `["name","price_danwan"]`, got.PinnedFields, "PinnedFields 不应变")

	// manifest_json(派生源)始终同步：解析后 Name/Description 为派生值（非 admin 覆盖值）。
	mf, err := got.Manifest()
	require.NoError(t, err)
	require.Equal(t, "派生名", mf.Name, "manifest.Name 为派生源值，不受 pin 影响")
	require.Equal(t, "派生描述", mf.Description)
}

// TestDeriveSKUs_RecordsVersionAndKind T2.3：派生 SKU 记录版本与能力种类。
// 版本写入 AgentItem.Version + manifest.Version + metadata.package_version；kind 写入 package_derived。
func TestDeriveSKUs_RecordsVersionAndKind(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mod-v", "drv_v")
	rt.Version = "1.2.0"

	svc.DeriveSKUs(rt, mcpTestTools("echo"))

	item, err := repo.GetByID("mod-v-echo")
	require.NoError(t, err)
	require.Equal(t, "1.2.0", item.Version, "AgentItem.Version 记录派生源版本")

	mf, err := item.Manifest()
	require.NoError(t, err)
	require.Equal(t, "1.2.0", mf.Version, "manifest.Version 记录派生源版本")
	require.Equal(t, "1.2.0", mf.Metadata["package_version"])
	require.Equal(t, "mcp", mf.Metadata["package_derived"], "stdio MCP 运行时 → kind=mcp")
}

// TestDeriveSKUs_ToolKind 非 MCP 传输（execute）的 auto_sku 运行时 → kind=tool。
func TestDeriveSKUs_ToolKind(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mod-tv", "drv_tv")
	rt.Version = "3.0.0"
	rt.Transport = model.SkillRuntimeTransportExecute

	svc.DeriveSKUs(rt, mcpTestTools("run"))

	item, err := repo.GetByID("mod-tv-run")
	require.NoError(t, err)
	mf, err := item.Manifest()
	require.NoError(t, err)
	require.Equal(t, "tool", mf.Metadata["package_derived"], "execute 运行时 → kind=tool")
	require.Equal(t, "3.0.0", item.Version)
}

// TestDeriveSKUs_SyncsVersionOnUpgrade 版本并入缓存键：tools 不变但 package 升级（版本变化）时
// 重派生，已存在 SKU 的 Version 同步刷新（T2.3「version 驱动更新检测」数据地基）。
func TestDeriveSKUs_SyncsVersionOnUpgrade(t *testing.T) {
	repo := newSKUServiceTestDB(t)
	svc := NewSkillRuntimeSKUService(repo, nil)
	rt := autoSKUTestRuntime("mod-u", "drv_u")
	tools := mcpTestTools("echo", "ping")

	rt.Version = "1.0.0"
	svc.DeriveSKUs(rt, tools)
	ping, err := repo.GetByID("mod-u-ping")
	require.NoError(t, err)
	require.Equal(t, "1.0.0", ping.Version)

	// package 升级：tools 不变，仅版本变化 -> 触发重派生并刷新版本
	rt.Version = "2.0.0"
	svc.DeriveSKUs(rt, tools)
	ping, err = repo.GetByID("mod-u-ping")
	require.NoError(t, err)
	require.Equal(t, "2.0.0", ping.Version, "版本升级应刷新已存在 SKU 的 Version")
	require.Equal(t, model.AgentStatusApproved, ping.Status, "重派生不应影响 status")
	mf, err := ping.Manifest()
	require.NoError(t, err)
	require.Equal(t, "2.0.0", mf.Version)

	// 同版本再扫 -> 幂等跳过（缓存键未变）
	svc.DeriveSKUs(rt, tools)
	ping, err = repo.GetByID("mod-u-ping")
	require.NoError(t, err)
	require.Equal(t, "2.0.0", ping.Version)
	items, err := repo.ListByModuleSKUs("mod-u")
	require.NoError(t, err)
	require.Len(t, items, 2, "幂等：不产生重复 SKU")
}
