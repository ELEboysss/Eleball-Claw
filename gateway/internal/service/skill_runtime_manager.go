package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
	"go.uber.org/zap"
)

// SkillRuntimeManager 管理运行时生命周期（启动/停止/监控）
// 按 deployment 类型分发到不同的启动器。
type SkillRuntimeManager struct {
	registry     *SkillRuntimeRegistry
	logger       *zap.Logger
	mu           sync.Mutex
	processes    map[string]*exec.Cmd     // runtime_id -> process
	stopping     map[string]bool          // runtime_id -> 是否正在停止
	sandboxCfg   *ProcessSandboxConfig    // process 沙箱配置
	mcpStdio     *MCPStdioProtocol        // stdio MCP 会话池（与 Registry 共享同一实例）
	skuService   *SkillRuntimeSKUService  // auto_sku 运行时探活成功后自动派生 SKU
	credService  *AgentCredentialService  // stdio spawn 时注入 module 级凭证到 env
	spawnUserIDs sync.Map                 // runtime_id -> userID（stdio spawn 注入凭证用的用户；空=autostart）
	stopChans    map[string]chan struct{} // runtime_id -> supervisor 退出信号
}

// ProcessSandboxConfig 本地子进程沙箱配置
type ProcessSandboxConfig struct {
	AllowedWorkDirs []string      // 允许的工作目录前缀
	AllowedEnvKeys  []string      // 允许的环境变量白名单
	MaxProcesses    int           // 最大并发进程数
	Timeout         time.Duration // 启动超时
}

// NewSkillRuntimeManager 创建运行时管理器
func NewSkillRuntimeManager(registry *SkillRuntimeRegistry, logger *zap.Logger) *SkillRuntimeManager {
	return &SkillRuntimeManager{
		registry:  registry,
		logger:    logger,
		processes: make(map[string]*exec.Cmd),
		stopping:  make(map[string]bool),
		stopChans: make(map[string]chan struct{}),
		sandboxCfg: &ProcessSandboxConfig{
			MaxProcesses: 10,
			Timeout:      30 * time.Second,
		},
	}
}

// SetSandboxConfig 设置 process 沙箱配置
func (m *SkillRuntimeManager) SetSandboxConfig(cfg *ProcessSandboxConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandboxCfg = cfg
}

// SetMCPStdioProtocol 注入共享的 stdio MCP 协议实例（与 SkillRuntimeRegistry 共享同一实例）。
// 未注入时 mcp_stdio 运行时无法启动会话与调用。
func (m *SkillRuntimeManager) SetMCPStdioProtocol(p *MCPStdioProtocol) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mcpStdio = p
}

// SetSKUService 注入自动 SKU 派生服务。stdio supervisor 探活成功并拿到 tools/list 后，
// 为 auto_sku 运行时调用 DeriveSKUs 合成/同步可购买 SKU（与 Registry 的 mcp_http 探活共用同一实例）。
func (m *SkillRuntimeManager) SetSKUService(svc *SkillRuntimeSKUService) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.skuService = svc
}

// SetCredentialService 注入凭证服务。stdio spawn 时据此把 module 级凭证替换进 env 模板（${credentials.KEY}）。
func (m *SkillRuntimeManager) SetCredentialService(svc *AgentCredentialService) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credService = svc
}

// Start 启动运行时（按 deployment 分发）
func (m *SkillRuntimeManager) Start(runtimeID string) error {
	rt := m.registry.Get(runtimeID)
	if rt == nil {
		return errors.New("runtime 未注册")
	}

	switch rt.Deployment {
	case model.SkillRuntimeDeploymentNone:
		// 已在线，只探测
		m.registry.ForceProbe(runtimeID)
		return nil

	case model.SkillRuntimeDeploymentDocker:
		return m.startDocker(rt)

	case model.SkillRuntimeDeploymentProcess:
		return m.startProcess(rt)

	case model.SkillRuntimeDeploymentExternal:
		// 外部服务，只注册 endpoint 并探测
		m.registry.ForceProbe(runtimeID)
		return nil

	default:
		return fmt.Errorf("不支持的 deployment: %s", rt.Deployment)
	}
}

