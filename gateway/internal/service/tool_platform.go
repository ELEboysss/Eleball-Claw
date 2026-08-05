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
	// Shell 执行本地 shell 命令；cwd 非空时设 exec.Cmd.Dir=cwd（AR-06 claw 工作目录绑定），空则用进程 cwd。
	// claw 本地模型（D3）：支持管道/重定向/链式/$()/内联 -c；危险操作黑名单硬拒；写/shell 操作的确认由
	// PermissionService（agent_tool_loop 调用 Decide）处理，此处不再做 cloud 式白名单 + 元字符禁令沙箱。
	// AR-E6：Shell 现委托 ShellStream 流式执行（headLimit=0 不截断），保持旧全量输出语义。
	Shell(ctx context.Context, command string, args []string, cwd string) (string, error)
	// ShellStream 流式执行 shell 命令，合并 stdout/stderr，按 headLimit 行截断防 context 爆。
	// 返回合并输出、是否截断、退出码与错误（非零退出时 err 非 nil，含退出码）。headLimit<=0 不截断。
	ShellStream(ctx context.Context, command string, args []string, cwd string, headLimit int) (output string, truncated bool, exitCode int, err error)
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

// shellUsageHint Shell 调用格式提示：随格式类错误返回给模型，引导其自我纠正
const shellUsageHint = "本地 shell 支持管道 |、重定向 >/<、链式 &&/||/;、命令替换 $()、内联 -c/-e。两种用法：1) 整行命令放入 command、args 留空（组合命令请用此法，如 \"grep -rn foo . | head\"）；2) 主命令放入 command、参数放入 args（参数按字面量传递，含特殊字符自动转义）。危险操作（rm -rf /、> /dev/sd、sudo、mkfs 等）将被拒绝。"

// normalizeShellInput 归一化 Shell 入参。
// 约定（D3 本地模型）：
//   - 有 args：command 应为裸命令（无空格），args 按字面量传递（含特殊字符由 buildCommandLine 转义）。
//   - 无 args 且 command 含 shell 操作符/引号：原样透传为整行命令，交 bash 解释（支持管道/重定向/链式/$()）。
//   - 无 args 且 command 仅含空格分词：兼容弱模型把参数塞进 command 的场景，按空白分词。
func normalizeShellInput(command string, args []string) (string, []string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", nil, errors.New("命令不能为空")
	}
	if len(args) > 0 {
		if strings.ContainsAny(command, " \t") {
			return "", nil, fmt.Errorf("command 中不应包含参数（command=%q，args=%v）。%s", command, args, shellUsageHint)
		}
		return command, args, nil
	}
	if hasShellOperator(command) {
		return command, nil, nil
	}
	if strings.ContainsAny(command, " \t") {
		fields := strings.Fields(command)
		return fields[0], fields[1:], nil
	}
	return command, args, nil
}

// hasShellOperator 判断字符串是否含需要 shell 解释的操作符或引号。
// 命中则该命令必须经 bash -c 执行（直接 exec.Command 无法解释这些语法）。
func hasShellOperator(s string) bool {
	return strings.ContainsAny(s, "|&;<>()$\n`'\"")
}

// buildCommandLine 将 command+args 拼成单行命令字符串。
// args 为空时原样返回 command（可能含操作符，交 bash 解释）；
// args 非空时对每个 arg 做 shell 转义，确保含特殊字符的参数按字面量传递。
func buildCommandLine(command string, args []string) string {
	if len(args) == 0 {
		return command
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, command)
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// rawCommandLine 拼接未转义的原始命令行（用于危险操作检测，反映用户意图而非 shell 转义后的执行串）。
// 危险检测必须基于原始意图：如 rm -rf /* 中 /* 是 glob，转义后 '/*' 会掩盖危险特征。
func rawCommandLine(command string, args []string) string {
	if len(args) == 0 {
		return command
	}
	return command + " " + strings.Join(args, " ")
}

// shellQuote 对单个 shell 参数做单引号转义。
// 不含特殊字符时原样返回；含特殊字符时用单引号包裹，内部单引号用 '\'' 转义。
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\"'$|&;<>()*?[]{}~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellDangerousPatterns 灾难性操作黑名单（D3 危险层：硬拒，不可 allowlist）。
// 这是权限确认模型（PermissionService.Decide）之下的最后硬底线：即便用户确认，
// 此处匹配的操作仍直接拒绝。E1 阶段 sudo 亦硬拒，E2 可演进为二次确认。
var shellDangerousPatterns = []*regexp.Regexp{
	// rm -rf 目标为根/家目录/上级/通配（灾难性递归删除）
	regexp.MustCompile(`rm\s+(?:-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*|-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*|-r\s+-f|-f\s+-r)\s+(?:/(?:\s|$)|/\*|~(?:\s|$|/)|\.\.(?:/|$)|\*(?:\s|$))`),
	// 写裸盘设备
	regexp.MustCompile(`>\s*/dev/(?:sd|nvme|hd|disk|xvd)`),
	regexp.MustCompile(`\bdd\s+.*of=/dev/`),
	// 格式化文件系统
	regexp.MustCompile(`\bmkfs(?:\.\w+)?\b`),
	// fork bomb
	regexp.MustCompile(`:\s*\(\)\s*\{.*:.*&.*\}`),
	// sudo（本地单用户不应需要；E2 演进为二次确认）
	regexp.MustCompile(`\bsudo\b`),
}

// shellDangerous 检查整行命令是否命中危险操作黑名单。
func shellDangerous(s string) error {
	for _, re := range shellDangerousPatterns {
		if re.MatchString(s) {
			return fmt.Errorf("命令命中危险操作黑名单，已拒绝: %s", s)
		}
	}
	return nil
}

// shellInputDangerous 判定 Shell 工具入参是否命中危险操作黑名单（审批闸前置调用）。
// 提取 command + args 还原原始命令行（未转义，反映用户意图），交 shellDangerous 检测。
// 用于审批闸：危险命令在权限决策之前直接拒绝，不可被 always-allow 规则绕过、不可加入允许列表。
func shellInputDangerous(input map[string]interface{}) bool {
	command, _ := input["command"].(string)
	var args []string
	if rawArgs, ok := input["args"].([]interface{}); ok {
		for _, a := range rawArgs {
			if s, ok := a.(string); ok {
				args = append(args, s)
			}
		}
	}
	return shellDangerous(rawCommandLine(command, args)) != nil
}

// shellReadOnlyCommand 判定 Shell 入参是否为只读命令（E3：git 读操作自动放行）。
// 仅当命令为 git 的只读子命令（status/diff/log/blame/show）且不含 shell 操作符时返回 true。
// 含操作符（管道/重定向/链式）的命令不自动放行（可能产生副作用，走正常权限确认）。
func shellReadOnlyCommand(input map[string]interface{}) bool {
	command, _ := input["command"].(string)
	var args []string
	if rawArgs, ok := input["args"].([]interface{}); ok {
		for _, a := range rawArgs {
			if s, ok := a.(string); ok {
				args = append(args, s)
			}
		}
	}
	if hasShellOperator(rawCommandLine(command, args)) {
		return false
	}
	// 解析子命令：args[0] 优先；无 args 时取 command 的第二个字段
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	} else if fields := strings.Fields(command); len(fields) >= 2 {
		sub = fields[1]
	}
	switch sub {
	case "status", "diff", "log", "blame", "show":
		head := command
		if fields := strings.Fields(command); len(fields) > 0 {
			head = fields[0]
		}
		return head == "git"
	}
	return false
}

