package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// PlatformToolRunner 定义跨平台工具执行抽象
type PlatformToolRunner interface {
	OCR(ctx context.Context, imagePath string) (string, error)
	Shell(ctx context.Context, command string, args []string) (string, error)
}

// NewPlatformRunner 根据当前操作系统返回合适的运行器
func NewPlatformRunner() PlatformToolRunner {
	if runtime.GOOS == "windows" {
		return &windowsToolRunner{}
	}
	return &defaultPlatformRunner{}
}

// defaultPlatformRunner Linux/macOS 跨平台运行器，通过命令行调用外部工具
type defaultPlatformRunner struct{}

// OCR 调用 tesseract 命令行识别图片文字
func (r *defaultPlatformRunner) OCR(ctx context.Context, imagePath string) (string, error) {
	bin := findExecutable("tesseract")
	if bin == "" {
		return "", errors.New("未找到 tesseract 命令。Ubuntu: sudo apt-get install tesseract-ocr tesseract-ocr-chi-sim; Windows: 安装 tesseract 并加入 PATH")
	}
	cmd := exec.CommandContext(ctx, bin, imagePath, "stdout", "-l", "chi_sim+eng")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("OCR 执行失败: %w, output: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// 允许执行的 shell 命令白名单；不在此列表中的命令会被拒绝，降低命令注入与误操作风险。
// 仅放开只读、非敏感的信息类命令，避免暴露密钥或执行危险操作。
var shellCommandWhitelist = map[string]bool{
	"ls": true, "cat": true, "pwd": true, "echo": true, "printf": true,
	"head": true, "tail": true, "wc": true, "grep": true, "find": true,
	"sort": true, "uniq": true, "cut": true, "awk": true, "sed": true,
	"python3": true, "python": true, "pip": true, "pip3": true,
	"node": true, "npm": true, "npx": true,
	"which": true, "where": true, "file": true, "stat": true, "dirname": true, "basename": true,
	// 只读系统信息类命令
	"date": true, "cal": true, "whoami": true, "uname": true,
	"hostname": true, "uptime": true,
}

// 参数中禁止的内联执行选项，防止通过 python -c / node -e 等绕过白名单
var shellInlineExecFlags = map[string]bool{
	"-c": true, "-e": true, "--eval": true, "--exec": true,
}

// shellUsageHint Shell 调用格式提示：随格式类错误返回给模型，引导其自我纠正
const shellUsageHint = "正确格式：command 只填主命令（不含空格），参数放入 args 数组；不支持管道 |、重定向 >/<、多命令（&&/||/;）、内联执行（-c/-e）。示例：{\"command\":\"grep\",\"args\":[\"-rn\",\"keyword\",\".\"]}"

// normalizeShellInput 归一化 Shell 入参。
// 兼容弱模型的常见错误——把整行命令塞进 command 而不用 args：
// command 含空格且 args 为空时按空白分词，首 token 为主命令、其余为参数；
// command 含空格且 args 非空时返回带格式提示的错误。
func normalizeShellInput(command string, args []string) (string, []string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", nil, errors.New("命令不能为空")
	}
	if !strings.ContainsAny(command, " \t") {
		return command, args, nil
	}
	if len(args) > 0 {
		return "", nil, fmt.Errorf("command 中不应包含参数（command=%q，args=%v）。%s", command, args, shellUsageHint)
	}
	fields := strings.Fields(command)
	return fields[0], fields[1:], nil
}

// Shell 执行受限 shell 命令（命令白名单 + 参数安全过滤）
func (r *defaultPlatformRunner) Shell(ctx context.Context, command string, args []string) (string, error) {
	command, args, err := normalizeShellInput(command, args)
	if err != nil {
		return "", err
	}
	if err := shellSafe(command); err != nil {
		return "", fmt.Errorf("%w。%s", err, shellUsageHint)
	}
	if !shellCommandWhitelist[command] {
		return "", fmt.Errorf("命令 '%s' 不在白名单中", command)
	}
	for _, a := range args {
		if err := shellSafe(a); err != nil {
			return "", fmt.Errorf("参数包含非法字符: %s。%s", a, shellUsageHint)
		}
		if shellInlineExecFlags[a] {
			return "", fmt.Errorf("参数 '%s' 被禁止：不允许内联执行代码", a)
		}
	}
	cmd := exec.CommandContext(ctx, command, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("shell 执行失败: %w", err)
	}
	return string(out), nil
}

// findExecutable 查找可执行文件，支持 Windows .exe 后缀
func findExecutable(name string) string {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath(name + ".exe"); err == nil {
			return name + ".exe"
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// windowsToolRunner Windows 平台运行器
// Windows 没有 grep/find/ls 等 Unix 工具，且 cmd 自带 find.exe 与 GNU find 语义完全不同：
// 常用只读命令优先走 Go 内置实现（tool_shell_builtin.go），未覆盖的命令（python/node 等）回退外部进程。
type windowsToolRunner struct{}

func (r *windowsToolRunner) OCR(ctx context.Context, imagePath string) (string, error) {
	bin := findExecutable("tesseract")
	if bin == "" {
		return "", errors.New("未找到 tesseract.exe。Windows 调试方案：1) 从 https://github.com/UB-Mannheim/tesseract/wiki 下载安装并加入 PATH；2) 或暂时使用 SearchWeb 工具替代 OCR。")
	}
	cmd := exec.CommandContext(ctx, bin, imagePath, "stdout", "-l", "chi_sim+eng")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("OCR 执行失败: %w, output: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *windowsToolRunner) Shell(ctx context.Context, command string, args []string) (string, error) {
	command, args, err := normalizeShellInput(command, args)
	if err != nil {
		return "", err
	}
	if err := shellSafe(command); err != nil {
		return "", fmt.Errorf("%w。%s", err, shellUsageHint)
	}
	if !shellCommandWhitelist[command] {
		return "", fmt.Errorf("命令 '%s' 不在白名单中", command)
	}
	for _, a := range args {
		if err := shellSafe(a); err != nil {
			return "", fmt.Errorf("参数包含非法字符: %s。%s", a, shellUsageHint)
		}
		if shellInlineExecFlags[a] {
			return "", fmt.Errorf("参数 '%s' 被禁止：不允许内联执行代码", a)
		}
	}
	// 优先使用 Go 内置实现（grep/find/ls/cat/head/tail/wc/sort/uniq/cut 等），无需安装 Unix 工具
	if out, handled, err := builtinShell(ctx, command, args); handled {
		if err != nil {
			return out, fmt.Errorf("shell 执行失败: %w", err)
		}
		return out, nil
	}
	// 未覆盖的白名单命令（python/node/npm/npx 等）回退为外部进程调用
	cmd := exec.CommandContext(ctx, command, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("shell 执行失败: %w", err)
	}
	return string(out), nil
}

// shellSafe 检查命令/参数是否包含危险字符，防止命令注入
func shellSafe(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("命令不能为空")
	}
	// 禁止 shell 元字符与多命令
	dangerous := regexp.MustCompile("[;|&$\\(\\)\\`\\<>\\n\\r]")
	if dangerous.MatchString(s) {
		return fmt.Errorf("命令包含非法字符: %s", s)
	}
	return nil
}

// sanitized 将提示词中的特殊字符替换为下划线，用于文件名
func sanitized(s string) string {
	re := regexp.MustCompile("[^a-zA-Z0-9\\u4e00-\\u9fa5_-]")
	s = re.ReplaceAllString(s, "_")
	runes := []rune(s)
	if len(runes) > 40 {
		s = string(runes[:40])
	}
	if s == "" {
		s = "video"
	}
	return s
}

// fileExists 判断文件是否存在
type fileExists bool

func (fileExists) check(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}