// Stop 停止指定运行时。
func (m *SkillRuntimeManager) Stop(runtimeID string) error {
	rt := m.registry.Get(runtimeID)
	if rt == nil {
		return errors.New("runtime 未注册")
	}

	m.mu.Lock()
	m.stopping[runtimeID] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.stopping, runtimeID)
		m.mu.Unlock()
	}()

	switch rt.Deployment {
	case model.SkillRuntimeDeploymentProcess:
		return m.stopProcess(rt)
	case model.SkillRuntimeDeploymentDocker:
		return m.stopDocker(rt)
	default:
		return nil
	}
}

// StopAll 停止所有由本管理器启动的运行时
func (m *SkillRuntimeManager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.processes))
	for id := range m.processes {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		_ = m.Stop(id)
	}
}

// RespawnByDriver 按 driverID 重启 stdio process 运行时并注入指定用户的模块级凭证。
// 仅当运行时 env 含 ${credentials.KEY} 模板（凭证 spawn 时烤进 env）时才需 respawn；
// 无模板的模块凭证经 _meta per-call 注入，换 key 下次调用即生效，跳过 respawn 避免无谓离线窗口。
// 非 process/stdio 运行时或未注册时安全跳过。返回 Start 的错误（Stop 错误忽略）。
func (m *SkillRuntimeManager) RespawnByDriver(driverID, userID string) error {
	if driverID == "" {
		return nil
	}
	rt := m.registry.GetByDriverID(driverID)
	if rt == nil {
		return nil
	}
	if rt.Deployment != model.SkillRuntimeDeploymentProcess || rt.Transport != model.SkillRuntimeTransportMCPStdio {
		return nil // 仅 stdio process 需重 spawn 注入 env
	}
	if !envHasCredentialTemplates(rt) {
		// 凭证经 _meta per-call 注入，换 key 无需 respawn（避免停启造成的离线窗口）
		if m.logger != nil {
			m.logger.Debug("模块凭证变更但 env 无凭证模板，跳过 respawn（per-call 注入已覆盖）",
				zap.String("driver", driverID))
		}
		return nil
	}
	m.spawnUserIDs.Store(rt.ID, userID)
	_ = m.Stop(rt.ID) // 停止旧进程（含 supervisor），忽略未运行等错误
	return m.Start(rt.ID)
}

// envHasCredentialTemplates 检查运行时 env 是否含 ${credentials.KEY} 模板。
// 有则凭证在 spawn 时烤进 env（换 key 需 respawn）；无则凭证经 _meta per-call 注入，无需 respawn。
func envHasCredentialTemplates(rt *model.SkillRuntime) bool {
	for _, v := range rt.EnvMap() {
		if strings.Contains(v, "${credentials.") {
			return true
		}
	}
	return false
}

// buildStdioEnv 构建 stdio 子进程环境变量：os.Environ() + 模块 env（${credentials.KEY} 模板替换）。
// 模块 env 为空时返回 nil（子进程继承父进程全部环境）。
func (m *SkillRuntimeManager) buildStdioEnv(rt *model.SkillRuntime) []string {
	moduleEnv := rt.EnvMap()
	if len(moduleEnv) == 0 {
		return nil
	}
	creds := m.loadStdioCredentials(rt)
	env := os.Environ()
	for k, v := range moduleEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, substituteCredentialPlaceholders(v, creds)))
	}
	return env
}

// loadStdioCredentials 读取 stdio 模块的 module 级凭证，用于 env 模板替换。
// 优先用 spawnUserIDs 记录的用户（RespawnByDriver 设置）；为空时取模块桶任一用户（claw autostart）。
func (m *SkillRuntimeManager) loadStdioCredentials(rt *model.SkillRuntime) map[string]string {
	if m.credService == nil || rt.DriverID == "" {
		return nil
	}
	v, _ := m.spawnUserIDs.Load(rt.ID)
	userID, _ := v.(string)
	if userID != "" {
		if creds, err := m.credService.LoadModuleBucket(userID, rt.DriverID); err == nil {
			return creds
		}
	}
	if creds, err := m.credService.LoadModuleBucketAnyUser(rt.DriverID); err == nil {
		return creds
	}
	return nil
}

