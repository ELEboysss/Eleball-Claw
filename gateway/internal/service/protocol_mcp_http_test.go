package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// mcpInitCapture 线程安全地捕获 initialize 请求（handler goroutine 写、测试 goroutine 读）。
type mcpInitCapture struct {
	mu  sync.Mutex
	req map[string]interface{}
}

func (c *mcpInitCapture) set(req map[string]interface{}) {
	c.mu.Lock()
	c.req = req
	c.mu.Unlock()
}

func (c *mcpInitCapture) get() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.req
}

// mcpTestServer 启动一个最小 MCP HTTP 测试服务端：按 serverVersion 回 initialize 结果，
// 吞掉 notifications/initialized（回 200 空体），并捕获收到的 initialize 请求。
// 返回服务端与捕获器（无需捕获时忽略第二个返回值）。
func mcpTestServer(t *testing.T, serverVersion string) (*httptest.Server, *mcpInitCapture) {
	t.Helper()
	capture := &mcpInitCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		id := req["id"]
		if id == nil {
			// notification：回 200 空体，客户端 do() 解码失败被 Initialize 忽略
			w.WriteHeader(http.StatusOK)
			return
		}
		method, _ := req["method"].(string)
		var resp map[string]interface{}
		switch method {
		case "initialize":
			capture.set(req)
			resp = map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"protocolVersion": serverVersion,
					"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
					"serverInfo":      map[string]interface{}{"name": "test", "version": "1.0.0"},
				},
			}
		case "tools/list":
			resp = map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"tools": []interface{}{
						map[string]interface{}{"name": "echo", "description": "回显"},
					},
				},
			}
		default:
			resp = map[string]interface{}{"jsonrpc": "2.0", "id": id,
				"error": map[string]interface{}{"code": -32601, "message": "method not found"}}
		}
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(resp)
		w.Write(b)
	}))
	return srv, capture
}

// TestMCPHTTPProtocol_InitializeSendsNewVersionAndCapabilities 验证 initialize 请求
// 协议版本升至 2025-06-18 并声明 roots 能力（listChanged:false）。
func TestMCPHTTPProtocol_InitializeSendsNewVersionAndCapabilities(t *testing.T) {
	srv, capture := mcpTestServer(t, mcpProtocolVersion)
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Initialize(ctx, srv.URL, nil); err != nil {
		t.Fatalf("Initialize 失败: %v", err)
	}
	// captured 是完整 JSON-RPC 请求，protocolVersion/capabilities 在 params 内
	captured := capture.get()
	params, ok := captured["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("initialize 请求缺 params: %+v", captured)
	}
	if got := params["protocolVersion"]; got != mcpProtocolVersion {
		t.Errorf("protocolVersion = %v, 期望 %s", got, mcpProtocolVersion)
	}
	caps, ok := params["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilities 缺失或类型错误: %T", params["capabilities"])
	}
	roots, ok := caps["roots"].(map[string]interface{})
	if !ok || roots["listChanged"] != false {
		t.Errorf("capabilities.roots.listChanged 期望 false, caps=%+v", caps)
	}
	if got := p.negotiated[srv.URL]; got != mcpProtocolVersion {
		t.Errorf("协商版本 = %q, 期望 %s", got, mcpProtocolVersion)
	}
}

// TestMCPHTTPProtocol_InitializeAcceptsOldServerVersion 验证 server 回退旧版本时
// 仍可握手并采用 server 版本（firecrawl 回 2024-11-05 场景），且后续 tools/list 正常。
func TestMCPHTTPProtocol_InitializeAcceptsOldServerVersion(t *testing.T) {
	srv, _ := mcpTestServer(t, "2024-11-05")
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Initialize(ctx, srv.URL, nil); err != nil {
		t.Fatalf("对回旧版 server Initialize 应成功, 得到: %v", err)
	}
	if got := p.negotiated[srv.URL]; got != "2024-11-05" {
		t.Errorf("协商版本 = %q, 期望回退到 2024-11-05", got)
	}
	tools, err := p.ListTools(ctx, srv.URL, nil)
	if err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("工具列表不符: %+v", tools)
	}
}

// TestMCPHTTPProtocol_InitializeAcceptsNewerServerVersion 验证 server 回更新版本时
// 也不报错，并采用 server 回的版本。
func TestMCPHTTPProtocol_InitializeAcceptsNewerServerVersion(t *testing.T) {
	srv, _ := mcpTestServer(t, "2025-11-25")
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Initialize(ctx, srv.URL, nil); err != nil {
		t.Fatalf("对回更新版 server Initialize 应成功, 得到: %v", err)
	}
	if got := p.negotiated[srv.URL]; got != "2025-11-25" {
		t.Errorf("协商版本 = %q, 期望采用 server 回的 2025-11-25", got)
	}
}

