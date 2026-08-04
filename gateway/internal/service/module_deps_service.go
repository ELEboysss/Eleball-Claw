package service

// module_deps_service.go 用户手动装依赖（H2，非自动）。
//
// 为 skill-maker 生成或手写的 stdio+process 模块提供应用内装依赖入口：检测模块目录下的
// requirements.txt（python）/ package.json（node），用户在秘技集市页确认后装：
//   - python：EnsureInterpreter 拿到 python -> python -m venv <module_dir>/.venv ->
//     <venv-python> -m pip install -r requirements.txt。spawn 经 resolveStdioCommand
//     探测到 .venv 后改用 venv python（见 skill_runtime_manager.go），实现依赖隔离。
//   - node：EnsureInterpreter 拿到 node -> npm install（PATH 含 node bin，cmd.Dir=模块目录）
//     -> node_modules。node 自动发现 cwd 下 node_modules，spawn 无需改动。
//
// 安全：不设硬白名单（用户「自行决定」）；装前 DepsStatus 返回包列表供前端展示 + 风险提示，
// 用户显式确认才装。仅 mcp_stdio + process 模块适用（docker 模块依赖在镜像内）。
// claw-only：cloud 不注册 stdio process 模块，DepsStatus 对其恒为 has_deps=false。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
)

// ModuleDep 单个依赖声明（名称 + 版本约束，用于装前展示）。
type ModuleDep struct {
	Name string `json:"name"`
	Spec string `json:"spec,omitempty"` // 版本约束（requirements.txt 原样片段 / package.json 值）
}

// ModuleDepsStatus 依赖状态（GET deps-status 返回）。
type ModuleDepsStatus struct {
	HasDeps   bool         `json:"has_deps"`
	Type      string       `json:"type,omitempty"` // "python" | "node" | ""
	Packages  []ModuleDep  `json:"packages,omitempty"`
	Installed bool         `json:"installed"`
	WorkDir   string       `json:"work_dir,omitempty"`
}

// ModuleDepsResult 装依赖结果（POST install-deps 返回）。
type ModuleDepsResult struct {
	Type      string        `json:"type"`
	Packages  []ModuleDep   `json:"packages"`
	Installed bool          `json:"installed"`
	Log       string        `json:"log,omitempty"` // 安装命令输出（成功/失败诊断）
}

// depsMarkerName 依赖已装标记文件名（写模块目录，幂等防重复装的提示用）。
const depsMarkerName = ".deps_installed"

// DepsStatus 检测模块依赖状态。仅 stdio+process 模块可能 has_deps；其余返回 has_deps=false。
func (s *ModuleService) DepsStatus(moduleID string) (*ModuleDepsStatus, error) {
	rt := s.resolveStdioProcessRuntime(moduleID)
	if rt == nil {
		return &ModuleDepsStatus{HasDeps: false}, nil
	}
	return scanRuntimeDeps(rt), nil
}

// InstallDeps 安装模块依赖（用户显式触发）。装后重启模块使新依赖生效。
func (s *ModuleService) InstallDeps(ctx context.Context, moduleID string) (*ModuleDepsResult, error) {
	rt := s.resolveStdioProcessRuntime(moduleID)
	if rt == nil {
		return nil, fmt.Errorf("模块 %s 不是 stdio+process 模块，无法装依赖", moduleID)
	}
	status := scanRuntimeDeps(rt)
	if !status.HasDeps {
		return nil, fmt.Errorf("模块 %s 无 requirements.txt/package.json，无需装依赖", moduleID)
	}
	if s.bootstrap == nil {
		return nil, fmt.Errorf("解释器引导未初始化，无法装依赖")
	}

	var log string
	var err error
	switch status.Type {
	case "python":
		log, err = s.installPythonDeps(ctx, rt)
	case "node":
		log, err = s.installNodeDeps(ctx, rt)
	default:
		return nil, fmt.Errorf("不支持的依赖类型 %q", status.Type)
	}
	if err != nil {
		return &ModuleDepsResult{Type: status.Type, Packages: status.Packages, Installed: false, Log: log + "\n" + err.Error()}, err
	}

	// 写标记（best-effort）。
	_ = os.WriteFile(filepath.Join(rt.WorkDir, depsMarkerName), []byte(status.Type+"\n"), 0o644)

	// 重启模块：使 spawn 用 venv python / 加载新 node_modules。
	if s.manager != nil && s.manager.IsRunning(moduleID) {
		_ = s.manager.Stop(moduleID)
		_ = s.manager.Start(moduleID)
	}

	installed := scanRuntimeDeps(rt).Installed
	return &ModuleDepsResult{Type: status.Type, Packages: status.Packages, Installed: installed, Log: log}, nil
}

// resolveStdioProcessRuntime 解析模块对应的运行时，要求 transport=mcp_stdio 且 deployment=process。
// 不满足（不存在 / 非 stdio+process）返回 nil。
func (s *ModuleService) resolveStdioProcessRuntime(moduleID string) *model.SkillRuntime {
	if s.registry == nil || moduleID == "" {
		return nil
	}
	rt := s.registry.Get(moduleID)
	if rt == nil {
		return nil
	}
	if rt.Transport != model.SkillRuntimeTransportMCPStdio || rt.Deployment != model.SkillRuntimeDeploymentProcess {
		return nil
	}
	return rt
}

