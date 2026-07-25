//go:build !windows

package service

// dockerFallbackDirs 非 Windows 平台下 docker 的常见安装位置。
// 覆盖 macOS Docker Desktop 符号链接目录与 Homebrew（Intel/Apple Silicon）。
func dockerFallbackDirs() []string {
	return []string{
		"/usr/local/bin",
		"/opt/homebrew/bin",
		"/Applications/Docker.app/Contents/Resources/bin",
	}
}
