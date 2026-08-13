package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
	"go.uber.org/zap"
)

// MCPConnectionStatus MCP 连接状态（T2.5）。
// 与 SkillRuntimeStatus 正交：前者是连接层状态机，后者是运行时整体状态；
// manager.onStatusChange 把连接状态映射为运行时状态（connected→active / failed→degraded）
// 触发 SKU 展示更新（ModuleOnline 列表时现算）。
type MCPConnectionStatus string

const (
	// MCPConnectionStatusPending 连接建立中（首次 Connect 前 / 重连中）
	MCPConnectionStatusPending MCPConnectionStatus = "pending"
	// MCPConnectionStatusConnected 已连接且工具已发现
	MCPConnectionStatusConnected MCPConnectionStatus = "connected"
	// MCPConnectionStatusFailed 连接/工具发现失败（可重连恢复）
	MCPConnectionStatusFailed MCPConnectionStatus = "failed"
	// MCPConnectionStatusDisabled 运行时被禁用（不连接、不探测）
	MCPConnectionStatusDisabled MCPConnectionStatus = "disabled"
)

// MCPConnection 单个 MCP runtime 的连接状态与工具缓存。
type MCPConnection struct {
	RuntimeID string
	Status    MCPConnectionStatus
	Client    MCPClient
	Tools     []MCPTool // tools/list 缓存（含协议层合成的 read_resource/get_prompt 伪工具）
	Error     error
	UpdatedAt time.Time
}

// MCPConnectionManager MCP 连接权威（T2.5）：per-runtime 连接状态机、超时、隔离、重连、状态推送。
// SkillRuntimeRegistry 的 MCP probe/execute 委托本管理器（共享同一协议实例）；
// registry 未设置 manager（nil）时保持既有直连路径（安全阀，既有测试不受影响）。
type MCPConnectionManager struct {
	mu             sync.Mutex
	conns          map[string]*MCPConnection
	connLocks      map[string]*sync.Mutex // per-runtime 隔离：同 runtime 的 Connect/Disconnect 串行，跨 runtime 不互扰
	mcpHTTP        *MCPHTTPProtocol
	mcpStdio       *MCPStdioProtocol
	logger         *zap.Logger
	connectTimeout time.Duration
	onStatusChange func(runtimeID string, status MCPConnectionStatus)
}

// NewMCPConnectionManager 创建 MCP 连接管理器。协议实例与 SkillRuntimeRegistry 共享（同一指针）。
func NewMCPConnectionManager(mcpHTTP *MCPHTTPProtocol, mcpStdio *MCPStdioProtocol, logger *zap.Logger) *MCPConnectionManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MCPConnectionManager{
		conns:          make(map[string]*MCPConnection),
		connLocks:      make(map[string]*sync.Mutex),
		mcpHTTP:        mcpHTTP,
		mcpStdio:       mcpStdio,
		logger:         logger,
		connectTimeout: 10 * time.Second,
	}
}

// SetConnectTimeout 设置连接超时（默认 10s，<=0 忽略保持原值）。
func (m *MCPConnectionManager) SetConnectTimeout(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d > 0 {
		m.connectTimeout = d
	}
}

// SetStatusChangeHandler 注册状态变更回调（T2.5 状态推送）。回调仅在状态实际变化时触发，幂等不重复推送。
func (m *MCPConnectionManager) SetStatusChangeHandler(h func(runtimeID string, status MCPConnectionStatus)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStatusChange = h
}

// EnsureConnected 缓存感知的连接入口：已 connected 直接返回缓存；否则（首次/failed/pending）走 Connect
// 重连。供 T4.3 激活链路与按需连接使用；探活应直接用 Connect（强制刷新工具）。
func (m *MCPConnectionManager) EnsureConnected(rt *model.SkillRuntime) (*MCPConnection, error) {
	if rt == nil {
		return nil, errors.New("runtime 记录不能为空")
	}
	if conn := m.getConn(rt.ID); conn != nil && conn.Status == MCPConnectionStatusConnected {
		return conn, nil
	}
	return m.Connect(rt)
}

// Connect 建立（或重建）MCP 连接并发现工具（探活入口：每次强制 initialize + tools/list 保持新鲜）。
// 成功置 connected 并缓存工具；失败置 failed 并保留 Error。状态跃迁触发 onStatusChange。
func (m *MCPConnectionManager) Connect(rt *model.SkillRuntime) (*MCPConnection, error) {
	if rt == nil {
		return nil, errors.New("runtime 记录不能为空")
	}
	lock := m.connLock(rt.ID)
	lock.Lock()
	defer lock.Unlock()

	// 禁用：不连接不探测（与 registry loadAll 跳过 disabled 的语义一致）。
	if rt.Status == model.SkillRuntimeStatusDisabled {
		m.setStatus(rt.ID, MCPConnectionStatusDisabled, errors.New("runtime 已禁用"))
		return m.getConn(rt.ID), errors.New("runtime 已禁用")
	}

	m.setStatus(rt.ID, MCPConnectionStatusPending, nil)

	client, err := newMCPClient(rt, m.mcpHTTP, m.mcpStdio)
	if err != nil {
		m.setStatus(rt.ID, MCPConnectionStatusFailed, err)
		return m.getConn(rt.ID), err
	}
	m.storeClient(rt.ID, client, nil)

	ctx, cancel := context.WithTimeout(context.Background(), m.connectTimeout)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		m.setStatus(rt.ID, MCPConnectionStatusFailed, err)
		return m.getConn(rt.ID), err
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		m.setStatus(rt.ID, MCPConnectionStatusFailed, err)
		return m.getConn(rt.ID), err
	}

	m.storeClient(rt.ID, client, tools)
	m.setStatus(rt.ID, MCPConnectionStatusConnected, nil)
	return m.getConn(rt.ID), nil
}

