package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// AR-E6：Shell 流式 stdout + BackgroundShell + 输出截断。
//
// 设计要点：
//   - 前台 Shell 由 CombinedOutput 同步阻塞改为 io.Pipe 流式读取合并 stdout/stderr，
//     按 headLimit 行截断防 context 爆；非零退出仍回带 output/exit_code 给模型诊断。
//   - BackgroundShell 在独立 context（与请求 ctx 分离）下后台执行长任务，输出累积到
//     有界缓冲（maxBgBufferBytes），轮询取最后 N 行，stop 经 cancel 终止进程。
//   - 超时与取消分离：后台进程不绑请求生命周期；仅靠 timeout 参数或 stop 动作终止。

const (
	// defaultShellHeadLimit 前台 Shell / 后台轮询默认返回行数上限。
	defaultShellHeadLimit = 2000
	// maxBgBufferBytes 后台 shell 输出缓冲上限（2MB）；超出后停止累积并置 bufferTruncated，
	// 防 dev server 海量日志撑爆内存。轮询仍可按 head_limit 取尾部行。
	maxBgBufferBytes = 2 * 1024 * 1024
)

// isShellLikeTool 判定是否为 shell 类工具（Shell / BackgroundShell）。
// 危险黑名单、只读 git 自动放行、审批风险等均按 shell 类统一处理，避免后台 shell 绕过。
func isShellLikeTool(name string) bool {
	return name == "Shell" || name == "BackgroundShell"
}

// parseHeadLimit 解析 head_limit 参数（JSON 数字多解为 float64），<=0 或缺省返回 def。
func parseHeadLimit(v interface{}, def int) int {
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return int(n)
		}
	case int:
		if n > 0 {
			return n
		}
	case int64:
		if n > 0 {
			return int(n)
		}
	}
	return def
}

// isExitError 判定 err 是否为非零退出（*exec.ExitError，可能被 %w 包裹）。
// 前台 Shell 对非零退出返回 result+nil（让模型看到输出与退出码），仅启动级错误返回 err。
func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

// prepareShell 归一化 + 危险黑名单校验，返回 (command, args, raw, err)。
// 供前台流式与后台启动共享：确保两路径都过危险检查（后台不可绕过）。
func prepareShell(command string, args []string) (string, []string, string, error) {
	command, args, err := normalizeShellInput(command, args)
	if err != nil {
		return "", nil, "", err
	}
	raw := rawCommandLine(command, args)
	if err := shellDangerous(raw); err != nil {
		return "", nil, "", err
	}
	return command, args, raw, nil
}

