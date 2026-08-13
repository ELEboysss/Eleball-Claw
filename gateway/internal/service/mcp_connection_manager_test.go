package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connEvent 捕获 onStatusChange 推送的事件（handler goroutine 写、测试 goroutine 读）。
type connEvent struct {
	runtimeID string
	status    MCPConnectionStatus
}

type connEventCapture struct {
	mu     sync.Mutex
	events []connEvent
}

func (c *connEventCapture) append(runtimeID string, status MCPConnectionStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, connEvent{runtimeID: runtimeID, status: status})
}

func (c *connEventCapture) statuses() []MCPConnectionStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]MCPConnectionStatus, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, e.status)
	}
	return out
}

func (c *connEventCapture) has(s MCPConnectionStatus) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.events {
		if e.status == s {
			return true
		}
	}
	return false
}

// newHTTPTestRT 构造指向 srv 的 mcp_http SkillRuntime（Name 供 deriveKind/Category 等使用）。
func newHTTPTestRT(id, srvURL string) *model.SkillRuntime {
	return &model.SkillRuntime{
		ID:         id,
		Name:       id,
		Transport:  model.SkillRuntimeTransportMCPHTTP,
		Deployment: model.SkillRuntimeDeploymentExternal,
		Endpoint:   srvURL,
		Status:     model.SkillRuntimeStatusInstalled,
		AutoSKU:    false,
	}
}

// TestMCPConnectionManager_HTTPConnect 验证 HTTP 传输经 manager 建连：
// 连接成功、工具已发现（含协议层合成）、二次 EnsureConnected 命中缓存（同一连接对象）。
func TestMCPConnectionManager_HTTPConnect(t *testing.T) {
	srv, _ := mcpTestServer(t, mcpProtocolVersion)
	defer srv.Close()

	mgr := NewMCPConnectionManager(NewMCPHTTPProtocol(nil), NewMCPStdioProtocol(nil), nil)
	rt := newHTTPTestRT("rt-http", srv.URL)

	conn, err := mgr.EnsureConnected(rt)
	require.NoError(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, MCPConnectionStatusConnected, conn.Status)
	require.Len(t, conn.Tools, 1)
	assert.Equal(t, "echo", conn.Tools[0].Name)

	// 二次 EnsureConnected 命中缓存（同一连接对象，不再重复 initialize/tools/list）
	cached, err := mgr.EnsureConnected(rt)
	require.NoError(t, err)
	assert.Same(t, conn, cached)

	// Status/Tools 读取入口一致
	assert.Equal(t, MCPConnectionStatusConnected, mgr.Status(rt.ID))
	assert.Len(t, mgr.Tools(rt.ID), 1)
}

// TestMCPConnectionManager_HTTPConnectFailure 验证连接/工具发现失败 → failed + Error。
func TestMCPConnectionManager_HTTPConnectFailure(t *testing.T) {
	srv, _ := mcpTestServer(t, mcpProtocolVersion)
	url := srv.URL
	srv.Close() // 关闭后连接必失败

	mgr := NewMCPConnectionManager(NewMCPHTTPProtocol(nil), NewMCPStdioProtocol(nil), nil)
	rt := newHTTPTestRT("rt-down", url)

	conn, err := mgr.EnsureConnected(rt)
	require.Error(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, MCPConnectionStatusFailed, conn.Status)
	assert.Error(t, conn.Error)
	assert.Equal(t, MCPConnectionStatusFailed, mgr.Status(rt.ID))
}

// TestMCPConnectionManager_Reconnect 验证同 endpoint 下「先失败后恢复」可重连：
// 首连失败 → 服务恢复 → EnsureConnected/Reconnect 建连成功。
func TestMCPConnectionManager_Reconnect(t *testing.T) {
	var mu sync.Mutex
	down := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		isDown := down
		mu.Unlock()
		if isDown {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		id := req["id"]
		if id == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		method, _ := req["method"].(string)
		var resp map[string]interface{}
		switch method {
		case "initialize":
			resp = map[string]interface{}{"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"protocolVersion": mcpProtocolVersion,
					"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
					"serverInfo":      map[string]interface{}{"name": "flaky", "version": "1.0.0"},
				}}
		case "tools/list":
			resp = map[string]interface{}{"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"tools": []interface{}{map[string]interface{}{"name": "echo", "description": "回显"}},
				}}
		default:
			resp = map[string]interface{}{"jsonrpc": "2.0", "id": id,
				"error": map[string]interface{}{"code": -32601, "message": "method not found"}}
		}
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(resp)
		w.Write(b)
	}))
	defer srv.Close()

	mgr := NewMCPConnectionManager(NewMCPHTTPProtocol(nil), NewMCPStdioProtocol(nil), nil)
	rt := newHTTPTestRT("rt-flaky", srv.URL)

	// 服务在维护中：首连失败
	conn, err := mgr.EnsureConnected(rt)
	require.Error(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, MCPConnectionStatusFailed, conn.Status)

	// 服务恢复：Reconnect 成功
	mu.Lock()
	down = false
	mu.Unlock()
	re, err := mgr.Reconnect(rt)
	require.NoError(t, err)
	assert.Equal(t, MCPConnectionStatusConnected, re.Status)
	require.Len(t, re.Tools, 1)
	assert.Equal(t, "echo", re.Tools[0].Name)
}

