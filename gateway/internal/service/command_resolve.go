package service

import (
	"errors"
	"fmt"
	"os/exec"
)

// InterpreterMissingError 命令未找到的可读错误，供 web 展示安装引导按钮。
// 对标 openhuman spawn_env::missing_command_error：把裸的 "not found" 转成可操作指引。
type InterpreterMissingError struct {
	Command string // 缺失的命令（python/npx/node/...）
	Hint    string // 安装指引文案
}

func (e *InterpreterMissingError) Error() string {
	return fmt.Sprintf("未找到命令 %s：%s", e.Command, e.Hint)
}

// interpreterHints 已知解释器/命令缺失时的安装指引（D3 可读报错）。
// 后续可由 claw.yaml 的 interpreter_hints 覆盖（H1 托管解释器引导）。
var interpreterHints = map[string]string{
	"python":  "未找到 Python，请安装 3.10+（https://www.python.org/downloads/）或运行 eleball-claw setup-python",
	"python3": "未找到 Python，请安装 3.10+（https://www.python.org/downloads/）或运行 eleball-claw setup-python",
	"node":    "未找到 Node.js，请安装 18+（https://nodejs.org/）或运行 eleball-claw setup-node",
	"npx":     "未找到 Node.js/npx，请安装 18+（https://nodejs.org/）或运行 eleball-claw setup-node",
	"uv":      "未找到 uv，请安装（https://docs.astral.sh/uv/）",
	"uvx":     "未找到 uv/uvx，请安装（https://docs.astral.sh/uv/）",
}

// locateCommand 预解析启动命令：exec.LookPath 找到则返回完整路径；
// 未找到时回退托管解释器（H1，仅廉价磁盘探测，不联网下载），仍无则返回带安装指引的 InterpreterMissingError。
// 供 stdio spawn（autostart）与 ProbeStdio（skill-maker 探测）在 spawn 前给出可操作报错，
// 避免 Windows 上 python/npx 缺失时只抛 "executable file not found"。
// 托管下载由显式动作触发（POST /claw-console/tools/install-interpreter / eleball-claw setup-python），
// 此处绝不联网，避免 autostart/probe 阻塞数分钟。
func locateCommand(command string) (string, error) {
	if command == "" {
		return "", errors.New("command 不能为空")
	}
	if path, err := exec.LookPath(command); err == nil {
		return path, nil
	}
	// H1：系统未找到时回退已安装的托管解释器（python 家族）。
	if managed := managedInterpreterPath(command); managed != "" {
		return managed, nil
	}
	hint := "请确认该命令已安装并在 PATH 中"
	if h, ok := interpreterHints[command]; ok {
		hint = h
	}
	return "", &InterpreterMissingError{Command: command, Hint: hint}
}