// buildExecCmd 依据 raw 是否含 shell 操作符，构造 *exec.Cmd（不启动）。
// 含操作符 -> bash -c buildCommandLine（支持管道/重定向/链式/$()）；否则直接 exec。
// 与旧 runViaShell 等价，但返回 cmd 交由调用方决定流式/后台启动方式。
func buildExecCmd(ctx context.Context, command string, args []string, raw, cwd string) (*exec.Cmd, error) {
	if hasShellOperator(raw) {
		shell := findShell()
		if shell == "" {
			return nil, errors.New("组合命令（管道/重定向/链式）需要 bash 解释器，未在 PATH 中找到 bash/sh；请安装 git-bash 或 WSL 并确保在 PATH")
		}
		cmd := exec.CommandContext(ctx, shell, "-c", buildCommandLine(command, args))
		if cwd != "" {
			cmd.Dir = cwd
		}
		return cmd, nil
	}
	cmd := exec.CommandContext(ctx, command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	return cmd, nil
}

// runStreamingCommand 流式执行已构造的 cmd，合并 stdout/stderr，按 headLimit 行截断。
// headLimit<=0 表示不限。返回合并输出、是否截断、退出码与错误（非零退出时 err 为 ExitError 包裹）。
//
// 流式而非 CombinedOutput：通过 io.Pipe 让 stdout/stderr 写入同一管道（exec 检测到
// Stdout==Stderr 同值时复用单一管道），读取侧按行累计至 headLimit 后继续 drain（不阻塞子进程），
// 既避免一次性缓冲海量输出，又能在截断后仍拿到退出码。
func runStreamingCommand(ctx context.Context, cmd *exec.Cmd, headLimit int) (output string, truncated bool, exitCode int, err error) {
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw // 同值 -> exec 合并为单一管道

	type readResult struct {
		output    string
		truncated bool
	}
	resCh := make(chan readResult, 1)

	go func() {
		defer pr.Close()
		reader := bufio.NewReaderSize(pr, 64*1024)
		var b strings.Builder
		trunc := false
		count := 0
		for {
			line, rerr := reader.ReadString('\n')
			if line != "" {
				if headLimit > 0 && count >= headLimit {
					trunc = true // 超限：继续读取 drain，但不累计，防子进程写阻塞
				} else {
					b.WriteString(line)
					count++
				}
			}
			if rerr != nil {
				break // io.EOF 或管道关闭
			}
		}
		resCh <- readResult{output: b.String(), truncated: trunc}
	}()

	startErr := cmd.Start()
	if startErr != nil {
		pw.Close()
		<-resCh
		return "", false, -1, startErr
	}

	waitErr := cmd.Wait()
	pw.Close() // Wait 返回后 exec 的 copy goroutine 已结束，关 pw 给读取侧 EOF
	res := <-resCh

	exitCode = 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
		err = fmt.Errorf("shell 执行失败: %w", waitErr)
	}
	return res.output, res.truncated, exitCode, err
}

