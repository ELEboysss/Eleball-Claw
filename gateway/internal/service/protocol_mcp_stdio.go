package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// MCPStdioProtocol stdio 子进程 JSON-RPC 2.0 MCP 协议适配器。
//
// 帧格式为换行分隔 JSON（NDJSON）：每条 JSON-RPC 消息独占一行。
// 请求/响应按 id 关联；无 id 的 notification（如 notifications/initialized）由 reader 丢弃。
// 单实例持有多个 runtime 的会话（map[runtimeID]*stdioSession），由 SkillRuntimeManager
// 在 spawn 子进程后 RegisterSession，SkillRuntimeRegistry 经同一实例调用 Execute/ListTools。
//
// 参考实现：providers/jcode/crates/jcode-base/src/mcp/client.rs。
type MCPStdioProtocol struct {
	mu       sync.Mutex
	sessions map[string]*stdioSession
	logger   *zap.Logger
	probeSeq atomic.Int64 // 一次性探测的临时会话 id 序列
}

// stdioSession 一个 stdio MCP 子进程的通信会话。
type stdioSession struct {
	runtimeID   string
	stdin       io.WriteCloser
	stdout      io.Reader
	writeMu     sync.Mutex // 串行化 stdin 写入
	pendingMu   sync.Mutex // 保护 nextID 与 pending
	nextID      int
	pending     map[int]chan *mcpResponse // request id -> 响应通道
	initMu      sync.Mutex
	initialized bool
	toolsMu     sync.RWMutex
	toolsCache  []MCPTool // 最近一次 tools/list 结果（内存 schema 缓存）
	done        chan struct{}
	once        sync.Once
	readErr     error
}

// NewMCPStdioProtocol 创建 stdio MCP 协议适配器
func NewMCPStdioProtocol(logger *zap.Logger) *MCPStdioProtocol {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MCPStdioProtocol{
		sessions: make(map[string]*stdioSession),
		logger:   logger,
	}
}

// SetLogger 设置日志器
func (p *MCPStdioProtocol) SetLogger(logger *zap.Logger) {
	if logger == nil {
		return
	}
	p.mu.Lock()
	p.logger = logger
	p.mu.Unlock()
}

// RegisterSession 注册一个 runtime 的 stdio 会话并启动 reader 协程。
// 若该 runtime 已有旧会话，先关闭旧会话（用于重连场景）。
func (p *MCPStdioProtocol) RegisterSession(runtimeID string, stdin io.WriteCloser, stdout io.Reader) {
	s := &stdioSession{
		runtimeID: runtimeID,
		stdin:     stdin,
		stdout:    stdout,
		pending:   make(map[int]chan *mcpResponse),
		done:      make(chan struct{}),
	}
	p.mu.Lock()
	if old := p.sessions[runtimeID]; old != nil {
		old.close()
	}
	p.sessions[runtimeID] = s
	p.mu.Unlock()
	go s.readLoop()
}

// UnregisterSession 关闭并移除一个 runtime 的会话（停止 reader、释放 pending）。
func (p *MCPStdioProtocol) UnregisterSession(runtimeID string) {
	p.mu.Lock()
	s := p.sessions[runtimeID]
	delete(p.sessions, runtimeID)
	p.mu.Unlock()
	if s != nil {
		s.close()
	}
}

// IsRegistered 会话是否已注册
func (p *MCPStdioProtocol) IsRegistered(runtimeID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.sessions[runtimeID]
	return ok
}

func (p *MCPStdioProtocol) session(runtimeID string) (*stdioSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[runtimeID]
	if !ok {
		return nil, fmt.Errorf("stdio MCP 会话未注册: %s", runtimeID)
	}
	return s, nil
}

// Initialize 执行 MCP initialize handshake
func (p *MCPStdioProtocol) Initialize(ctx context.Context, runtimeID string) error {
	s, err := p.session(runtimeID)
	if err != nil {
		return err
	}
	return s.initialize(ctx)
}