// mcpSSEServer 启动一个返回 SSE 流的最小 MCP 测试服务端。
// onData 可在返回主响应前注入额外 SSE 帧（如通知）。sessionID 非空时在响应头回送。
// 返回 server 与按 method 记录收到的 Mcp-Session-Id 的捕获器。
func mcpSSEServer(t *testing.T, sessionID string, onData func(w http.ResponseWriter)) (*httptest.Server, map[string]string) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]string{} // method -> 收到的 Mcp-Session-Id
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		method, _ := req["method"].(string)
		id := req["id"]
		mu.Lock()
		seen[method] = r.Header.Get("Mcp-Session-Id")
		mu.Unlock()
		if id == nil {
			// notification：回 200 空体（SSE 空），调用方不解析
			w.Header().Set("Content-Type", "text/event-stream")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if sessionID != "" {
			w.Header().Set("Mcp-Session-Id", sessionID)
		}
		if onData != nil {
			onData(w)
		}
		var resp map[string]interface{}
		switch method {
		case "initialize":
			resp = map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": map[string]interface{}{
				"protocolVersion": mcpProtocolVersion,
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":      map[string]interface{}{"name": "sse", "version": "1.0.0"},
			}}
		case "tools/list":
			resp = map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": map[string]interface{}{
				"tools": []interface{}{map[string]interface{}{"name": "sse-tool", "description": "SSE 工具"}},
			}}
		default:
			resp = map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": map[string]interface{}{"ok": true}}
		}
		b, _ := json.Marshal(resp)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}))
	return srv, seen
}

// TestMCPHTTPProtocol_DoSSEParsesResponse 验证 Content-Type=text/event-stream 时
// do() 走 SSE 解析，从 data: 帧取首个 JSON-RPC 响应。
func TestMCPHTTPProtocol_DoSSEParsesResponse(t *testing.T) {
	srv, _ := mcpSSEServer(t, "", nil)
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Initialize(ctx, srv.URL, nil); err != nil {
		t.Fatalf("Initialize 失败: %v", err)
	}
	tools, err := p.ListTools(ctx, srv.URL, nil)
	if err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "sse-tool" {
		t.Fatalf("SSE tools/list 解析错误: %+v", tools)
	}
}

// TestMCPHTTPProtocol_DoSSESkipsNotifications 验证 SSE 流中通知帧（仅 method）
// 被跳过，返回首个含 result 的响应。
func TestMCPHTTPProtocol_DoSSESkipsNotifications(t *testing.T) {
	srv, _ := mcpSSEServer(t, "", func(w http.ResponseWriter) {
		// 先发一个通知帧（notifications/progress，无 id 无 result）
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":0.5}}\n\n")
	})
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Initialize(ctx, srv.URL, nil); err != nil {
		t.Fatalf("Initialize 失败: %v", err)
	}
	// ListTools 响应前有一个通知帧，应被跳过而非误当响应
	tools, err := p.ListTools(ctx, srv.URL, nil)
	if err != nil {
		t.Fatalf("ListTools 失败（通知帧应被跳过）: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "sse-tool" {
		t.Fatalf("工具解析错误: %+v", tools)
	}
}

// TestMCPHTTPProtocol_DoSSEMultiLineData 验证 SSE 多个 data: 行按 \n 拼接成帧数据
// （JSON 换行处为合法空白，可解析）。
func TestMCPHTTPProtocol_DoSSEMultiLineData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		id := req["id"]
		w.Header().Set("Content-Type", "text/event-stream")
		// JSON 对象跨两行 data:（换行在逗号后，为合法 JSON 空白）
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%v,\n", id)
		fmt.Fprint(w, "data: \"result\":{\"multiline\":true}}\n\n")
	}))
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := p.do(ctx, srv.URL, &mcpRequest{JSONRPC: "2.0", ID: 1, Method: "ping"}, nil)
	if err != nil {
		t.Fatalf("do 失败: %v", err)
	}
	var res struct {
		Multiline bool `json:"multiline"`
	}
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("多行 data 拼接后解析失败: %v (raw=%s)", err, string(resp.Result))
	}
	if !res.Multiline {
		t.Errorf("multiline 期望 true, result=%s", string(resp.Result))
	}
}

