package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// pythonAvailable 检测 PATH 中是否有 python（示例 stdio MCP server 依赖）
func pythonAvailable() bool {
	_, err := exec.LookPath("python")
	return err == nil
}

// sampleStdioDir 示例 stdio MCP 模块目录（测试从 gateway/internal/service/ 运行）
func sampleStdioDir() string {
	return filepath.Join("..", "..", "marketplace", "mcp-stdio-echo")
}

// firecrawlStdioDir firecrawl stdio MCP 模块目录（首模块迁移 E1，测试从 gateway/internal/service/ 运行）
func firecrawlStdioDir() string {
	return filepath.Join("..", "..", "marketplace", "firecrawl")
}

// waitOnline 轮询 ForceProbe 直至运行时在线或超时
func waitOnline(t *testing.T, reg *SkillRuntimeRegistry, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st := reg.ForceProbe(id); st != nil && st.Online {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("运行时 %s 未在 %v 内转在线", id, timeout)
}

// waitStatus 轮询直至运行时状态匹配或超时
func waitStatus(t *testing.T, reg *SkillRuntimeRegistry, id string, want model.SkillRuntimeStatus, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if rt := reg.Get(id); rt != nil && rt.Status == want {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	rt := reg.Get(id)
	got := "<nil>"
	if rt != nil {
		got = string(rt.Status)
	}
	t.Fatalf("运行时 %s 状态未在 %v 内变为 %s（当前 %s）", id, timeout, want, got)
}

// TestSkillRuntimeManager_StdioStartProbeExecute E2E：spawn python 示例 -> 探活在线 -> echo 调用。
func TestSkillRuntimeManager_StdioStartProbeExecute(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python 不在 PATH，跳过 stdio E2E 测试")
	}
	dir := sampleStdioDir()
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Skipf("示例模块不存在: %v", err)
	}

	reg := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	mcpStdio := NewMCPStdioProtocol(nil)
	reg.SetMCPStdioProtocol(mcpStdio)
	mgr := NewSkillRuntimeManager(reg, nil)
	mgr.SetMCPStdioProtocol(mcpStdio)

	rt := &model.SkillRuntime{
		ID:         "mcp-stdio-echo-test",
		Name:       "echo",
		Transport:  model.SkillRuntimeTransportMCPStdio,
		Deployment: model.SkillRuntimeDeploymentProcess,
		Command:    "python",
		WorkDir:    dir,
	}
	rt.SetArgs([]string{"main.py"})
	if err := reg.Register(rt); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Start(rt.ID); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer mgr.Stop(rt.ID)

	waitOnline(t, reg, rt.ID, 10*time.Second)

	// 调用 echo 工具
	res, err := reg.Execute(rt.ID, "echo", map[string]interface{}{"message": "hi"}, "u1")
	if err != nil {
		t.Fatalf("Execute echo 失败: %v", err)
	}
	content, _ := res["content"].([]interface{})
	if len(content) == 0 {
		t.Fatalf("响应无 content: %+v", res)
	}
	first, _ := content[0].(map[string]interface{})
	if first["text"] != "hi" {
		t.Fatalf("echo 内容不符: %+v", first)
	}
}

// TestSkillRuntimeManager_StdioReconnect E2E：杀进程后 supervisor 自动重连转在线。
func TestSkillRuntimeManager_StdioReconnect(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python 不在 PATH，跳过 stdio E2E 测试")
	}
	dir := sampleStdioDir()
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Skipf("示例模块不存在: %v", err)
	}

	// 缩短探活周期加速重连
	prev := stdioProbeInterval
	stdioProbeInterval = 1 * time.Second
	defer func() { stdioProbeInterval = prev }()

	reg := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	mcpStdio := NewMCPStdioProtocol(nil)
	reg.SetMCPStdioProtocol(mcpStdio)
	mgr := NewSkillRuntimeManager(reg, nil)
	mgr.SetMCPStdioProtocol(mcpStdio)

	rt := &model.SkillRuntime{
		ID:         "mcp-stdio-echo-reconnect",
		Name:       "echo",
		Transport:  model.SkillRuntimeTransportMCPStdio,
		Deployment: model.SkillRuntimeDeploymentProcess,
		Command:    "python",
		WorkDir:    dir,
	}
	rt.SetArgs([]string{"main.py"})
	if err := reg.Register(rt); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Start(rt.ID); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer mgr.Stop(rt.ID)

	waitOnline(t, reg, rt.ID, 10*time.Second)

	// 杀掉子进程，supervisor 下次探活失败后应自动重连
	mgr.mu.Lock()
	cmd := mgr.processes[rt.ID]
	mgr.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		t.Fatal("未找到运行中的子进程")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill 失败: %v", err)
	}

	// 重连需经历：探活失败 -> 1s/2s/4s 退避 -> 重 spawn -> 探活在线
	waitOnline(t, reg, rt.ID, 30*time.Second)
}