// ListTools 获取工具列表（强制刷新，用于健康探测与能力发现）
func (p *MCPStdioProtocol) ListTools(ctx context.Context, runtimeID string) ([]MCPTool, error) {
	s, err := p.session(runtimeID)
	if err != nil {
		return nil, err
	}
	return s.listTools(ctx, true)
}

// Execute 调用 MCP 工具
func (p *MCPStdioProtocol) Execute(runtimeID, action string, params map[string]interface{}) (map[string]interface{}, error) {
	s, err := p.session(runtimeID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	return s.execute(ctx, action, params)
}

// ProbeStdio 一次性探测候选 stdio MCP server（未注册）：spawn 子进程 -> initialize ->
// tools/list -> 返回工具 schema -> 关闭子进程。供 skill-maker 探测端点自动生成 module.json + SKU。
// 调用方负责沙箱校验（command/work_dir/env 白名单）。
func (p *MCPStdioProtocol) ProbeStdio(ctx context.Context, command string, args []string, env map[string]string, workDir string) ([]MCPTool, error) {
	if command == "" {
		return nil, errors.New("command 不能为空")
	}
	// D3：探测前预解析命令，缺失解释器时返回可读错误（带安装指引），
	// 供 ProbeMCP handler 转成结构化 interpreter_missing 响应。
	resolved, err := locateCommand(command)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(resolved, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动进程失败: %w", err)
	}

	// 确保子进程被清理
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	tempID := fmt.Sprintf("__probe_%d__", p.probeSeq.Add(1))
	p.RegisterSession(tempID, stdin, stdout)
	defer p.UnregisterSession(tempID)

	if err := p.Initialize(ctx, tempID); err != nil {
		return nil, err
	}
	return p.ListTools(ctx, tempID)
}

// --- 会话内部实现 ---

// initialize 执行握手（幂等）
func (s *stdioSession) initialize(ctx context.Context) error {
	s.initMu.Lock()
	defer s.initMu.Unlock()
	if s.initialized {
		return nil
	}
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "eleball-gateway",
			"version": "1.0.0",
		},
	}
	if _, err := s.call(ctx, "initialize", params); err != nil {
		return fmt.Errorf("MCP stdio initialize 失败: %w", err)
	}
	// 发送 initialized notification（无 id，无响应）
	notif := &mcpRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		Params:  map[string]interface{}{},
	}
	body, _ := json.Marshal(notif)
	body = append(body, '\n')
	s.writeMu.Lock()
	_, _ = s.stdin.Write(body)
	s.writeMu.Unlock()
	s.initialized = true
	return nil
}

// listTools 获取工具列表；force=false 时优先返回缓存
func (s *stdioSession) listTools(ctx context.Context, force bool) ([]MCPTool, error) {
	if !force {
		s.toolsMu.RLock()
		cached := s.toolsCache
		s.toolsMu.RUnlock()
		if cached != nil {
			return cached, nil
		}
	}
	if err := s.initialize(ctx); err != nil {
		return nil, err
	}
	resp, err := s.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP stdio tools/list 解析失败: %w", err)
	}
	s.toolsMu.Lock()
	s.toolsCache = result.Tools
	s.toolsMu.Unlock()
	return result.Tools, nil
}

// execute 调用 tools/call
func (s *stdioSession) execute(ctx context.Context, action string, params map[string]interface{}) (map[string]interface{}, error) {
	if err := s.initialize(ctx); err != nil {
		return nil, err
	}
	arguments := mcpCallArguments(params)
	callParams := map[string]interface{}{
		"name":      action,
		"arguments": arguments,
	}
	// per-call 凭证注入：把用户已配置的模块凭证经 MCP _meta 透传给子进程，子进程每次调用读取，
	// 不再在 spawn 时烤进 env（凭证随调用走，换 key 即时生效、无需 respawn）。mcpCallArguments
	// 仍把 credentials 从 arguments 剥离，工具入参保持干净；仅 _meta 携带凭证。
	if creds, ok := params["credentials"].(map[string]string); ok && len(creds) > 0 {
		callParams["_meta"] = map[string]interface{}{"credentials": creds}
	}
	resp, err := s.call(ctx, "tools/call", callParams)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP stdio tools/call 解析失败: %w", err)
	}
	if isError, ok := result["isError"].(bool); ok && isError {
		return nil, fmt.Errorf("MCP 工具错误: %s", extractMCPContent(result))
	}
	return result, nil
}