// resolveStdioCommand 解析 stdio 启动命令的二进制路径。
// H2：python 模块若已建 .venv（用户经 install-deps 装过依赖），优先用 venv python 以加载
// 第三方包；否则回退 locateCommand（系统 -> 托管磁盘探测 -> InterpreterMissingError）。
func (m *SkillRuntimeManager) resolveStdioCommand(rt *model.SkillRuntime) (string, error) {
	if isPythonCommand(rt.Command) {
		if venvBin := managedVenvPython(rt.WorkDir); venvBin != "" {
			return venvBin, nil
		}
	}
	return locateCommand(rt.Command)
}

// ensureNodePath 若 resolved 是托管 node/npx 二进制，把托管 node bin 目录前插到 PATH，
// 供 npx 脚本 shebang（#!/usr/bin/env node）解析 node。env 为 nil 时基于 os.Environ() 构建；
// resolved 非托管 node 时原样返回（系统 node 已在 PATH）。
func (m *SkillRuntimeManager) ensureNodePath(resolved string, env []string) []string {
	binDir := managedNodeBinDir()
	if binDir == "" || !strings.HasPrefix(filepath.Clean(resolved), binDir) {
		return env
	}
	if env == nil {
		env = os.Environ()
	}
	return prependPath(env, binDir)
}

// startDocker 启动 Docker 运行时
func (m *SkillRuntimeManager) startDocker(rt *model.SkillRuntime) error {
	if m.logger != nil {
		m.logger.Info("启动 Docker 运行时", zap.String("runtime_id", rt.ID))
	}

	// 构建 docker-compose 文件路径
	composePath := rt.DockerComposePath
	if composePath == "" {
		composePath = filepath.Join("marketplace", rt.ID, "docker-compose.yml")
	}

	if _, err := os.Stat(composePath); err != nil {
		return fmt.Errorf("docker-compose.yml 不存在: %s", composePath)
	}

	// 使用 docker-compose 启动
	projectName := fmt.Sprintf("eleball-%s", rt.ID)
	cmd := exec.Command("docker", "compose", "-f", composePath, "-p", projectName, "up", "-d")
	cmd.Dir = filepath.Dir(composePath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmdWithCtx := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmdWithCtx.Dir = cmd.Dir

	output, err := cmdWithCtx.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up 失败: %w, output: %s", err, string(output))
	}

	// 启动后探测
	m.registry.ForceProbe(rt.ID)
	return nil
}

// stopDocker 停止 Docker 运行时
func (m *SkillRuntimeManager) stopDocker(rt *model.SkillRuntime) error {
	if m.logger != nil {
		m.logger.Info("停止 Docker 运行时", zap.String("runtime_id", rt.ID))
	}

	composePath := rt.DockerComposePath
	if composePath == "" {
		composePath = filepath.Join("marketplace", rt.ID, "docker-compose.yml")
	}

	projectName := fmt.Sprintf("eleball-%s", rt.ID)
	cmd := exec.Command("docker", "compose", "-f", composePath, "-p", projectName, "down")
	cmd.Dir = filepath.Dir(composePath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmdWithCtx := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmdWithCtx.Dir = cmd.Dir

	output, err := cmdWithCtx.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down 失败: %w, output: %s", err, string(output))
	}
	return nil
}

