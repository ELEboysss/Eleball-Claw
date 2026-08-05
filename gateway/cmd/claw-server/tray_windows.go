//go:build windows

// claw-server 系统托盘（Windows）。
//
// 双击 exe / 安装后运行 claw-server 时，把前台 console 切换为后台进程：
//   - 托盘区留品牌图标；左键托盘图标 -> 显示/隐藏运行中的 console（终端）；
//   - 右键托盘图标 -> 菜单「对话 / 控制台 / 退出」；
//   - 启动后隐藏 console 并禁用其关闭按钮（关 X 会杀进程，故只能经托盘「退出」优雅关闭）。
//
// 库选型：fyne.io/systray（Windows 后端纯 Go syscall，无 cgo，保持 CGO_ENABLED=0 交叉编译）。
// systray.init() 调 runtime.LockOSThread()，故 systray.Run 必须在主 goroutine 调用——
// main() 直接调用本函数，HTTP 服务与模块自启已在各自 goroutine，阻塞在此安全。
// 非 Windows / CLAW_NO_TRAY=1 见 tray_other.go 或本文件 trayEnabled 早退，保持前台行为。
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"sync/atomic"

	"fyne.io/systray"
	"go.uber.org/zap"
	"golang.org/x/sys/windows"
)

//go:embed assets/icon.ico
var trayIconICO []byte

var (
	lazyKernel32        = windows.NewLazySystemDLL("kernel32.dll")
	pGetConsoleWindow   = lazyKernel32.NewProc("GetConsoleWindow")

	lazyUser32           = windows.NewLazySystemDLL("user32.dll")
	pShowWindow          = lazyUser32.NewProc("ShowWindow")
	pSetForegroundWindow = lazyUser32.NewProc("SetForegroundWindow")
	pGetSystemMenu       = lazyUser32.NewProc("GetSystemMenu")
	pEnableMenuItem      = lazyUser32.NewProc("EnableMenuItem")
	pDrawMenuBar         = lazyUser32.NewProc("DrawMenuBar")
)

// ShowWindow / EnableMenuItem 常量
const (
	swHide = 0 // SW_HIDE
	swShow = 5 // SW_SHOW

	scClose     = 0xF060 // SC_CLOSE
	mfByCommand = 0x00000000
	mfGrayed    = 0x00000001
)

// trayEnabled 是否启用 Windows 系统托盘。
// CLAW_NO_TRAY=1 关闭（headless / 远程会话 / dev 想看前台日志时）。
func trayEnabled() bool {
	return os.Getenv("CLAW_NO_TRAY") != "1"
}

// consoleHWND 返回本进程 console 窗口句柄；无 console（如 detached 启动）返回 0。
func consoleHWND() windows.HWND {
	hwnd, _, _ := pGetConsoleWindow.Call()
	return windows.HWND(hwnd)
}

// disableCloseButton 灰掉 console 窗口的关闭按钮（含 Alt+F4 / 系统菜单 Close），
// 防止用户误关终端杀掉服务；退出统一走托盘「退出」。
func disableCloseButton() {
	hwnd := consoleHWND()
	if hwnd == 0 {
		return
	}
	menu, _, _ := pGetSystemMenu.Call(uintptr(hwnd), 0) // FALSE -> 可改的系统菜单副本
	if menu == 0 {
		return
	}
	pEnableMenuItem.Call(uintptr(menu), uintptr(scClose), uintptr(mfByCommand|mfGrayed))
	pDrawMenuBar.Call(uintptr(hwnd))
}

// runTray 启动 Windows 系统托盘并阻塞，直到用户「退出」或 ctx 被取消（Ctrl+C）。
// onExit 调 cancel() 触发主 goroutine 的 <-ctx.Done() 优雅关闭流程。
func runTray(ctx context.Context, cancel context.CancelFunc, port int, logger *zap.Logger) {
	if !trayEnabled() {
		return // 前台模式
	}

	// console 可见性：onReady 后默认隐藏；左键托盘图标切换。
	var consoleVisible atomic.Bool

	showConsole := func() {
		hwnd := consoleHWND()
		if hwnd == 0 {
			return
		}
		pShowWindow.Call(uintptr(hwnd), uintptr(swShow))
		pSetForegroundWindow.Call(uintptr(hwnd))
		consoleVisible.Store(true)
	}
	hideConsole := func() {
		hwnd := consoleHWND()
		if hwnd == 0 {
			return
		}
		pShowWindow.Call(uintptr(hwnd), uintptr(swHide))
		consoleVisible.Store(false)
	}

	onReady := func() {
		systray.SetIcon(trayIconICO)
		systray.SetTooltip("Eleball-claw 本地网关")

		// 左键托盘图标 -> 切换运行中 console 显示/隐藏。
		systray.SetOnTapped(func() {
			if consoleVisible.Load() {
				hideConsole()
			} else {
				showConsole()
			}
		})

		mChat := systray.AddMenuItem("对话", "打开对话页")
		mConsole := systray.AddMenuItem("控制台", "打开本地控制台")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "退出 Eleball-claw")

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-mChat.ClickedCh:
					if !ok {
						return
					}
					openBrowser(fmt.Sprintf("http://localhost:%d/", port))
				}
			}
		}()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-mConsole.ClickedCh:
					if !ok {
						return
					}
					openBrowser(fmt.Sprintf("http://localhost:%d/admin", port))
				}
			}
		}()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-mQuit.ClickedCh:
					if !ok {
						return
					}
					systray.Quit()
				}
			}
		}()

		// 切换为后台：隐藏 console + 禁用关闭按钮（退出只能走托盘「退出」）。
		disableCloseButton()
		hideConsole()
		logger.Info("已最小化到系统托盘")
	}

	onExit := func() {
		// 触发主 goroutine 的 <-ctx.Done() 优雅关闭。
		cancel()
	}

	// Ctrl+C（console 显示时可用）取消 ctx 时，拆掉托盘让 Run 返回继续关闭流程。
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()

	systray.Run(onReady, onExit)
}
