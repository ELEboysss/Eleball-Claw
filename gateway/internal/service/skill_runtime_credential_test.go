package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestBuildStdioEnv_CredentialSubstitution stdio 子进程 env 中的 ${credentials.KEY} 模板
// 被替换为 module 桶凭证值；指定用户与 autostart（任一用户）两条路径均生效。
func TestBuildStdioEnv_CredentialSubstitution(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AgentItem{}, &model.AgentUserCredential{}))
	agentRepo := repository.NewAgentRepo(db)
	credSvc := NewAgentCredentialService(repository.NewAgentCredentialRepo(db), agentRepo)

	manifest := model.ToolManifest{
		ID: "mod-x-key", Name: "X", Description: "x", Driver: "drv_x",
		Parameters:  map[string]interface{}{"type": "object"},
		Credentials: map[string]model.CredentialDef{"API_KEY": {Type: model.CredentialTypeAPIKey, Scope: model.CredentialScopeModule, Required: true}},
	}
	mfJSON, _ := json.Marshal(manifest)
	require.NoError(t, agentRepo.Create(&model.AgentItem{ID: "mod-x-key", Name: "X", ManifestJSON: string(mfJSON), Status: model.AgentStatusApproved}))
	require.NoError(t, credSvc.SaveForUserAgent("u1", "mod-x-key", map[string]string{"API_KEY": "secret123"}))

	mgr := NewSkillRuntimeManager(NewSkillRuntimeRegistry(&config.AgentReachConfig{}), nil)
	mgr.SetCredentialService(credSvc)

	rt := &model.SkillRuntime{ID: "mod-x", Name: "x", DriverID: "drv_x",
		Transport: model.SkillRuntimeTransportMCPStdio, Deployment: model.SkillRuntimeDeploymentProcess}
	rt.SetEnv(map[string]string{"MY_API_KEY": "${credentials.API_KEY}", "PLAIN": "keep"})

	// 指定用户 -> 替换为 secret123
	mgr.spawnUserIDs.Store("mod-x", "u1")
	env := mgr.buildStdioEnv(rt)
	require.Contains(t, env, "MY_API_KEY=secret123")
	require.Contains(t, env, "PLAIN=keep")

	// 未指定用户（autostart）-> LoadModuleBucketAnyUser 同样取到（claw 单用户）
	mgr.spawnUserIDs.Delete("mod-x")
	env = mgr.buildStdioEnv(rt)
	require.Contains(t, env, "MY_API_KEY=secret123")

	// 无凭证服务 -> 模板替换为空（不残留 ${credentials...} 字面量）
	mgr2 := NewSkillRuntimeManager(NewSkillRuntimeRegistry(&config.AgentReachConfig{}), nil)
	env = mgr2.buildStdioEnv(rt)
	require.Contains(t, env, "MY_API_KEY=")
	for _, e := range env {
		require.NotContains(t, e, "${credentials.")
	}
}

// TestCredentialChangeHook_FiresOnModuleSave 保存 module 级凭证后异步触发重启钩子；
// SKU 级凭证不触发。
func TestCredentialChangeHook_FiresOnModuleSave(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AgentItem{}, &model.AgentUserCredential{}))
	agentRepo := repository.NewAgentRepo(db)
	credSvc := NewAgentCredentialService(repository.NewAgentCredentialRepo(db), agentRepo)

	mkManifest := func(id, driver string, scope model.CredentialScope) model.ToolManifest {
		return model.ToolManifest{
			ID: id, Name: id, Description: "m", Driver: model.ToolDriverType(driver),
			Parameters:  map[string]interface{}{"type": "object"},
			Credentials: map[string]model.CredentialDef{"K": {Scope: scope}},
		}
	}
	mf1, _ := json.Marshal(mkManifest("m1", "drv_m", model.CredentialScopeModule))
	require.NoError(t, agentRepo.Create(&model.AgentItem{ID: "m1", Name: "m1", ManifestJSON: string(mf1), Status: model.AgentStatusApproved}))

	var gotDriver, gotUser string
	var wg sync.WaitGroup
	wg.Add(1)
	credSvc.SetModuleCredentialChangeHook(func(driverID, userID string) error {
		gotDriver, gotUser = driverID, userID
		wg.Done()
		return nil
	})

	require.NoError(t, credSvc.SaveForUserAgent("u9", "m1", map[string]string{"K": "v"}))
	wg.Wait() // 钩子异步触发
	require.Equal(t, "drv_m", gotDriver)
	require.Equal(t, "u9", gotUser)

	// SKU 级凭证不触发
	mf2, _ := json.Marshal(mkManifest("m2", "drv_s", model.CredentialScopeSKU))
	require.NoError(t, agentRepo.Create(&model.AgentItem{ID: "m2", Name: "m2", ManifestJSON: string(mf2), Status: model.AgentStatusApproved}))
	fired := false
	credSvc.SetModuleCredentialChangeHook(func(string, string) error { fired = true; return nil })
	require.NoError(t, credSvc.SaveForUserAgent("u9", "m2", map[string]string{"K": "v"}))
	time.Sleep(150 * time.Millisecond)
	require.False(t, fired, "SKU 级凭证不应触发模块重启钩子")
}