// startProcess 启动本地子进程运行时
func (m *SkillRuntimeManager) startProcess(rt *model.SkillRuntime) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已在运行
	if cmd, exists := m.processes[rt.ID]; exists {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			return nil // 已在运行
		}
		delete(m.processes, rt.ID)
	}

	// 沙箱检查
	if err := m.validateProcessSandbox(rt); err != nil {
		return fmt.Errorf("沙箱校验失败: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("启动本地进程运行时",
			zap.String("runtime_id", rt.ID),
			zap.String("command", rt.Command),
			zap.Strings("args", rt.ArgsList()),
		)
	}

	// 构建命令
	args := rt.ArgsList()
	cmd := exec.Command(rt.Command, args...)

	// 设置工作目录
	if rt.WorkDir != "" {
		cmd.Dir = rt.WorkDir
	}

	// 设置环境变量
	if env := rt.EnvMap(); len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// 设置 stdio（对于 mcp_stdio transport）
	if rt.Transport == model.SkillRuntimeTransportMCPStdio {
		if m.mcpStdio == nil {
			return errors.New("stdio MCP 协议未注入，无法启动 mcp_stdio 运行时")
		}
		// 创建 supervisor 退出信号（startProcess 已持有 m.mu）
		m.stopChans[rt.ID] = make(chan struct{})

		proc, spawnErr := m.spawnStdioProcess(rt)
		if spawnErr != nil {
			delete(m.stopChans, rt.ID)
			return spawnErr
		}
		m.processes[rt.ID] = proc

		// 更新状态为 starting
		rt.Status = model.SkillRuntimeStatusStarting
		m.registry.Register(rt)

		// 启动 supervisor（周期探活 + 掉线重连）
		go m.superviseStdio(rt)
	} else {
		// 普通 process（如 search-web 脚本），后台启动
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("启动进程失败: %w", err)
		}
		m.processes[rt.ID] = cmd

		// 后台监控
		go m.monitorProcess(rt.ID, cmd)
	}

	return nil
}

// stopProcess 停止本地子进程
func (m *SkillRuntimeManager) stopProcess(rt *model.SkillRuntime) error {
	// 通知 supervisor 退出（不再重连）
	m.mu.Lock()
	if stopCh, ok := m.stopChans[rt.ID]; ok {
		select {
		case <-stopCh:
		default:
			close(stopCh)
		}
	}
	cmd, exists := m.processes[rt.ID]
	m.mu.Unlock()

	if !exists {
		// 仍可能存在 stdio 会话（supervisor 重连中途），一并清理
		if m.mcpStdio != nil {
			m.mcpStdio.UnregisterSession(rt.ID)
		}
		return nil
	}

	if m.logger != nil {
		m.logger.Info("停止本地进程运行时", zap.String("runtime_id", rt.ID))
	}

	// 先注销 stdio 会话，避免 supervisor 探活误判重连
	if m.mcpStdio != nil {
		m.mcpStdio.UnregisterSession(rt.ID)
	}

	// 发送终止信号
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	// 等待进程退出
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// 强制杀死
		_ = cmd.Process.Kill()
	}

	m.mu.Lock()
	delete(m.processes, rt.ID)
	delete(m.stopChans, rt.ID)
	m.mu.Unlock()

	// 更新状态为 offline
	rt.Status = model.SkillRuntimeStatusOffline
	m.registry.Register(rt)
	return nil
}

// validateProcessSandbox 校验 process 沙箱
func (m *SkillRuntimeManager) validateProcessSandbox(rt *model.SkillRuntime) error {
	if m.sandboxCfg == nil {
		return nil
	}

	// 检查工作目录
	if rt.WorkDir != "" && len(m.sandboxCfg.AllowedWorkDirs) > 0 {
		allowed := false
		for _, prefix := range m.sandboxCfg.AllowedWorkDirs {
			if absPrefix, err := filepath.Abs(prefix); err == nil {
				if absWorkDir, err := filepath.Abs(rt.WorkDir); err == nil {
					if rel, err := filepath.Rel(absPrefix, absWorkDir); err == nil && rel != ".." && rel != "." {
						allowed = true
						break
					}
				}
			}
		}
		if !allowed {
			return fmt.Errorf("工作目录 %s 不在允许范围内", rt.WorkDir)
		}
	}

	// 模块声明 env（rt.EnvMap，含 ${credentials.KEY} 凭证）不经白名单：这些是模块显式声明、
	// 子进程运行所需（如 FIRECRAWL_API_KEY），白名单只列系统通用变量（PATH/HOME/…）会误拦
	// 所有带凭证的 process 模块。网关自身云凭证的防泄漏应经 spawn 时过滤 os.Environ（拒绝已知
	// 密钥名）实现，而非拦模块 env——后者既不达目的（os.Environ 仍全量透传）又阻断合法模块启动。

	// 检查最大进程数
	if len(m.processes) >= m.sandboxCfg.MaxProcesses {
		return errors.New("已达到最大进程数限制")
	}

	return nil
}