// TestMCPConnectionManager_OnStatusChange 验证状态跃迁触发推送（含首连 pending→connected）。
func TestMCPConnectionManager_OnStatusChange(t *testing.T) {
	srv, _ := mcpTestServer(t, mcpProtocolVersion)
	defer srv.Close()

	capture := &connEventCapture{}
	mgr := NewMCPConnectionManager(NewMCPHTTPProtocol(nil), NewMCPStdioProtocol(nil), nil)
	mgr.SetStatusChangeHandler(capture.append)

	rt := newHTTPTestRT("rt-push", srv.URL)
	_, err := mgr.EnsureConnected(rt)
	require.NoError(t, err)
	assert.True(t, capture.has(MCPConnectionStatusConnected), "推送应含 connected，实际 %v", capture.statuses())

	// 幂等：重复 EnsureConnected 命中缓存，不重复推送 connected
	_, err = mgr.EnsureConnected(rt)
	require.NoError(t, err)
	conns := 0
	for _, s := range capture.statuses() {
		if s == MCPConnectionStatusConnected {
			conns++
		}
	}
	assert.Equal(t, 1, conns, "connected 应只推送一次，实际 %v", capture.statuses())
}

// TestMCPConnectionManager_StdioConnect 验证 stdio 传输经 manager 建连（会话已由 Supervisor 注册）。
func TestMCPConnectionManager_StdioConnect(t *testing.T) {
	reqW, respR, stop := newPipeSession(t)
	defer stop()

	stdioProto := NewMCPStdioProtocol(nil)
	stdioProto.RegisterSession("rt-stdio", reqW, respR)
	defer stdioProto.UnregisterSession("rt-stdio")

	mgr := NewMCPConnectionManager(NewMCPHTTPProtocol(nil), stdioProto, nil)
	rt := &model.SkillRuntime{
		ID:         "rt-stdio",
		Name:       "rt-stdio",
		Transport:  model.SkillRuntimeTransportMCPStdio,
		Deployment: model.SkillRuntimeDeploymentProcess,
		Status:     model.SkillRuntimeStatusInstalled,
	}

	conn, err := mgr.EnsureConnected(rt)
	require.NoError(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, MCPConnectionStatusConnected, conn.Status)
	require.Len(t, conn.Tools, 2)
	assert.Equal(t, "echo", conn.Tools[0].Name)
	assert.Equal(t, "ping", conn.Tools[1].Name)

	// Execute 走 stdio 协议（工具层错误不改状态）
	res, err := mgr.Execute(rt, "echo", map[string]interface{}{"message": "hi"}, "u1")
	require.NoError(t, err)
	content, _ := res["content"].([]interface{})
	require.NotEmpty(t, content)
	assert.Equal(t, MCPConnectionStatusConnected, mgr.Status(rt.ID))
}

// TestMCPConnectionManager_StdioUnregistered 验证无会话的 stdio runtime → failed（离线判定）。
func TestMCPConnectionManager_StdioUnregistered(t *testing.T) {
	mgr := NewMCPConnectionManager(NewMCPHTTPProtocol(nil), NewMCPStdioProtocol(nil), nil)
	rt := &model.SkillRuntime{
		ID:         "rt-no-session",
		Name:       "rt-no-session",
		Transport:  model.SkillRuntimeTransportMCPStdio,
		Deployment: model.SkillRuntimeDeploymentProcess,
		Status:     model.SkillRuntimeStatusInstalled,
	}
	conn, err := mgr.EnsureConnected(rt)
	require.Error(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, MCPConnectionStatusFailed, conn.Status)
}

// TestMCPConnectionManager_SSENotImplemented 验证 SSE 传输返回后置未实现错误（不阻塞主链路）。
func TestMCPConnectionManager_SSENotImplemented(t *testing.T) {
	mgr := NewMCPConnectionManager(NewMCPHTTPProtocol(nil), NewMCPStdioProtocol(nil), nil)
	rt := &model.SkillRuntime{
		ID:         "rt-sse",
		Name:       "rt-sse",
		Transport:  model.SkillRuntimeTransportMCPSSE,
		Deployment: model.SkillRuntimeDeploymentExternal,
		Status:     model.SkillRuntimeStatusInstalled,
	}
	conn, err := mgr.EnsureConnected(rt)
	require.Error(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, MCPConnectionStatusFailed, conn.Status)
	assert.Contains(t, err.Error(), "SSE")
}

