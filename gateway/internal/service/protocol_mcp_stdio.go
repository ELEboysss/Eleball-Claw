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
	runtimeID       string
	stdin           io.WriteCloser
	stdout          io.Reader
	logger          *zap.Logger
	writeMu         sync.Mutex // 串行化 stdin 写入
	pendingMu       sync.Mutex // 保护 nextID 与 pending
	nextID          int
	pending         map[int]chan *mcpResponse // request id -> 响应通道
	initMu          sync.Mutex
	initialized     bool
	protocolVersion string                 // 协商回的协议版本（server 可能回退旧版）
	capabilities    map[string]interface{} // server 声明的 capabilities（resources/prompts 等，M5 伪工具合成依据）
	toolsMu         sync.RWMutex
	toolsCache      []MCPTool // 最近一次 tools/list 结果（内存 schema 缓存）
	done            chan struct{}
	once            sync.Once
	readErr         error
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
	p.mu.Lock()
	if old := p.sessions[runtimeID]; old != nil {
		old.close()
	}
	s := &stdioSession{
		runtimeID: runtimeID,
		stdin:     stdin,
		stdout:    stdout,
		logger:    p.logger,
		pending:   make(map[int]chan *mcpResponse),
		done:      make(chan struct{}),
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

// ListResources 获取 MCP 资源列表（resources/list）。
func (p *MCPStdioProtocol) ListResources(ctx context.Context, runtimeID string) ([]MCPResource, error) {
	s, err := p.session(runtimeID)
	if err != nil {
		return nil, err
	}
	return s.listResources(ctx)
}

// ReadResource 读取指定资源内容（resources/read）。
func (p *MCPStdioProtocol) ReadResource(ctx context.Context, runtimeID, uri string) (map[string]interface{}, error) {
	s, err := p.session(runtimeID)
	if err != nil {
		return nil, err
	}
	return s.readResource(ctx, uri)
}

// ListPrompts 获取 MCP 提示列表（prompts/list）。
func (p *MCPStdioProtocol) ListPrompts(ctx context.Context, runtimeID string) ([]MCPPrompt, error) {
	s, err := p.session(runtimeID)
	if err != nil {
		return nil, err
	}
	return s.listPrompts(ctx)
}

// GetPrompt 获取指定提示渲染结果（prompts/get）。
func (p *MCPStdioProtocol) GetPrompt(ctx context.Context, runtimeID, name string, arguments map[string]interface{}) (map[string]interface{}, error) {
	s, err := p.session(runtimeID)
	if err != nil {
		return nil, err
	}
	return s.getPrompt(ctx, name, arguments)
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
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    mcpClientCapabilities(),
		"clientInfo":      mcpClientInfo(),
	}
	resp, err := s.call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("MCP stdio initialize 失败: %w", err)
	}
	// 协商协议版本：server 可能回退到旧版，采用 server 版本并记录。
	s.protocolVersion = negotiateMCPVersion(resp.Result, mcpProtocolVersion, s.logger)
	// M5：捕获 server capabilities（resources/prompts 等），供 listTools 合成伪工具。
	s.capabilities = parseMCPServerCapabilities(resp.Result)
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
	// M5：据 server capabilities 合成 read_resource/get_prompt 伪工具。
	result.Tools = s.synthesizePseudoTools(ctx, result.Tools)
	s.toolsMu.Lock()
	s.toolsCache = result.Tools
	s.toolsMu.Unlock()
	return result.Tools, nil
}