// call 发送 JSON-RPC 请求并等待对应 id 的响应
func (s *stdioSession) call(ctx context.Context, method string, params map[string]interface{}) (*mcpResponse, error) {
	s.pendingMu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan *mcpResponse, 1)
	s.pending[id] = ch
	s.pendingMu.Unlock()

	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
	}()

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')

	s.writeMu.Lock()
	_, werr := s.stdin.Write(body)
	s.writeMu.Unlock()
	if werr != nil {
		return nil, fmt.Errorf("stdio 写入失败: %w", werr)
	}

	select {
	case resp := <-ch:
		if resp == nil {
			return nil, fmt.Errorf("stdio MCP 会话已断开: %v", s.readErr)
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, fmt.Errorf("stdio MCP 会话已断开: %v", s.readErr)
	}
}

// readLoop 读取 stdout 按行解析 JSON-RPC 响应，按 id 投递到 pending 通道
func (s *stdioSession) readLoop() {
	scanner := bufio.NewScanner(s.stdout)
	// MCP tool schema 可能较大，扩容单行上限至 4MB
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	defer func() {
		err := io.EOF
		if e := scanner.Err(); e != nil {
			err = e
		}
		s.shutdown(err)
	}()
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp mcpResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue // 非 JSON 行（日志噪声等）忽略
		}
		id, ok := extractIntID(resp.ID)
		if !ok {
			continue // notification（无 id）丢弃
		}
		s.pendingMu.Lock()
		ch, exists := s.pending[id]
		s.pendingMu.Unlock()
		if !exists {
			continue // 已超时被清理的请求，丢弃迟到响应
		}
		select {
		case ch <- &resp:
		default:
		}
	}
}

// shutdown 关闭会话：记录断开原因、关闭 done、清空所有 pending（触发调用方返回错误）。
// readErr 仅在 once 内写入，避免并发数据竞争。
func (s *stdioSession) shutdown(err error) {
	s.once.Do(func() {
		s.readErr = err
		close(s.done)
		s.pendingMu.Lock()
		for id, ch := range s.pending {
			select {
			case ch <- nil:
			default:
			}
			delete(s.pending, id)
		}
		s.pendingMu.Unlock()
	})
}

// close 主动关闭会话（UnregisterSession / 重连替换旧会话时调用）。
// 先 shutdown 标记断开，再关 stdin 触发 reader 退出。
func (s *stdioSession) close() {
	s.shutdown(errors.New("session closed"))
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
}

// extractIntID 从 JSON-RPC id（interface{}）中提取 int
func extractIntID(id interface{}) (int, bool) {
	if id == nil {
		return 0, false
	}
	switch v := id.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

// mcpCallArguments 从调用参数中过滤内部键并展平 params，构造 tools/call 的 arguments。
// 与 MCPHTTPProtocol.Execute 的过滤逻辑一致，供 stdio/http 共用。
func mcpCallArguments(params map[string]interface{}) map[string]interface{} {
	arguments := make(map[string]interface{})
	for k, v := range params {
		if k == "__mcp_endpoint__" || k == "__mcp_server__" || k == "__mcp_headers__" ||
			k == "__runtime_id__" || k == "__module_id__" || k == "credentials" {
			continue
		}
		if k == "params" {
			if nested, ok := v.(map[string]interface{}); ok {
				for nk, nv := range nested {
					arguments[nk] = nv
				}
				continue
			}
		}
		arguments[k] = v
	}
	return arguments
}
