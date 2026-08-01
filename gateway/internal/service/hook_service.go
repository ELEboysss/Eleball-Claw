package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// HookOutcome 钩子事件聚合结果（T-C2-2）。
// 无匹配钩子时所有字段为默认值，表示「放行，无改动」。
type HookOutcome struct {
	// Decision 最终决策：allow / deny / ask；空值等价于 allow（无钩子影响）。
	Decision model.PermissionDecision
	// UpdatedInput 非空时覆盖原工具输入。
	UpdatedInput map[string]interface{}
	// SystemMessage 一次性系统提示，可注入当前轮对话。
	SystemMessage string
	// SetMode 非空时切换会话权限模式。
	SetMode string
	// BlockReason 阻断时的原因（展示/回喂模型）。
	BlockReason string
	// NonBlockingErrors 非阻断型钩子失败记录（如超时、非 2 退出码、解析失败）。
	NonBlockingErrors []string
}

// IsAllow 最终决策是否为放行（无影响或显式 allow）。
func (o *HookOutcome) IsAllow() bool {
	return o.Decision == "" || o.Decision == model.PermissionDecisionAllow
}

// hookResult 单个钩子执行原始结果。
type hookResult struct {
	output   model.HookOutput
	exitCode int
	timeout  bool
	err      error
	parseErr error
}

