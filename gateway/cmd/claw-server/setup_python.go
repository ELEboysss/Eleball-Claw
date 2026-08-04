package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/eleball/gateway/internal/service"
	"go.uber.org/zap"
)

// setup-python 子命令：预装托管 Python 解释器（H1，降低安装成本）。
//
// 用户无系统 python 时，下载 astral-sh/python-build-standalone（SHA-256 校验）到
// ~/.eleball-claw/tools/python；locateCommand 在系统未找到时回退到该二进制。
// 供 D3 interpreter_missing 引导文案「运行 eleball-claw setup-python」与首次部署预装。
//
// 用法：
//
//	eleball-claw setup-python   下载/复用托管 Python（系统已有则直接复用）
//	eleball-claw setup          同上（setup 别名，后续可扩展装更多工具）
func runSetupPython(args []string) int {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	b := service.NewInterpreterBootstrap(logger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	resolved, err := b.EnsureInterpreter(ctx, "python")
	if err != nil {
		fmt.Fprintf(os.Stderr, "安装失败: %v\n", err)
		return 1
	}

	sourceLabel := map[string]string{"system": "系统", "managed": "托管"}[resolved.Source]
	if resolved.Source == "" {
		sourceLabel = resolved.Source
	}
	fmt.Printf("Python 就绪：%s\n", resolved.Path)
	if resolved.Version != "" {
		fmt.Printf("版本：%s（来源：%s）\n", resolved.Version, sourceLabel)
	} else {
		fmt.Printf("来源：%s\n", sourceLabel)
	}
	if resolved.Source == "system" {
		fmt.Println("检测到系统已安装 Python，无需托管下载。")
	} else if resolved.Reused {
		fmt.Println("复用已安装的托管 Python。")
	} else {
		fmt.Println("托管 Python 安装完成。后续 stdio MCP 模块将自动使用该解释器。")
	}
	return 0
}