// Disconnect 断开连接：HTTP=Reset endpoint 清会话状态；stdio=客户端 no-op（会话归 Supervisor）。
// 缓存状态置 failed（断开=离线），触发 onStatusChange（registry → degraded）。
func (m *MCPConnectionManager) Disconnect(runtimeID string) {
	lock := m.connLock(runtimeID)
	lock.Lock()
	defer lock.Unlock()
	conn := m.getConn(runtimeID)
	if conn != nil && conn.Client != nil {
		_ = conn.Client.Disconnect()
	}
	m.setStatus(runtimeID, MCPConnectionStatusFailed, errors.New("连接已断开"))
}

// Reconnect 强制重连（Disconnect + Connect）。供外部触发重连与测试使用。
func (m *MCPConnectionManager) Reconnect(rt *model.SkillRuntime) (*MCPConnection, error) {
	if rt == nil {
		return nil, errors.New("runtime 记录不能为空")
	}
	m.Disconnect(rt.ID)
	return m.Connect(rt)
}

// Status 返回 runtime 的连接状态（无记录返回 pending）。
func (m *MCPConnectionManager) Status(runtimeID string) MCPConnectionStatus {
	if conn := m.getConn(runtimeID); conn != nil {
		return conn.Status
	}
	return MCPConnectionStatusPending
}

// Tools 返回 runtime 缓存的工具列表（含伪工具；未连接/未探活返回 nil）。
func (m *MCPConnectionManager) Tools(runtimeID string) []MCPTool {
	if conn := m.getConn(runtimeID); conn != nil {
		return conn.Tools
	}
	return nil
}

// Execute 调用 MCP 工具（转发到对应传输协议，语义与 registry 既有路径一致）：
// - HTTP：从 params 提取 __mcp_headers__ 传 protocol.Execute（协议层过滤内部键 + 拦截伪工具）。
// - stdio：校验会话已注册后 protocol.Execute。
// - sse：返回后置未实现错误。
// 连接状态由探活（Connect）维护，Execute 不改变状态（工具层错误 ≠ 连接断开，避免误标离线）。
func (m *MCPConnectionManager) Execute(rt *model.SkillRuntime, action string, params map[string]interface{}, userID string) (map[string]interface{}, error) {
	if rt == nil {
		return nil, errors.New("runtime 记录不能为空")
	}
	switch rt.Transport {
	case model.SkillRuntimeTransportMCPHTTP:
		if m.mcpHTTP == nil {
			return nil, errors.New("MCP HTTP 协议未初始化")
		}
		var headers map[string]string
		if h, ok := params["__mcp_headers__"].(map[string]string); ok {
			headers = h
		}
		return m.mcpHTTP.Execute(rt.Endpoint, action, params, headers)
	case model.SkillRuntimeTransportMCPStdio:
		if m.mcpStdio == nil {
			return nil, errors.New("MCP stdio 协议未初始化")
		}
		if !m.mcpStdio.IsRegistered(rt.ID) {
			return nil, fmt.Errorf("stdio MCP 会话未注册: %s", rt.ID)
		}
		return m.mcpStdio.Execute(rt.ID, action, params)
	case model.SkillRuntimeTransportMCPSSE:
		return nil, errors.New("MCP SSE 传输后置未实现")
	default:
		return nil, fmt.Errorf("不支持的 transport: %s", rt.Transport)
	}
}

// --- 内部：状态机 + 存储 ---

// connLock 获取（或创建）runtime 专属互斥锁，保证同 runtime 的 Connect/Disconnect 串行。
func (m *MCPConnectionManager) connLock(runtimeID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.connLocks[runtimeID]
	if !ok {
		l = &sync.Mutex{}
		m.connLocks[runtimeID] = l
	}
	return l
}

func (m *MCPConnectionManager) getConn(runtimeID string) *MCPConnection {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conns[runtimeID]
}

// storeClient 在持锁下把 client/tools 挂到连接对象（Connect 中多步写，避免并发读竞态）。
func (m *MCPConnectionManager) storeClient(runtimeID string, client MCPClient, tools []MCPTool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	conn := m.conns[runtimeID]
	if conn == nil {
		conn = &MCPConnection{RuntimeID: runtimeID}
		m.conns[runtimeID] = conn
	}
	conn.Client = client
	conn.Tools = tools
}

// setStatus 更新连接状态（缺失则创建），状态跃迁时推送 onStatusChange。
// 首次（无记录）视为 pending → 目标状态，保证 pending→connected 等初始跃迁也会推送。
func (m *MCPConnectionManager) setStatus(runtimeID string, status MCPConnectionStatus, err error) {
	m.mu.Lock()
	conn := m.conns[runtimeID]
	prev := MCPConnectionStatusPending
	if conn != nil {
		prev = conn.Status
	}
	changed := prev != status
	if conn == nil {
		conn = &MCPConnection{RuntimeID: runtimeID}
		m.conns[runtimeID] = conn
	}
	conn.Status = status
	conn.Error = err
	conn.UpdatedAt = time.Now()
	handler := m.onStatusChange
	m.mu.Unlock()

	if changed && handler != nil {
		handler(runtimeID, status)
	}
}