// merge 按 hook 配置顺序聚合结果。优先级：deny > ask > allow；
// updatedInput / setMode / systemMessage 取最后非空值。
func (o *HookOutcome) merge(r hookResult, cfg model.HookConfig) {
	if r.timeout {
		o.NonBlockingErrors = append(o.NonBlockingErrors, fmt.Sprintf("hook %q timeout", cfg.Name))
		return
	}
	if r.err != nil && r.exitCode != 2 {
		// 非 0 非 2 退出或进程未启动错误按非阻断处理
		o.NonBlockingErrors = append(o.NonBlockingErrors, fmt.Sprintf("hook %q error: %v", cfg.Name, r.err))
		return
	}
	if r.parseErr != nil {
		o.NonBlockingErrors = append(o.NonBlockingErrors, fmt.Sprintf("hook %q output parse failed: %v", cfg.Name, r.parseErr))
	}

	dec := normalizeHookDecision(r.output.Decision)
	if dec == "" && r.output.PermissionDecision != "" {
		dec = normalizeHookDecision(r.output.PermissionDecision)
	}
	if r.exitCode == 2 {
		dec = model.PermissionDecisionDeny
	}

	switch dec {
	case model.PermissionDecisionDeny:
		if o.Decision != model.PermissionDecisionDeny {
			o.Decision = model.PermissionDecisionDeny
			o.BlockReason = firstNonEmpty(r.output.Reason, strings.TrimSpace(r.output.SystemMessage), "hook blocked")
		}
	case model.PermissionDecisionAsk:
		if o.Decision != model.PermissionDecisionDeny {
			o.Decision = model.PermissionDecisionAsk
		}
	case model.PermissionDecisionAllow:
		if o.Decision == "" {
			o.Decision = model.PermissionDecisionAllow
		}
	}

	if len(r.output.UpdatedInput) > 0 {
		o.UpdatedInput = r.output.UpdatedInput
	}
	if r.output.SystemMessage != "" {
		o.SystemMessage = r.output.SystemMessage
	}
	if r.output.SetMode != "" {
		o.SetMode = r.output.SetMode
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func normalizeHookDecision(s string) model.PermissionDecision {
	switch strings.ToLower(s) {
	case "allow", "approve":
		return model.PermissionDecisionAllow
	case "deny", "block":
		return model.PermissionDecisionDeny
	case "ask":
		return model.PermissionDecisionAsk
	default:
		return ""
	}
}

// HookService 生命周期钩子配置加载与分发服务（C2）。
// 一期职责：从 hooks.json 加载配置、按事件索引、fsnotify 热重载、command 型钩子并行分发。
// prompt 型钩子由 T-C2-5 补齐，依赖注入的 LLM 客户端。
type HookService struct {
	mu        sync.RWMutex
	path      string
	watcher   *fsnotify.Watcher
	logger    *zap.Logger
	llmClient AgentLLMClient
	entries   map[model.HookEvent][]*hookEntry
	compiled  bool // 至少成功完成一次编译加载
}

// hookEntry 内部索引条目，含已编译的 matcher 正则。
type hookEntry struct {
	config *model.HookConfig
	re     *regexp.Regexp
}

// NewHookService 创建钩子服务并立即加载配置；path 为空时表示禁用钩子。
// 非空路径时启动 fsnotify 热重载，路径不存在则监听所在目录等待文件创建。
func NewHookService(path string, logger *zap.Logger) (*HookService, error) {
	s := &HookService{
		path:    path,
		logger:  logger,
		entries: make(map[model.HookEvent][]*hookEntry),
	}
	if path != "" {
		if err := s.Reload(); err != nil && !errors.Is(err, os.ErrNotExist) {
			// 首次加载失败仅记录，不阻塞网关启动（缺少配置时按空配置继续）
			logWarn(logger, "hook config initial load failed", "path", path, "error", err)
		}
		if err := s.startWatch(); err != nil {
			logWarn(logger, "hook config watcher failed", "path", path, "error", err)
		}
	}
	return s, nil
}

// SetLLMClient 注入 prompt 型钩子所需的 LLM 客户端（T-C2-5）。
func (s *HookService) SetLLMClient(client AgentLLMClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.llmClient = client
}

// Reload 立即重新加载配置文件。线程安全，可被 fsnotify 或单测显式调用。
func (s *HookService) Reload() error {
	if s.path == "" {
		s.setEntries(nil)
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		s.setEntries(nil)
		return err
	}
	var configs []model.HookConfig
	if err := json.Unmarshal(b, &configs); err != nil {
		return fmt.Errorf("解析 hooks.json 失败: %w", err)
	}
	entries := make(map[model.HookEvent][]*hookEntry)
	for i := range configs {
		cfg := &configs[i]
		if cfg.Event == "" {
			logWarn(s.logger, "hook entry missing event, skip", "index", i)
			continue
		}
		if cfg.Type != model.HookTypeCommand && cfg.Type != model.HookTypePrompt {
			logWarn(s.logger, "hook entry unknown type, skip", "index", i, "type", cfg.Type)
			continue
		}
		if cfg.Type == model.HookTypeCommand && cfg.Command == "" {
			logWarn(s.logger, "command hook missing command, skip", "index", i)
			continue
		}
		if cfg.Type == model.HookTypePrompt && cfg.Prompt == "" {
			logWarn(s.logger, "prompt hook missing prompt, skip", "index", i)
			continue
		}
		if cfg.Timeout.Duration() <= 0 {
			cfg.Timeout = model.JSONDuration(model.DefaultHookTimeout)
		}
		var re *regexp.Regexp
		if cfg.Matcher != "" {
			var err error
			re, err = regexp.Compile(cfg.Matcher)
			if err != nil {
				logWarn(s.logger, "hook matcher invalid, skip", "index", i, "matcher", cfg.Matcher, "error", err)
				continue
			}
		}
		entries[cfg.Event] = append(entries[cfg.Event], &hookEntry{config: cfg, re: re})
	}
	s.setEntries(entries)
	return nil
}

// MatchHooks 返回匹配指定事件与工具名的钩子配置副本（T-C2-2 分发器使用）。
func (s *HookService) MatchHooks(event model.HookEvent, toolName string) []model.HookConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.HookConfig
	for _, e := range s.entries[event] {
		if e.re == nil || e.re.MatchString(toolName) {
			out = append(out, *e.config)
		}
	}
	return out
}

// HasEvent 返回是否存在至少一个已编译的事件钩子。
func (s *HookService) HasEvent(event model.HookEvent) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries[event]) > 0
}

// Dispatch 执行指定事件下匹配 toolName 的所有钩子，并聚合结果（T-C2-2）。
// 仅支持 command 型；prompt 型返回非阻断错误，待 T-C2-5 实现。
// 并行执行，超时强杀，聚合优先级：deny > ask > allow，updatedInput/setMode 取最后非空。
func (s *HookService) Dispatch(ctx context.Context, event model.HookEvent, input model.HookInput) (HookOutcome, error) {
	var outcome HookOutcome
	hooks := s.MatchHooks(event, input.ToolName)
	if len(hooks) == 0 {
		return outcome, nil
	}
	stdin, err := CompileHookInput(input)
	if err != nil {
		return outcome, fmt.Errorf("compile hook input: %w", err)
	}

	results := make([]hookResult, len(hooks))
	var wg sync.WaitGroup
	for i := range hooks {
		wg.Add(1)
		go func(idx int, cfg model.HookConfig) {
			defer wg.Done()
			results[idx] = s.executeHook(ctx, cfg, stdin)
		}(i, hooks[i])
	}
	wg.Wait()

	for i, r := range results {
		outcome.merge(r, hooks[i])
	}
	return outcome, nil
}

