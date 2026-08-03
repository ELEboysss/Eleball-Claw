package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
	"go.uber.org/zap"
)

// SkillRuntimeManager 管理运行时生命周期（启动/停止/监控）
// 按 deployment 类型分发到不同的启动器。
type SkillRuntimeManager struct {
	registry  *SkillRuntimeRegistry
	logger    *zap.Logger
	mu        sync.Mutex
	processes map[string]*exec.Cmd      // runtime_id -> process
	stopping  map[string]bool            // runtime_id -> 是否正在停止
	sandboxCfg *ProcessSandboxConfig     // process 沙箱配置
}

// ProcessSandboxConfig 本地子进程沙箱配置
type ProcessSandboxConfig struct {
	AllowedWorkDirs []string          // 允许的工作目录前缀
	AllowedEnvKeys  []string          // 允许的环境变量白名单
	MaxProcesses    int               // 最大并发进程数
	Timeout         time.Duration     // 启动超时
}

// NewSkillRuntimeManager 创建运行时管理器
func NewSkillRuntimeManager(registry *SkillRuntimeRegistry, logger *zap.Logger) *SkillRuntimeManager {
	return &SkillRuntimeManager{
		registry:  registry,
		logger:    logger,
		processes: make(map[string]*exec.Cmd),
		stopping:  make(map[string]bool),
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
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("创建 stdin 管道失败: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("创建 stdout 管道失败: %w", err)
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("创建 stderr 管道失败: %w", err)
		}

		// 启动进程
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("启动进程失败: %w", err)
		}

		m.processes[rt.ID] = cmd

		// 启动 goroutine 读取 stderr
		go m.readProcessStderr(rt.ID, stderr)

		// 启动 supervisor
		go m.superviseProcess(rt.ID, cmd, stdin, stdout)

		// 更新状态为 starting
		rt.Status = model.SkillRuntimeStatusStarting
		m.registry.Register(rt)
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
	m.mu.Lock()
	cmd, exists := m.processes[rt.ID]
	m.mu.Unlock()

	if !exists {
		return nil
	}

	if m.logger != nil {
		m.logger.Info("停止本地进程运行时", zap.String("runtime_id", rt.ID))
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

	// 检查环境变量
	if len(m.sandboxCfg.AllowedEnvKeys) > 0 {
		env := rt.EnvMap()
		for k := range env {
			allowed := false
			for _, allowedKey := range m.sandboxCfg.AllowedEnvKeys {
				if k == allowedKey {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("环境变量 %s 不在白名单", k)
			}
		}
	}

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

// superviseProcess MCP stdio 进程 supervisor
func (m *SkillRuntimeManager) superviseProcess(runtimeID string, cmd *exec.Cmd, stdin, stdout interface{}) {
	// 简化实现：等待进程退出，然后标记 offline
	// 完整实现应包含 JSON-RPC 读写循环和自动重连
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		delete(m.processes, runtimeID)
		m.mu.Unlock()

		rt := m.registry.Get(runtimeID)
		if rt != nil {
			rt.Status = model.SkillRuntimeStatusOffline
			m.registry.Register(rt)
		}
	}()
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