// TestMCPToolName 验证 mcp__{server}__{tool} 命名与 64 字符 sanitize。
func TestMCPToolName(t *testing.T) {
	name := mcpToolName("demo-pkg-mcp-search", "web_search")
	assert.Equal(t, "mcp__demo-pkg-mcp-search__web_search", name)

	// 非法字符 sanitize：server/tool 中的非 [a-zA-Z0-9_-] 转 _
	assert.Equal(t, "mcp__svr__t_ool", mcpToolName("svr", "t/ool"))
	assert.Equal(t, "mcp__svr__t_ool", mcpToolName("svr", "t.ool"))

	// 超长截断到 64 字符
	long := mcpToolName("very-long-server-"+string(make([]byte, 40)), "tool")
	assert.Len(t, long, 64)
}

// TestMCPToolRef 验证 MCP SKU 识别：(server, tool) 引用。
func TestMCPToolRef(t *testing.T) {
	l := NewAgentToolLoader(nil, nil)

	// 派生 MCP SKU：package_derived=mcp，module=rt.ID，Actions=[{Name:tool}]
	mf := &model.ToolManifest{
		ID:    "demo-pkg-mcp-search-web_search",
		Driver: model.ToolDriverType("demo-pkg-mcp-search"),
		Actions: []model.ToolAction{{Name: "web_search", Description: "搜索"}},
		Metadata: map[string]string{"module": "demo-pkg-mcp-search", "package_derived": "mcp"},
	}
	server, tool, ok := l.mcpToolRef(mf)
	assert.True(t, ok)
	assert.Equal(t, "demo-pkg-mcp-search", server)
	assert.Equal(t, "web_search", tool)

	// 手写 MCP SKU：driver=mcp + metadata.module
	mf2 := &model.ToolManifest{
		ID:     "hand-mcp",
		Driver: model.ToolDriverMCP,
		Actions: []model.ToolAction{{Name: "do_thing", Description: "d"}},
		Metadata: map[string]string{"module": "hand-mcp-rt"},
	}
	server2, tool2, ok2 := l.mcpToolRef(mf2)
	assert.True(t, ok2)
	assert.Equal(t, "hand-mcp-rt", server2)
	assert.Equal(t, "do_thing", tool2)

	// 非 MCP（execute 派生 SKU）不改名
	mf3 := &model.ToolManifest{
		Driver:   model.ToolDriverType("demo-pkg-calc"),
		Actions:  []model.ToolAction{{Name: "calc", Description: "d"}},
		Metadata: map[string]string{"module": "demo-pkg-calc", "package_derived": "tool"},
	}
	_, _, ok3 := l.mcpToolRef(mf3)
	assert.False(t, ok3)

	// MCP 但缺 Actions（无工具名）→ 不改名
	mf4 := &model.ToolManifest{
		Driver:   model.ToolDriverMCP,
		Metadata: map[string]string{"module": "mcp-rt"},
	}
	_, _, ok4 := l.mcpToolRef(mf4)
	assert.False(t, ok4)
}

// TestRegistry_ProbeDelegatesToManager 验证注入 manager 后，registry ForceProbe 经 manager 探活
// 并落 active 状态（caps 含工具）；onStatusChange 已先行推送。
func TestRegistry_ProbeDelegatesToManager(t *testing.T) {
	srv, _ := mcpTestServer(t, mcpProtocolVersion)
	defer srv.Close()

	httpProto := NewMCPHTTPProtocol(nil)
	mgr := NewMCPConnectionManager(httpProto, NewMCPStdioProtocol(nil), nil)
	reg := NewSkillRuntimeRegistry(nil)
	reg.SetMCPHTTPProtocol(httpProto)
	reg.SetMCPConnectionManager(mgr)

	rt := newHTTPTestRT("rt-reg", srv.URL)
	require.NoError(t, reg.Register(rt))

	// ForceProbe 触发 manager.Connect（强制 initialize + tools/list）→ active
	st := reg.ForceProbe("rt-reg")
	require.NotNil(t, st)
	assert.Equal(t, model.SkillRuntimeStatusActive, st.Status)
	assert.True(t, st.Online)
	require.Contains(t, st.Capabilities, "echo")

	// 断连后 ForceProbe → degraded（状态推送已落库）
	srv.Close()
	st2 := reg.ForceProbe("rt-reg")
	require.NotNil(t, st2)
	assert.Equal(t, model.SkillRuntimeStatusDegraded, st2.Status)
	assert.False(t, st2.Online)
}

// TestRegistry_ProbeNilManager 验证未注入 manager 时保持既有直连路径（安全阀，等价既有行为）。
func TestRegistry_ProbeNilManager(t *testing.T) {
	srv, _ := mcpTestServer(t, mcpProtocolVersion)
	defer srv.Close()

	reg := NewSkillRuntimeRegistry(nil)
	rt := newHTTPTestRT("rt-direct", srv.URL)
	require.NoError(t, reg.Register(rt))

	st := reg.ForceProbe("rt-direct")
	require.NotNil(t, st)
	assert.Equal(t, model.SkillRuntimeStatusActive, st.Status)
	assert.True(t, st.Online)
}
