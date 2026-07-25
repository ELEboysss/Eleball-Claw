package service

import (
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
// 对策：LookPath 未命中时，回退探测 Docker Desktop 默认安装目录与注册表
// Path（见 docker_path_windows.go），找到后把所在目录临时注入进程 PATH，
// 使后续所有 exec.Command("docker", ...)（compose 上下线、镜像安装、
// system status 探测等）无需逐一改造即可生效。

var (
	dockerResolveOnce sync.Once
	dockerResolved    string
)

// ResolveDocker 返回 docker CLI 的可用路径；未找到返回空串。
// 优先进程 PATH，未命中回退平台相关的常见安装位置。
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
	return ""
}

// EnsureDockerOnPath 解析 docker 并在需要时把其目录注入进程 PATH（幂等）。
// 返回最终可用的 docker 路径（仍不可用返回空串）。
// 注意：只修改当前进程环境，不影响系统配置。
func EnsureDockerOnPath() string {
	dockerResolveOnce.Do(func() {
		p := ResolveDocker()
		if p == "" {
			return
		}
		// p 来自回退路径（PATH 中没有）时，注入目录让子进程同样可见
		if _, err := exec.LookPath("docker"); err != nil {
			dir := filepath.Dir(p)
			os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
		dockerResolved = p
	})
	return dockerResolved
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