// TestMCPHTTPProtocol_SessionIDCapturedAndResent 验证 Mcp-Session-Id 响应头
// 被捕获并存入 sessions，后续请求回送同一 session id。
func TestMCPHTTPProtocol_SessionIDCapturedAndResent(t *testing.T) {
	srv, seen := mcpSSEServer(t, "sess-abc", nil)
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Initialize(ctx, srv.URL, nil); err != nil {
		t.Fatalf("Initialize 失败: %v", err)
	}
	// 首次 initialize 请求时 sessions 尚空，不应携带 session id
	if got := seen["initialize"]; got != "" {
		t.Errorf("首次 initialize 不应携带 Mcp-Session-Id, 得 %q", got)
	}
	// initialize 响应应已捕获 session id
	if got := p.sessions[srv.URL]; got != "sess-abc" {
		t.Errorf("sessions 期望 sess-abc, 得 %q", got)
	}
	// 后续 tools/list 应回送 session id
	if _, err := p.ListTools(ctx, srv.URL, nil); err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	if got := seen["tools/list"]; got != "sess-abc" {
		t.Errorf("tools/list 应回送 Mcp-Session-Id=sess-abc, 得 %q", got)
	}
}

// TestMCPHTTPProtocol_DoNon200SSEExtractsError 验证非 200 且 Content-Type 为 SSE 时
// 从 data: 帧提取 JSON-RPC error 对象返回，而非通用 HTTP 错误串。
func TestMCPHTTPProtocol_DoNon200SSEExtractsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"error\":{\"code\":-32000,\"message\":\"bad request\"}}\n\n")
	}))
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := p.do(ctx, srv.URL, &mcpRequest{JSONRPC: "2.0", ID: 1, Method: "ping"}, nil)
	if err == nil {
		t.Fatalf("期望错误，得 nil")
	}
	var mcpErr *mcpError
	if !errors.As(err, &mcpErr) {
		t.Fatalf("期望 *mcpError，得 %T: %v", err, err)
	}
	if mcpErr.Code != -32000 {
		t.Errorf("error code = %d，期望 -32000", mcpErr.Code)
	}
	if mcpErr.Message != "bad request" {
		t.Errorf("error message = %q，期望 bad request", mcpErr.Message)
	}
}

// TestReadMCPSSEResponse_SizeLimit 验证 SSE 流超过大小上限时返回超限错误
// （防恶意 server 流式放大或仅发通知不响应的无界读取）。
func TestReadMCPSSEResponse_SizeLimit(t *testing.T) {
	// 仅含通知帧的流（无 result/error），永不返回响应；超过小上限应报超限
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"i\":")
		// 数字占位让每帧略大
		sb.WriteString("1234567890")
		sb.WriteString("}}\n\n")
	}
	_, err := readMCPSSEResponse(strings.NewReader(sb.String()), 1024)
	if err == nil {
		t.Fatalf("期望超限错误，得 nil")
	}
	if !strings.Contains(err.Error(), "上限") {
		t.Errorf("错误应提示超限，得: %v", err)
	}
}

// TestReadMCPSSEResponse_NoResponse 验证纯通知流（未超限）返回无响应错误。
func TestReadMCPSSEResponse_NoResponse(t *testing.T) {
	stream := "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n\n"
	_, err := readMCPSSEResponse(strings.NewReader(stream), mcpSSEMaxBytes)
	if err == nil {
		t.Fatalf("期望无响应错误，得 nil")
	}
	if !errors.Is(err, errMCPSSENoResponse) {
		t.Errorf("期望 errMCPSSENoResponse，得: %v", err)
	}
}