// TestRespawnByDriver_RestartsStdio E2E：RespawnByDriver 停止旧 stdio 进程并以新凭证重启，运行时再次在线。
func TestRespawnByDriver_RestartsStdio(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python 不在 PATH，跳过 stdio E2E 测试")
	}
	dir := sampleStdioDir()
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Skipf("示例模块不存在: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))
	rtRepo := repository.NewSkillRuntimeRepo(db)

	reg := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	reg.SetRepo(rtRepo) // 启用 GetByDriverID（RespawnByDriver 据此定位运行时）
	mcpStdio := NewMCPStdioProtocol(nil)
	reg.SetMCPStdioProtocol(mcpStdio)
	mgr := NewSkillRuntimeManager(reg, nil)
	mgr.SetMCPStdioProtocol(mcpStdio)
	mgr.SetCredentialService(NewAgentCredentialService(repository.NewAgentCredentialRepo(db), repository.NewAgentRepo(db)))

	rt := &model.SkillRuntime{ID: "echo-respawn", Name: "echo", DriverID: "drv_echo_respawn",
		Transport: model.SkillRuntimeTransportMCPStdio, Deployment: model.SkillRuntimeDeploymentProcess,
		Command: "python", WorkDir: dir}
	rt.SetArgs([]string{"main.py"})
	// env 含 ${credentials.KEY} 模板 -> 凭证 spawn 时烤进 env -> 变更需 respawn 才生效
	rt.SetEnv(map[string]string{"CRED": "${credentials.api_key}"})
	require.NoError(t, reg.Register(rt))
	require.NoError(t, mgr.Start(rt.ID))
	defer mgr.Stop(rt.ID)
	waitOnline(t, reg, rt.ID, 10*time.Second)

	// RespawnByDriver -> Stop + Start（注入凭证）-> 再次在线
	require.NoError(t, mgr.RespawnByDriver("drv_echo_respawn", "u1"))
	waitOnline(t, reg, rt.ID, 10*time.Second)
}

// TestRespawnByDriver_SkipsWithoutEnvTemplate env 无 ${credentials.KEY} 模板的模块凭证经
// _meta per-call 注入，换 key 无需 respawn：RespawnByDriver 应是 no-op（进程指针不变、保持在线）。
func TestRespawnByDriver_SkipsWithoutEnvTemplate(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python 不在 PATH，跳过 stdio E2E 测试")
	}
	dir := sampleStdioDir()
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Skipf("示例模块不存在: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))
	rtRepo := repository.NewSkillRuntimeRepo(db)

	reg := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	reg.SetRepo(rtRepo)
	mcpStdio := NewMCPStdioProtocol(nil)
	reg.SetMCPStdioProtocol(mcpStdio)
	mgr := NewSkillRuntimeManager(reg, nil)
	mgr.SetMCPStdioProtocol(mcpStdio)
	mgr.SetCredentialService(NewAgentCredentialService(repository.NewAgentCredentialRepo(db), repository.NewAgentRepo(db)))

	rt := &model.SkillRuntime{ID: "echo-skip", Name: "echo", DriverID: "drv_echo_skip",
		Transport: model.SkillRuntimeTransportMCPStdio, Deployment: model.SkillRuntimeDeploymentProcess,
		Command: "python", WorkDir: dir}
	rt.SetArgs([]string{"main.py"}) // 无 env 凭证模板 -> per-call 注入 -> 无需 respawn
	require.NoError(t, reg.Register(rt))
	require.NoError(t, mgr.Start(rt.ID))
	defer mgr.Stop(rt.ID)
	waitOnline(t, reg, rt.ID, 10*time.Second)

	mgr.mu.Lock()
	cmdBefore := mgr.processes[rt.ID]
	mgr.mu.Unlock()
	require.NotNil(t, cmdBefore, "进程未启动")

	require.NoError(t, mgr.RespawnByDriver("drv_echo_skip", "u1"))

	mgr.mu.Lock()
	cmdAfter := mgr.processes[rt.ID]
	mgr.mu.Unlock()
	require.NotNil(t, cmdAfter, "respawn 后进程缺失")
	require.Same(t, cmdBefore, cmdAfter, "无 env 凭证模板应跳过 respawn，进程不应重启")
}