// scanRuntimeDeps 扫描运行时模块目录，检测依赖类型、解析包列表、判断是否已装。
// 独立函数（不用 ModuleService 状态），供 DepsStatus 与 AgentMarketService.checkModuleDeps 复用。
func scanRuntimeDeps(rt *model.SkillRuntime) *ModuleDepsStatus {
	st := &ModuleDepsStatus{WorkDir: rt.WorkDir}
	if rt.WorkDir == "" {
		return st
	}
	switch {
	case isPythonCommand(rt.Command):
		reqPath := filepath.Join(rt.WorkDir, "requirements.txt")
		data, err := os.ReadFile(reqPath)
		if err != nil {
			return st // 无 requirements.txt -> 无依赖
		}
		st.Type = "python"
		st.HasDeps = true
		st.Packages = parseRequirements(string(data))
		st.Installed = managedVenvPython(rt.WorkDir) != ""
	case isNodeCommand(rt.Command):
		pkgPath := filepath.Join(rt.WorkDir, "package.json")
		data, err := os.ReadFile(pkgPath)
		if err != nil {
			return st
		}
		st.Type = "node"
		st.HasDeps = true
		st.Packages = parsePackageDeps(data)
		st.Installed = nodeModulesExists(rt.WorkDir)
	}
	return st
}

// installPythonDeps 建 venv + pip install -r requirements.txt，返回命令输出。
func (s *ModuleService) installPythonDeps(ctx context.Context, rt *model.SkillRuntime) (string, error) {
	resolved, err := s.bootstrap.EnsureInterpreter(ctx, "python")
	if err != nil {
		return "", fmt.Errorf("获取 Python 失败: %w", err)
	}
	venvDir := filepath.Join(rt.WorkDir, ".venv")
	// 建 venv（已存在则重建以保干净；pip install 本身幂等，重建避免残留旧包）。
	if out, err := runCmd(ctx, resolved.Path, rt.WorkDir, nil, "-m", "venv", venvDir); err != nil {
		return out, fmt.Errorf("创建 venv 失败: %w", err)
	}
	venvPython := managedVenvPython(rt.WorkDir)
	if venvPython == "" {
		return "", fmt.Errorf("venv 创建后未找到 python 二进制")
	}
	reqPath := filepath.Join(rt.WorkDir, "requirements.txt")
	out, err := runCmd(ctx, venvPython, rt.WorkDir, nil, "-m", "pip", "install", "-r", reqPath)
	return out, err
}

// installNodeDeps npm install（PATH 含 node bin，cmd.Dir=模块目录），返回命令输出。
func (s *ModuleService) installNodeDeps(ctx context.Context, rt *model.SkillRuntime) (string, error) {
	resolved, err := s.bootstrap.EnsureInterpreter(ctx, "node")
	if err != nil {
		return "", fmt.Errorf("获取 Node.js 失败: %w", err)
	}
	env := os.Environ()
	if binDir := nodeBinDirFor(resolved.Path); binDir != "" {
		env = prependPath(env, binDir)
	}
	out, err := runCmd(ctx, "npm", rt.WorkDir, env, "install")
	return out, err
}

// runCmd 执行命令并返回 combined output。command 带 PATH 解析（"npm" 经 LookPath）。
func runCmd(ctx context.Context, command, dir string, env []string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, command, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// nodeBinDirFor 据解析到的 node 路径推断其 bin 目录（供 npm 脚本找到 node）。
// 托管 node 用 managedNodeBinDir；系统 node 用其所在目录。
func nodeBinDirFor(nodePath string) string {
	if nodePath == "" {
		return ""
	}
	if mdir := managedNodeBinDir(); mdir != "" && strings.HasPrefix(filepath.Clean(nodePath), mdir) {
		return mdir
	}
	return filepath.Dir(nodePath)
}

// prependPath 把 dir 前插到 env 的 PATH（无则追加）。
func prependPath(env []string, dir string) []string {
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + dir + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// nodeModulesExists 判断模块目录下是否有 node_modules。
func nodeModulesExists(workDir string) bool {
	if info, err := os.Stat(filepath.Join(workDir, "node_modules")); err == nil && info.IsDir() {
		return true
	}
	return false
}

// managedVenvPython 廉价磁盘探测：模块目录下 .venv 的 python 二进制路径。
// 已建 venv 则返回绝对路径，否则空串。供 resolveStdioCommand（spawn）与 DepsStatus 复用。
func managedVenvPython(workDir string) string {
	if workDir == "" {
		return ""
	}
	candidates := []string{
		filepath.Join(workDir, ".venv", "bin", "python"),
		filepath.Join(workDir, ".venv", "bin", "python3"),
		filepath.Join(workDir, ".venv", "Scripts", "python.exe"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

// parseRequirements 解析 requirements.txt 为依赖列表。跳过注释、选项行（-e/--index-url 等）；
// 名称为首个版本 specifier/extras 之前的部分。
func parseRequirements(content string) []ModuleDep {
	var deps []ModuleDep
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 跳过选项行（-r / -e / --index-url 等）。
		if strings.HasPrefix(line, "-") {
			continue
		}
		// 去行内注释。
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		// 去环境标记（; python_version<'3'）。
		if i := strings.IndexByte(line, ';'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		// 名称为首个 [ (extras) 或版本 specifier 之前的部分。
		specIdx := len(line)
		for i := 0; i < len(line); i++ {
			c := line[i]
			if c == '[' || c == '<' || c == '>' || c == '=' || c == '!' || c == '~' {
				specIdx = i
				break
			}
		}
		name := strings.TrimSpace(line[:specIdx])
		spec := strings.TrimSpace(line[specIdx:])
		if name == "" {
			continue
		}
		deps = append(deps, ModuleDep{Name: name, Spec: spec})
	}
	return deps
}

// parsePackageDeps 解析 package.json 的 dependencies + devDependencies。
func parsePackageDeps(data []byte) []ModuleDep {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	var deps []ModuleDep
	for _, m := range []map[string]string{pkg.Dependencies, pkg.DevDependencies} {
		for name, ver := range m {
			deps = append(deps, ModuleDep{Name: name, Spec: ver})
		}
	}
	return deps
}
