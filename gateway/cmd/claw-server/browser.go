package main

import (
	"os"
	"os/exec"
	"runtime"
	"time"

	"go.uber.org/zap"
)

// openBrowserDelayed 延迟片刻后用系统默认浏览器打开目标地址。
//
// 延迟是为了让 HTTP 服务先完成监听，避免浏览器拿到连接拒绝。
// CLAW_NO_BROWSER=1 可关闭该行为（远程/服务化部署场景）；
// Linux 下无 DISPLAY（纯 headless 服务器）时自动跳过。
func openBrowserDelayed(url string, logger *zap.Logger) {
	if os.Getenv("CLAW_NO_BROWSER") == "1" {
		return
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" {
		return
	}
	go func() {
		time.Sleep(800 * time.Millisecond)
		if err := openBrowser(url); err != nil {
			logger.Warn("自动打开浏览器失败，请手动访问", zap.String("url", url), zap.Error(err))
		} else {
			logger.Info("已自动打开浏览器", zap.String("url", url))
		}
	}()
}

// openBrowser 按平台调起系统默认浏览器
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 方式无需额外依赖，且不会把 URL 中的特殊字符交给 cmd 解析
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default: // linux 及其他 freedesktop 环境
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
