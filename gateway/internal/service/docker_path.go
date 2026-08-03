package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// docker 路径解析：解决「exe 直接运行时误报未检测到 docker」。
//
// 根因：Windows 下 Docker Desktop 只把安装目录写进注册表中的用户/系统 Path，
// 需要重新登录（或重启资源管理器）后才传播到新进程。若 Docker 安装后直接用
// 旧终端/旧资源管理器会话启动 claw exe，进程 PATH 中没有 docker，
// exec.LookPath("docker") 即失败——尽管 Docker 已安装。
//
// 对策：
//   1. LookPath 未命中时，回退探测 Docker Desktop 默认安装目录与注册表
//      Path（见 docker_path_windows.go），找到后把所在目录临时注入进程 PATH。
//   2. 仍找不到但本机已启用 WSL 且 `wsl docker --version` 成功时，标记使用
//      WSL 桥接模式；后续 DockerCommand 自动转发为 `wsl docker ...`。

var (
	dockerResolveOnce sync.Once
	dockerResolved    string
	// wslDocker 为 true 时表示当前进程通过 WSL 桥接调用 docker（`wsl docker ...`）
	wslDocker bool
)

// ResolveDocker 返回 docker CLI 的可用路径；未找到返回空串。
// 优先进程 PATH，未命中回退平台相关的常见安装位置；Windows 下仍找不到时
// 若 `wsl docker` 可用，返回字符串 "wsl" 作为占位，并设置 wslDocker 标志。
func ResolveDocker() string {
	if p, err := exec.LookPath("docker"); err == nil {
		return p
	}
	for _, dir := range dockerFallbackDirs() {
		for _, name := range dockerCandidateNames() {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	// 兜底：WSL 内已安装 docker，但 Windows PATH 中没有原生客户端
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		if cmd := exec.Command("wsl", "docker", "--version"); cmd.Run() == nil {
			wslDocker = true
			return "wsl"
		}
	}
	return ""
}

// EnsureDockerOnPath 解析 docker 并在需要时把其目录注入进程 PATH（幂等）。
// 返回最终可用的 docker 路径（仍不可用返回空串）；使用 WSL 桥接时返回 "wsl"。
// 注意：只修改当前进程环境，不影响系统配置。
func EnsureDockerOnPath() string {
	dockerResolveOnce.Do(func() {
		p := ResolveDocker()
		if p == "" {
			return
		}
		// p 来自回退路径（PATH 中没有）时，注入目录让子进程同样可见
		if _, err := exec.LookPath("docker"); err != nil && !wslDocker {
			dir := filepath.Dir(p)
			os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
		dockerResolved = p
	})
	return dockerResolved
}

// DockerCommand 构造一条 docker 命令；在 WSL 桥接模式下实际执行 `wsl docker <args...>`。
func DockerCommand(ctx context.Context, args ...string) *exec.Cmd {
	if wslDocker {
		return exec.CommandContext(ctx, "wsl", append([]string{"docker"}, args...)...)
	}
	return exec.CommandContext(ctx, "docker", args...)
}

// dockerCandidateNames 平台相关的 docker 可执行文件候选名。
// Windows 下除 docker.exe（Docker Desktop）外，还可能是用户自建的
// docker.bat / docker.cmd 桥接 shim（如 WSL 桥接）。
func dockerCandidateNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"docker.exe", "docker.bat", "docker.cmd"}
	}
	return []string{"docker"}
}
