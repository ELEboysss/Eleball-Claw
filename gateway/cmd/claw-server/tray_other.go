//go:build !windows

// claw-server 系统托盘非 Windows 回退。
// systray 的 macOS/Linux 后端需 cgo（Cocoa/GTK），与 claw 的 CGO_ENABLED=0 纯 Go 交叉编译冲突；
// 且本特性面向 GUI 桌面（Windows），故非 Windows 一律前台运行，保持原有 Ctrl+C/SIGTERM 行为。
package main

import (
	"context"

	"go.uber.org/zap"
)

// trayEnabled 非 Windows 恒为 false：前台运行。
func trayEnabled() bool {
	return false
}

// runTray 非 Windows no-op：claw 作为前台进程运行，靠 Ctrl+C / SIGTERM 优雅关闭（行为不变）。
func runTray(ctx context.Context, cancel context.CancelFunc, port int, logger *zap.Logger) {
	// no-op
}