// readProcessStderr 读取进程 stderr（用于调试）
func (m *SkillRuntimeManager) readProcessStderr(runtimeID string, stderr interface{ Read([]byte) (int, error) }) {
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		if err != nil {
			return
		}
		if m.logger != nil && n > 0 {
			m.logger.Debug("进程 stderr",
				zap.String("runtime_id", runtimeID),
				zap.String("output", string(buf[:n])),
			)
		}
	}
}

// stdioProbeInterval stdio MCP supervisor 的探活周期（测试可短期覆盖）
var stdioProbeInterval = 60 * time.Second

// superviseStdio MCP stdio 进程 supervisor：周期 tools/list 探活 + 掉线自动重连。
// 不调用 cmd.Wait（由 stopProcess 负责 reap）；进程退出经下一次探活失败感知，
// 随后按指数退避（1s/2s/4s）重 spawn，最多 3 次，超限标记 error。
func (m *SkillRuntimeManager) superviseStdio(rt *model.SkillRuntime) {
	runtimeID := rt.ID

	// 子进程启动后短暂等待就绪，立即探活一次
	time.Sleep(2 * time.Second)
	if m.isStopping(runtimeID) {
		return
	}
	_ = m.probeStdioRuntime(rt)

	ticker := time.NewTicker(stdioProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopChanFor(runtimeID):
			return
		case <-ticker.C:
			if m.isStopping(runtimeID) {
				return
			}
			if err := m.probeStdioRuntime(rt); err == nil {
				continue
			}
			// 探活失败 -> 尝试重连；重连失败（超限/停止）则结束 supervisor
			if !m.reconnectStdio(rt) {
				return
			}
		}
	}
}

// spawnStdioProcess 创建并启动 stdio MCP 子进程、注册会话、起 stderr reader。
// 不触碰 m.mu/m.processes（由调用方在合适锁状态写入 processes）。沙箱校验由调用方完成。
// 返回已启动的 cmd，供调用方存入 processes map。
func (m *SkillRuntimeManager) spawnStdioProcess(rt *model.SkillRuntime) (*exec.Cmd, error) {
	// D3：spawn 前预解析命令，缺失解释器时返回带安装指引的可读错误，
	// 避免 Windows 上 python/npx 缺失只抛 "executable file not found"。
	// H2：python 模块若已建 .venv（用户装过依赖），改用 venv python 以加载第三方依赖。
	resolved, err := m.resolveStdioCommand(rt)
	if err != nil {
		return nil, err
	}
	args := rt.ArgsList()
	cmd := exec.Command(resolved, args...)
	if rt.WorkDir != "" {
		cmd.Dir = rt.WorkDir
	}
	// buildStdioEnv 注入 module 级凭证 env；ensureNodePath 把托管 node bin 放上 PATH
	// （供 npx 脚本 shebang 解析 node；仅 resolved 为托管 node 二进制时生效）。
	cmd.Env = m.ensureNodePath(resolved, m.buildStdioEnv(rt))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 stdin 管道失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 stdout 管道失败: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 stderr 管道失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动进程失败: %w", err)
	}

	if m.mcpStdio != nil {
		m.mcpStdio.RegisterSession(rt.ID, stdin, stdout)
	}
	go m.readProcessStderr(rt.ID, stderr)

	if m.logger != nil {
		pid := 0
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		m.logger.Info("stdio MCP 子进程已启动",
			zap.String("runtime_id", rt.ID),
			zap.Int("pid", pid),
		)
	}
	return cmd, nil
}