// synthesizePseudoTools 据 server capabilities + resources/prompts 清单合成伪工具追加到 tools。
// 已存在同名真实工具时不合成；resources/list 或 prompts/list 拉取失败则跳过对应伪工具（仅记录日志）。
func (s *stdioSession) synthesizePseudoTools(ctx context.Context, tools []MCPTool) []MCPTool {
	existing := make(map[string]bool, len(tools))
	for _, t := range tools {
		existing[t.Name] = true
	}
	if s.hasCapability("resources") && !existing[mcpPseudoToolReadResource] {
		if resources, err := s.listResources(ctx); err == nil {
			tools = append(tools, mcpReadResourcePseudoTool(resources))
		} else if s.logger != nil {
			s.logger.Warn("MCP stdio resources/list 拉取失败，跳过 read_resource 伪工具",
				zap.String("runtime_id", s.runtimeID), zap.Error(err))
		}
	}
	if s.hasCapability("prompts") && !existing[mcpPseudoToolGetPrompt] {
		if prompts, err := s.listPrompts(ctx); err == nil {
			tools = append(tools, mcpGetPromptPseudoTool(prompts))
		} else if s.logger != nil {
			s.logger.Warn("MCP stdio prompts/list 拉取失败，跳过 get_prompt 伪工具",
				zap.String("runtime_id", s.runtimeID), zap.Error(err))
		}
	}
	return tools
}

// hasCapability 判断 server capabilities 是否声明了某能力（resources/prompts）。
func (s *stdioSession) hasCapability(capability string) bool {
	if s.capabilities == nil {
		return false
	}
	_, ok := s.capabilities[capability]
	return ok
}

// listResources 获取资源列表（resources/list）。
func (s *stdioSession) listResources(ctx context.Context) ([]MCPResource, error) {
	if err := s.initialize(ctx); err != nil {
		return nil, err
	}
	resp, err := s.call(ctx, "resources/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Resources []MCPResource `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP stdio resources/list 解析失败: %w", err)
	}
	return result.Resources, nil
}

// readResource 读取指定资源内容（resources/read）。
func (s *stdioSession) readResource(ctx context.Context, uri string) (map[string]interface{}, error) {
	if err := s.initialize(ctx); err != nil {
		return nil, err
	}
	resp, err := s.call(ctx, "resources/read", map[string]interface{}{"uri": uri})
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP stdio resources/read 解析失败: %w", err)
	}
	return result, nil
}

// listPrompts 获取提示列表（prompts/list）。
func (s *stdioSession) listPrompts(ctx context.Context) ([]MCPPrompt, error) {
	if err := s.initialize(ctx); err != nil {
		return nil, err
	}
	resp, err := s.call(ctx, "prompts/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Prompts []MCPPrompt `json:"prompts"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP stdio prompts/list 解析失败: %w", err)
	}
	return result.Prompts, nil
}

// getPrompt 获取指定提示渲染结果（prompts/get）。
func (s *stdioSession) getPrompt(ctx context.Context, name string, arguments map[string]interface{}) (map[string]interface{}, error) {
	if err := s.initialize(ctx); err != nil {
		return nil, err
	}
	params := map[string]interface{}{"name": name}
	if arguments != nil {
		params["arguments"] = arguments
	}
	resp, err := s.call(ctx, "prompts/get", params)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP stdio prompts/get 解析失败: %w", err)
	}
	return result, nil
}

// execute 调用 tools/call
func (s *stdioSession) execute(ctx context.Context, action string, params map[string]interface{}) (map[string]interface{}, error) {
	if err := s.initialize(ctx); err != nil {
		return nil, err
	}
	arguments := mcpCallArguments(params)
	// M5：伪工具拦截 -> resources/read / prompts/get（不进 tools/call）。
	switch action {
	case mcpPseudoToolReadResource:
		uri, _ := arguments["uri"].(string)
		return s.readResource(ctx, uri)
	case mcpPseudoToolGetPrompt:
		name, _ := arguments["name"].(string)
		var promptArgs map[string]interface{}
		if a, ok := arguments["arguments"].(map[string]interface{}); ok {
			promptArgs = a
		}
		return s.getPrompt(ctx, name, promptArgs)
	}
	resp, err := s.call(ctx, "tools/call", map[string]interface{}{
		"name":      action,
		"arguments": arguments,
	})
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