// TestMCPHTTPProtocol_EndpointPathRetained 验证 endpoint 含路径（如 /mcp）时 do() 把完整 URL
// 作为 POST 目标--path 不被剥光。server 仅 /mcp 受理 JSON-RPC，根路径返回 404；
// 反证 POST 到根（旧行为剥光 path）会失败，正证保留 path 的 endpoint 能 initialize + list。
func TestMCPHTTPProtocol_EndpointPathRetained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		id := req["id"]
		if id == nil {
			// notification：回 200 空体
			w.WriteHeader(http.StatusOK)
			return
		}
		method, _ := req["method"].(string)
		var resp map[string]interface{}
		switch method {
		case "initialize":
			resp = map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"protocolVersion": mcpProtocolVersion,
					"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
					"serverInfo":      map[string]interface{}{"name": "path-test", "version": "1.0.0"},
				},
			}
		case "tools/list":
			resp = map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"tools": []interface{}{
						map[string]interface{}{"name": "scrape", "description": "抓取"},
					},
				},
			}
		default:
			resp = map[string]interface{}{"jsonrpc": "2.0", "id": id,
				"error": map[string]interface{}{"code": -32601, "message": "method not found"}}
		}
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(resp)
		w.Write(b)
	}))
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint := srv.URL + "/mcp"

	// 反证：POST 到根路径（旧行为剥光 path）会 404 失败
	if err := p.Initialize(ctx, srv.URL, nil); err == nil {
		t.Fatal("根路径应 404 失败（server 仅服务 /mcp），实际成功--path 未被有效利用")
	}

	// 正证：保留 path 的完整 endpoint 能 initialize + list
	if err := p.Initialize(ctx, endpoint, nil); err != nil {
		t.Fatalf("Initialize(/mcp) 失败: %v", err)
	}
	tools, err := p.ListTools(ctx, endpoint, nil)
	if err != nil {
		t.Fatalf("ListTools(/mcp) 失败: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "scrape" {
		t.Fatalf("期望 1 个工具 scrape，实际 %+v", tools)
	}
}

// TestMCPHTTPProtocol_ResourcesPseudoTool 验证 server 声明 resources capability 时，
// ListTools 合成 read_resource 伪工具（InputSchema.uri.enum 列资源 URI），
// Execute(read_resource) 拦截重映射到 resources/read 并返回资源内容（M5）。
func TestMCPHTTPProtocol_ResourcesPseudoTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		id := req["id"]
		if id == nil {
			w.WriteHeader(http.StatusOK) // notification：回 200 空体
			return
		}
		method, _ := req["method"].(string)
		var resp map[string]interface{}
		switch method {
		case "initialize":
			resp = map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"protocolVersion": mcpProtocolVersion,
					"capabilities": map[string]interface{}{
						"tools":     map[string]interface{}{},
						"resources": map[string]interface{}{},
					},
					"serverInfo": map[string]interface{}{"name": "res", "version": "1.0.0"},
				},
			}
		case "tools/list":
			resp = map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"tools": []interface{}{
						map[string]interface{}{"name": "echo", "description": "回显"},
					},
				},
			}
		case "resources/list":
			resp = map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"resources": []interface{}{
						map[string]interface{}{"uri": "file:///a.txt", "name": "A"},
						map[string]interface{}{"uri": "file:///b.txt", "name": "B"},
					},
				},
			}
		case "resources/read":
			resp = map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"contents": []interface{}{
						map[string]interface{}{"uri": "file:///a.txt", "mimeType": "text/plain", "text": "hello-resource"},
					},
				},
			}
		default:
			resp = map[string]interface{}{"jsonrpc": "2.0", "id": id,
				"error": map[string]interface{}{"code": -32601, "message": "method not found"}}
		}
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(resp)
		w.Write(b)
	}))
	defer srv.Close()

	p := NewMCPHTTPProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := p.ListTools(ctx, srv.URL, nil)
	if err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	// echo + read_resource 伪工具
	var pseudo *MCPTool
	names := map[string]bool{}
	for i := range tools {
		names[tools[i].Name] = true
		if tools[i].Name == mcpPseudoToolReadResource {
			pseudo = &tools[i]
		}
	}
	if !names["echo"] || !names[mcpPseudoToolReadResource] {
		t.Fatalf("期望 echo + read_resource，实际 %+v", tools)
	}
	if pseudo == nil {
		t.Fatal("read_resource 伪工具缺失")
	}
	// uri.enum 列出资源 URI
	props, _ := pseudo.InputSchema["properties"].(map[string]interface{})
	uriProp, _ := props["uri"].(map[string]interface{})
	enum, _ := uriProp["enum"].([]string)
	if len(enum) != 2 || enum[0] != "file:///a.txt" || enum[1] != "file:///b.txt" {
		t.Errorf("read_resource uri.enum = %v, 期望 [file:///a.txt file:///b.txt]", enum)
	}

	// Execute(read_resource) 拦截 -> resources/read -> 资源内容
	result, err := p.Execute(srv.URL, mcpPseudoToolReadResource, map[string]interface{}{"uri": "file:///a.txt"}, nil)
	if err != nil {
		t.Fatalf("Execute(read_resource) 失败: %v", err)
	}
	contents, _ := result["contents"].([]interface{})
	if len(contents) == 0 {
		t.Fatalf("期望 contents 非空: %+v", result)
	}
	first, _ := contents[0].(map[string]interface{})
	if first["text"] != "hello-resource" {
		t.Errorf("资源内容 = %v, 期望 hello-resource", first["text"])
	}
}