// reconnectStdio 探活失败后重连：清旧会话 -> 指数退避重 spawn -> 探活确认。
// 返回 true 表示重连成功（supervisor 继续）；false 表示超限标记 error 或正在停止。
func (m *SkillRuntimeManager) reconnectStdio(rt *model.SkillRuntime) bool {
	runtimeID := rt.ID
	if m.mcpStdio != nil {
		m.mcpStdio.UnregisterSession(runtimeID)
	}
	m.removeProcess(runtimeID)

	for attempt := 1; attempt <= 3; attempt++ {
		if m.isStopping(runtimeID) {
			return false
		}
		// 指数退避：1s -> 2s -> 4s
		backoff := time.Duration(1<<(attempt-1)) * time.Second
		time.Sleep(backoff)

		if m.logger != nil {
			m.logger.Warn("stdio MCP 尝试重连",
				zap.String("runtime_id", runtimeID),
				zap.Int("attempt", attempt),
			)
		}
		proc, spawnErr := m.spawnStdioProcess(rt)
		if spawnErr != nil {
			if m.logger != nil {
				m.logger.Warn("stdio MCP 重连 spawn 失败",
					zap.String("runtime_id", runtimeID),
					zap.Int("attempt", attempt),
					zap.Error(spawnErr),
				)
			}
			continue
		}
		m.mu.Lock()
		m.processes[runtimeID] = proc
		m.mu.Unlock()
		// 等待子进程就绪后探活确认
		time.Sleep(2 * time.Second)
		if err := m.probeStdioRuntime(rt); err == nil {
			if m.logger != nil {
				m.logger.Info("stdio MCP 重连成功", zap.String("runtime_id", runtimeID), zap.Int("attempt", attempt))
			}
			return true
		}
		// 探活仍失败，清理后继续下一轮
		if m.mcpStdio != nil {
			m.mcpStdio.UnregisterSession(runtimeID)
		}
		m.removeProcess(runtimeID)
	}

	// 超过重连上限 -> 标记 error
	if m.logger != nil {
		m.logger.Warn("stdio MCP 重连超限，标记 error", zap.String("runtime_id", runtimeID))
	}
	m.registry.SetRuntimeStatus(runtimeID, model.SkillRuntimeStatusError, nil, "stdio MCP 重连超限")
	return false
}

// probeStdioRuntime 经共享 MCPStdioProtocol 探活并更新状态。成功返回 nil。
func (m *SkillRuntimeManager) probeStdioRuntime(rt *model.SkillRuntime) error {
	if m.mcpStdio == nil || !m.mcpStdio.IsRegistered(rt.ID) {
		m.registry.SetRuntimeStatus(rt.ID, model.SkillRuntimeStatusOffline, nil, "stdio 会话未注册")
		return errors.New("stdio 会话未注册")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := m.mcpStdio.ListTools(ctx, rt.ID)
	if err != nil {
		m.registry.SetRuntimeStatus(rt.ID, model.SkillRuntimeStatusOffline, nil, err.Error())
		return err
	}
	tools = FilterTools(rt, tools) // G2：按 allowed/disallowed 过滤，caps 与 DeriveSKUs 均只见允许的工具
	caps := make([]string, 0, len(tools))
	for _, t := range tools {
		caps = append(caps, t.Name)
	}
	m.registry.SetRuntimeStatus(rt.ID, model.SkillRuntimeStatusOnline, caps, "")
	// auto_sku 运行时：探活成功且拿到工具列表 -> 自动派生/同步 SKU
	if m.skuService != nil {
		m.skuService.DeriveSKUs(rt, tools)
	}
	return nil
}

// stopChanFor 返回 runtime 的 supervisor 退出信号；不存在时返回 nil
func (m *SkillRuntimeManager) stopChanFor(runtimeID string) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopChans[runtimeID]
}

// isStopping 是否正在停止
func (m *SkillRuntimeManager) isStopping(runtimeID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopping[runtimeID]
}

// removeProcess 从 processes map 移除（不 kill）
func (m *SkillRuntimeManager) removeProcess(runtimeID string) {
	m.mu.Lock()
	delete(m.processes, runtimeID)
	m.mu.Unlock()
}

// monitorProcess 监控普通后台进程
func (m *SkillRuntimeManager) monitorProcess(runtimeID string, cmd *exec.Cmd) {
	_ = cmd.Wait()
	m.mu.Lock()
	delete(m.processes, runtimeID)
	m.mu.Unlock()

	rt := m.registry.Get(runtimeID)
	if rt != nil {
		rt.Status = model.SkillRuntimeStatusOffline
		m.registry.Register(rt)
	}
}

// IsRunning 检查运行时是否正在运行（仅 process deployment）
func (m *SkillRuntimeManager) IsRunning(runtimeID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd, exists := m.processes[runtimeID]
	if !exists {
		return false
	}
	return cmd.ProcessState == nil || !cmd.ProcessState.Exited()
}