// truncateLines 按行从头截断（用于 builtin 输出补截断）。limit<=0 不截断。
func truncateLines(s string, limit int) (string, bool) {
	if limit <= 0 {
		return s, false
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= limit {
		return s, false
	}
	return strings.Join(lines[:limit], "\n"), true
}

// tailLines 按行取最后 N 行（用于后台轮询：最新输出最有价值）。limit<=0 不截断。
func tailLines(s string, limit int) (string, bool) {
	if limit <= 0 {
		return s, false
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= limit {
		return s, false
	}
	return strings.Join(lines[len(lines)-limit:], "\n"), true
}

// backgroundShellReadOnly 判定 BackgroundShell 入参是否为只读操作（轮询）。
// 提供 shell_id 且 action 为空或 poll 时为只读：仅读取已累积输出，不产生副作用，
// 故在 default/acceptEdits/plan 模式下自动放行，避免轮询反复打断用户。
func backgroundShellReadOnly(input map[string]interface{}) bool {
	sid, _ := input["shell_id"].(string)
	if sid == "" {
		return false
	}
	action, _ := input["action"].(string)
	return action == "" || action == "poll"
}

// backgroundShell 后台 shell 运行实例。
type backgroundShell struct {
	id              string
	command         string
	startedAt       time.Time
	cancel          context.CancelFunc // 终止进程（stop / timeout）
	doneCh          chan struct{}      // goroutine 结束信号
	mu              sync.Mutex
	output          bytes.Buffer // 仅 reader goroutine 写（加锁），poll/stop 读（加锁）
	bufferTruncated bool         // 输出超 maxBgBufferBytes，停止累积
	done            bool
	exitCode        int
	err             error
}

// backgroundShellRegistry 后台 shell 注册表（ToolRegistry 持有，Clone 共享）。
type backgroundShellRegistry struct {
	mu      sync.Mutex
	shells  map[string]*backgroundShell
	counter uint64
}

func newBackgroundShellRegistry() *backgroundShellRegistry {
	return &backgroundShellRegistry{shells: make(map[string]*backgroundShell)}
}

func (r *backgroundShellRegistry) get(id string) *backgroundShell {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shells[id]
}

func (r *backgroundShellRegistry) add(bs *backgroundShell) {
	r.mu.Lock()
	r.shells[bs.id] = bs
	r.mu.Unlock()
}

func (r *backgroundShellRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.shells, id)
	r.mu.Unlock()
}

// start 后台启动 shell 命令，返回 shell_id。
// timeoutSec>0 时设最大运行时长；超时/stop 经 cancel 终止进程。与请求 ctx 分离。
func (r *backgroundShellRegistry) start(command string, args []string, cwd string, timeoutSec int) (string, error) {
	command, args, raw, err := prepareShell(command, args)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	if timeoutSec > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	}
	cmd, err := buildExecCmd(ctx, command, args, raw, cwd)
	if err != nil {
		cancel()
		return "", err
	}

	n := atomic.AddUint64(&r.counter, 1)
	bs := &backgroundShell{
		id:        fmt.Sprintf("bgsh-%d-%d", n, time.Now().UnixNano()),
		command:   command,
		startedAt: time.Now(),
		cancel:    cancel,
		doneCh:    make(chan struct{}),
	}

	go func() {
		defer close(bs.doneCh)
		pr, pw := io.Pipe()
		cmd.Stdout = pw
		cmd.Stderr = pw

		if err := cmd.Start(); err != nil {
			bs.mu.Lock()
			bs.done = true
			bs.exitCode = -1
			bs.err = err
			bs.mu.Unlock()
			pw.Close()
			io.Copy(io.Discard, pr)
			pr.Close()
			return
		}

		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			buf := make([]byte, 4096)
			for {
				n, rerr := pr.Read(buf)
				if n > 0 {
					bs.mu.Lock()
					if bs.output.Len() < maxBgBufferBytes {
						bs.output.Write(buf[:n])
					} else {
						bs.bufferTruncated = true
					}
					bs.mu.Unlock()
				}
				if rerr != nil {
					break
				}
			}
		}()

		waitErr := cmd.Wait()
		pw.Close()
		<-readerDone
		pr.Close()

		bs.mu.Lock()
		bs.done = true
		if waitErr != nil {
			if ee, ok := waitErr.(*exec.ExitError); ok {
				bs.exitCode = ee.ExitCode()
			} else {
				bs.exitCode = -1
			}
			bs.err = waitErr
		} else {
			bs.exitCode = 0
		}
		bs.mu.Unlock()
	}()

	r.add(bs)
	return bs.id, nil
}

// poll 读取后台 shell 当前累积输出（取最后 headLimit 行）与状态。
func (r *backgroundShellRegistry) poll(id string, headLimit int) (output string, truncated bool, status string, exitCode int, err error) {
	bs := r.get(id)
	if bs == nil {
		return "", false, "", 0, fmt.Errorf("未找到后台 shell: %s", id)
	}
	bs.mu.Lock()
	defer bs.mu.Unlock()

	full := bs.output.String()
	if headLimit > 0 {
		out, tr := tailLines(full, headLimit)
		output, truncated = out, tr || bs.bufferTruncated
	} else {
		output, truncated = full, bs.bufferTruncated
	}
	if bs.done {
		status = "done"
		exitCode = bs.exitCode
	} else {
		status = "running"
	}
	return
}

// stop 终止后台 shell 进程并等待其结束（最多 5s）。
func (r *backgroundShellRegistry) stop(id string) (status string, exitCode int, err error) {
	bs := r.get(id)
	if bs == nil {
		return "", 0, fmt.Errorf("未找到后台 shell: %s", id)
	}
	bs.mu.Lock()
	if bs.done {
		ec := bs.exitCode
		bs.mu.Unlock()
		return "done", ec, nil
	}
	bs.mu.Unlock()

	bs.cancel() // 经 ctx 终止进程（SIGKILL on cancel）
	select {
	case <-bs.doneCh:
	case <-time.After(5 * time.Second):
	}

	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.done {
		return "stopped", bs.exitCode, nil
	}
	return "running", -1, nil
}