// TestSkillRuntimeManager_StdioAutoSKU E2E：auto_sku stdio 模块 supervisor 探活成功后，
// 自动派生 echo/ping 两个可购买 SKU（验证 probeStdioRuntime -> DeriveSKUs 全链路）。
func TestSkillRuntimeManager_StdioAutoSKU(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python 不在 PATH，跳过 stdio E2E 测试")
	}
	dir := sampleStdioDir()
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Skipf("示例模块不存在: %v", err)
	}

	// 内存 DB + SKU 服务：探活成功后 DeriveSKUs 会把 echo/ping 写成 AgentItem
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AgentItem{}))
	agentRepo := repository.NewAgentRepo(db)

	reg := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	mcpStdio := NewMCPStdioProtocol(nil)
	reg.SetMCPStdioProtocol(mcpStdio)
	mgr := NewSkillRuntimeManager(reg, nil)
	mgr.SetMCPStdioProtocol(mcpStdio)
	mgr.SetSKUService(NewSkillRuntimeSKUService(agentRepo, nil))

	rt := &model.SkillRuntime{
		ID:         "mcp-stdio-echo-autosku",
		Name:       "echo",
		DriverID:   "mcp_stdio_echo_autosku",
		Transport:  model.SkillRuntimeTransportMCPStdio,
		Deployment: model.SkillRuntimeDeploymentProcess,
		Command:    "python",
		WorkDir:    dir,
		AutoSKU:    true,
	}
	rt.SetArgs([]string{"main.py"})
	require.NoError(t, reg.Register(rt))
	require.NoError(t, mgr.Start(rt.ID))
	defer mgr.Stop(rt.ID)

	waitOnline(t, reg, rt.ID, 10*time.Second)

	// supervisor 在 spawn 后约 2s 探活并调 DeriveSKUs；轮询 DB 直至 echo SKU 出现
	deadline := time.Now().Add(15 * time.Second)
	var echo *model.AgentItem
	for time.Now().Before(deadline) {
		if it, err := agentRepo.GetByID("mcp-stdio-echo-autosku-echo"); err == nil && it != nil {
			echo = it
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotNil(t, echo, "echo SKU 未自动派生")
	require.Equal(t, model.AgentStatusApproved, echo.Status)
	mf, err := echo.Manifest()
	require.NoError(t, err)
	require.Equal(t, model.ToolDriverType("mcp_stdio_echo_autosku"), mf.Driver)
	require.Equal(t, "mcp-stdio-echo-autosku", mf.Metadata["module"])

	ping, err := agentRepo.GetByID("mcp-stdio-echo-autosku-ping")
	require.NoError(t, err, "ping SKU 未自动派生")
	require.Equal(t, model.AgentStatusApproved, ping.Status)
}

// TestSkillRuntimeManager_StdioFirecrawlAutoSKU E2E：firecrawl 首模块迁移验证（E1）。
// auto_sku stdio 模块探活后派生 scrape/crawl/extract 三 SKU，且 manifest 透传 module.json 的
// credentials 声明（firecrawl_api_key, scope=module），供 web 提示用户填写、env 模板 ${credentials.KEY} 引用。
// tools/list 不需 API Key，故无需真实凭证即可验证派生 + 凭证透传链路。
func TestSkillRuntimeManager_StdioFirecrawlAutoSKU(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python 不在 PATH，跳过 stdio E2E 测试")
	}
	dir := firecrawlStdioDir()
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Skipf("firecrawl 模块不存在: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AgentItem{}))
	agentRepo := repository.NewAgentRepo(db)

	reg := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	mcpStdio := NewMCPStdioProtocol(nil)
	reg.SetMCPStdioProtocol(mcpStdio)
	mgr := NewSkillRuntimeManager(reg, nil)
	mgr.SetMCPStdioProtocol(mcpStdio)
	mgr.SetSKUService(NewSkillRuntimeSKUService(agentRepo, nil))

	rt := &model.SkillRuntime{
		ID:         "firecrawl",
		Name:       "Firecrawl 网页抓取",
		DriverID:   "firecrawl",
		Transport:  model.SkillRuntimeTransportMCPStdio,
		Deployment: model.SkillRuntimeDeploymentProcess,
		Command:    "python",
		WorkDir:    dir,
		AutoSKU:    true,
	}
	rt.SetArgs([]string{"main.py"})
	rt.SetCredentials(map[string]model.CredentialDef{
		"firecrawl_api_key": {
			Type:     model.CredentialTypeAPIKey,
			Label:    "Firecrawl API Key",
			Required: true,
			Scope:    model.CredentialScopeModule,
		},
	})
	require.NoError(t, reg.Register(rt))
	require.NoError(t, mgr.Start(rt.ID))
	defer mgr.Stop(rt.ID)

	waitOnline(t, reg, rt.ID, 10*time.Second)

	// 轮询 DB 直至三 SKU 派生
	tools := []string{"scrape", "crawl", "extract"}
	deadline := time.Now().Add(15 * time.Second)
	items := map[string]*model.AgentItem{}
	for time.Now().Before(deadline) {
		items = map[string]*model.AgentItem{}
		ok := true
		for _, name := range tools {
			it, err := agentRepo.GetByID("firecrawl-" + name)
			if err != nil || it == nil {
				ok = false
				break
			}
			items[name] = it
		}
		if ok {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, name := range tools {
		require.NotNil(t, items[name], name+" SKU 未自动派生")
		require.Equal(t, model.AgentStatusApproved, items[name].Status, name+" 状态不符")
		mf, err := items[name].Manifest()
		require.NoError(t, err, name+" manifest 解析失败")
		require.Equal(t, model.ToolDriverType("firecrawl"), mf.Driver, name+" driver 不符")
		require.Equal(t, "firecrawl", mf.Metadata["module"], name+" module 元数据不符")
		// 凭证声明透传：web 据此提示用户填 API Key，env 模板 ${credentials.firecrawl_api_key} 引用同名 key
		require.Contains(t, mf.Credentials, "firecrawl_api_key", name+" 缺凭证声明")
		require.True(t, mf.Credentials["firecrawl_api_key"].Required, name+" 凭证应必填")
		require.Equal(t, model.CredentialScopeModule, mf.Credentials["firecrawl_api_key"].Scope, name+" 凭证应 module 级")
	}
}