// findShell 查找可用的 shell 解释器（bash 优先，sh 次之）。
// 组合命令（管道/重定向/链式）需要 shell 解释；找不到时返回空。
func findShell() string {
	for _, name := range []string{"bash", "sh"} {
		if p := findExecutable(name); p != "" {
			return p
		}
	}
	return ""
}

// runViaShell 已被 buildExecCmd（含操作符路由）+ runStreamingCommand 取代（AR-E6 流式）。

// Shell 执行本地 shell 命令（D3 本地模型：去白名单/去元字符禁令，危险黑名单 + 真 bash）。
// AR-E6：委托 ShellStream 流式执行（headLimit=0 不截断），保持旧全量输出语义。
func (r *defaultPlatformRunner) Shell(ctx context.Context, command string, args []string, cwd string) (string, error) {
	out, _, _, err := r.ShellStream(ctx, command, args, cwd, 0)
	return out, err
}

// ShellStream 流式执行（Linux/macOS）：归一化 + 危险黑名单 + 路由 + 流式截断。
func (r *defaultPlatformRunner) ShellStream(ctx context.Context, command string, args []string, cwd string, headLimit int) (string, bool, int, error) {
	command, args, raw, err := prepareShell(command, args)
	if err != nil {
		return "", false, -1, err
	}
	cmd, err := buildExecCmd(ctx, command, args, raw, cwd)
	if err != nil {
		return "", false, -1, err
	}
	return runStreamingCommand(ctx, cmd, headLimit)
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
// 常用只读命令优先走 Go 内置实现（tool_shell_builtin.go），未覆盖的命令（python/node 等）回退外部进程；
// 组合命令（含管道/重定向）经 bash -c 执行（需 git-bash 在 PATH）。
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

// Shell 执行本地 shell 命令（Windows）。AR-E6：委托 ShellStream 流式执行（headLimit=0 不截断）。
func (r *windowsToolRunner) Shell(ctx context.Context, command string, args []string, cwd string) (string, error) {
	out, _, _, err := r.ShellStream(ctx, command, args, cwd, 0)
	return out, err
}

// ShellStream 流式执行（Windows）：归一化 + 危险黑名单 + 路由；无操作符命令优先 Go 内置实现
// （grep/find/ls/cat 等，输出按 headLimit 截断），未覆盖的命令（python/node 等）回退流式 exec。
func (r *windowsToolRunner) ShellStream(ctx context.Context, command string, args []string, cwd string, headLimit int) (string, bool, int, error) {
	command, args, raw, err := prepareShell(command, args)
	if err != nil {
		return "", false, -1, err
	}
	// 路由判定基于原始命令（用户意图）：含操作符才需 bash 解释。
	// 不能用 buildCommandLine 的转义结果判定，否则 args 中的 * 等被引号包裹后会误触发 bash 路由。
	if hasShellOperator(raw) {
		cmd, err := buildExecCmd(ctx, command, args, raw, cwd)
		if err != nil {
			return "", false, -1, err
		}
		return runStreamingCommand(ctx, cmd, headLimit)
	}
	// 简单命令（无操作符）：优先 Go 内置实现（grep/find/ls/cat/head/tail/wc/sort/uniq/cut 等），
	// 无需安装 Unix 工具；未覆盖的命令（python/node/npm/npx 等）回退流式外部进程。
	if out, handled, err := builtinShell(ctx, command, args); handled {
		trunc := false
		if headLimit > 0 {
			out, trunc = truncateLines(out, headLimit)
		}
		if err != nil {
			return out, trunc, -1, fmt.Errorf("shell 执行失败: %w", err)
		}
		return out, trunc, 0, nil
	}
	cmd, err := buildExecCmd(ctx, command, args, raw, cwd)
	if err != nil {
		return "", false, -1, err
	}
	return runStreamingCommand(ctx, cmd, headLimit)
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