// executeHook 执行单个钩子。command 型 spawn 子进程；prompt 型暂返回未实现错误。
func (s *HookService) executeHook(ctx context.Context, cfg model.HookConfig, stdin []byte) hookResult {
	switch cfg.Type {
	case model.HookTypeCommand:
		return s.runCommandHook(ctx, cfg, stdin)
	case model.HookTypePrompt:
		return s.runPromptHook(ctx, cfg, stdin)
	default:
		return hookResult{err: fmt.Errorf("unknown hook type %q", cfg.Type), exitCode: -1}
	}
}

// runPromptHook 执行 prompt 型钩子：将 cfg.Prompt 作为 system prompt，HookInput JSON 作为 user message，
// 调 LLM 返回 HookOutput JSON。超时按非阻断处理。
func (s *HookService) runPromptHook(ctx context.Context, cfg model.HookConfig, stdin []byte) hookResult {
	r := hookResult{}
	if s.llmClient == nil {
		r.err = errors.New("prompt hook requires LLM client (call SetLLMClient)")
		r.exitCode = -1
		return r
	}

	timeout := cfg.Timeout.Duration()
	if timeout <= 0 {
		timeout = model.DefaultHookTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req := llm.ChatRequest{
		Model:     "prompt-hook",
		Messages:  []llm.Message{{Role: "system", Content: cfg.Prompt}, {Role: "user", Content: string(stdin)}},
		MaxTokens: 500,
		Stream:    false,
	}
	resp, err := s.llmClient.Chat(ctx, req)
	if err != nil {
		r.err = err
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			r.timeout = true
		}
		r.exitCode = -1
		return r
	}
	if resp == nil || resp.Delta == "" {
		return r
	}
	out, perr := ParseHookOutput([]byte(resp.Delta))
	if perr != nil {
		r.parseErr = perr
		return r
	}
	r.output = out
	return r
}

// runCommandHook 执行 command 型钩子：spawn 子进程，stdin 注入 HookInput，
// 收集 stdout/stderr 与退出码。退出码 2 表示阻断；0 表示放行；其他为非阻断错误。
func (s *HookService) runCommandHook(ctx context.Context, cfg model.HookConfig, stdin []byte) hookResult {
	r := hookResult{}
	shell, args, err := resolveShell(cfg.Command)
	if err != nil {
		r.err = err
		r.exitCode = -1
		return r
	}

	timeout := cfg.Timeout.Duration()
	if timeout <= 0 {
		timeout = model.DefaultHookTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		r.err = err
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			r.timeout = true
			r.exitCode = -1
			return r
		}
	}
	if cmd.ProcessState != nil {
		r.exitCode = cmd.ProcessState.ExitCode()
	}
	if stdout.Len() > 0 {
		out, perr := ParseHookOutput(stdout.Bytes())
		if perr != nil {
			r.parseErr = perr
		} else {
			r.output = out
		}
	}
	return r
}

// resolveShell 选择可用的 shell 执行命令字符串；优先 bash，次选 sh，Windows 兜底 cmd。
func resolveShell(command string) (shell string, args []string, err error) {
	if _, e := exec.LookPath("bash"); e == nil {
		return "bash", []string{"-c", command}, nil
	}
	if _, e := exec.LookPath("sh"); e == nil {
		return "sh", []string{"-c", command}, nil
	}
	if runtime.GOOS == "windows" {
		if _, e := exec.LookPath("cmd"); e == nil {
			return "cmd", []string{"/c", command}, nil
		}
	}
	return "", nil, errors.New("no shell (bash/sh/cmd) found for hook command")
}

// Close 关闭 fsnotify watcher。
func (s *HookService) Close() error {
	if s.watcher == nil {
		return nil
	}
	return s.watcher.Close()
}

func (s *HookService) setEntries(entries map[model.HookEvent][]*hookEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = entries
	s.compiled = true
}

func (s *HookService) startWatch() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	s.watcher = watcher

	addWatch := func(p string) {
		if err := watcher.Add(p); err != nil {
			logWarn(s.logger, "hook watcher add failed", "path", p, "error", err)
		}
	}

	// 监听文件本身（编辑）及所在目录（删除/重建/重命名）
	dir := filepath.Dir(s.path)
	if _, err := os.Stat(s.path); err == nil {
		addWatch(s.path)
	} else if !errors.Is(err, os.ErrNotExist) {
		logWarn(s.logger, "hook config stat failed", "path", s.path, "error", err)
	}
	if dir != "" && dir != "." {
		addWatch(dir)
	}

	go s.watchLoop()
	return nil
}

func (s *HookService) watchLoop() {
	if s.watcher == nil {
		return
	}
	debounce := time.NewTimer(0)
	<-debounce.C // 初始空定时器已触发，直接消耗
	var pending bool
	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			if event.Name != s.path {
				continue
			}
			// 写、创建、重命名均触发重载；删除则清空配置
			if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				s.setEntries(nil)
				logInfo(s.logger, "hook config removed, cleared hooks")
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Chmod) {
				// 防抖 200ms：编辑器可能先写临时文件再重命名
				if !pending {
					pending = true
					debounce = time.NewTimer(200 * time.Millisecond)
				}
			}
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			logWarn(s.logger, "hook watcher error", "error", err)
		case <-debounce.C:
			if !pending {
				continue
			}
			pending = false
			if err := s.Reload(); err != nil {
				logWarn(s.logger, "hook config hot reload failed", "error", err)
			} else {
				logInfo(s.logger, "hook config reloaded", "path", s.path)
			}
		}
	}
}

// logInfo / logWarn 兼容 nil logger，避免依赖包增加 logger 注入成本。
func logInfo(logger *zap.Logger, msg string, fields ...interface{}) {
	if logger == nil {
		return
	}
	logger.Info(msg, toZapFields(fields...)...)
}

func logWarn(logger *zap.Logger, msg string, fields ...interface{}) {
	if logger == nil {
		return
	}
	logger.Warn(msg, toZapFields(fields...)...)
}

// DispatchPreCompact 执行 PreCompact 生命周期钩子（C2 骨架，供 C4 调用）。
// 将待压缩摘要作为 tool_result 传入，钩子可返回 systemMessage 要求保留或 decision=block 取消压缩。
func (s *HookService) DispatchPreCompact(ctx context.Context, sessionID, conversationID, cwd, summary string) (HookOutcome, error) {
	input := model.HookInput{
		SessionID:      sessionID,
		ConversationID: conversationID,
		Cwd:            cwd,
		ToolName:       "compact",
		HookEventName:  string(model.HookEventPreCompact),
		ToolResult: map[string]interface{}{
			"summary": summary,
		},
	}
	return s.Dispatch(ctx, model.HookEventPreCompact, input)
}

func toZapFields(kv ...interface{}) []zap.Field {
	if len(kv)%2 != 0 {
		kv = append(kv, "missing")
	}
	fields := make([]zap.Field, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", kv[i])
		}
		fields = append(fields, zap.Any(key, kv[i+1]))
	}
	return fields
}

// CompileHookInput 将输入结构序列化为 JSON，供 command 型钩子 stdin 使用。
func CompileHookInput(input model.HookInput) ([]byte, error) {
	return json.Marshal(input)
}

// ParseHookOutput 解析钩子 stdout JSON；空/非法 JSON 返回空输出（按放行处理）。
func ParseHookOutput(data []byte) (model.HookOutput, error) {
	var out model.HookOutput
	if len(data) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

// hookCtxKey 用于把 HookService 注入 context（可选）。
type hookCtxKey struct{}

// ContextWithHookService 把服务注入 context，供深层函数取用。
func ContextWithHookService(ctx context.Context, svc *HookService) context.Context {
	return context.WithValue(ctx, hookCtxKey{}, svc)
}

// HookServiceFromContext 从 context 取出服务；不存在返回 nil。
func HookServiceFromContext(ctx context.Context) *HookService {
	if svc, ok := ctx.Value(hookCtxKey{}).(*HookService); ok {
		return svc
	}
	return nil
}

// Ensure 占位，避免编译器因仅导入 context 报错；T-C2-2 使用。
var _ = context.Background
