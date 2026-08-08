package service

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// startFakeStdioServer 启动一个内存 stdio MCP server：
//   - 从 reqR 读取协议写出的请求（reqR 对应 session.stdin 的对端）
//   - 向 respW 写出响应（respW 对应 session.stdout 的对端）
//
// 返回停止函数。协议侧 RegisterSession(id, reqW, respR)。
func startFakeStdioServer(t *testing.T, reqR io.Reader, respW io.Writer) func() {
	t.Helper()
	var mu sync.Mutex
	closed := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reqR)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var req map[string]interface{}
			if err := json.Unmarshal(line, &req); err != nil {
				continue
			}
			id := req["id"]
			method, _ := req["method"].(string)
			// notification（无 id）不响应
			if id == nil {
				continue
			}
			var resp map[string]interface{}
			switch method {
			case "initialize":
				resp = map[string]interface{}{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]interface{}{
						"protocolVersion": "2024-11-05",
						"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
						"serverInfo":      map[string]interface{}{"name": "fake", "version": "1.0.0"},
					},
				}
			case "tools/list":
				resp = map[string]interface{}{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]interface{}{
						"tools": []interface{}{
							map[string]interface{}{"name": "echo", "description": "回显"},
							map[string]interface{}{"name": "ping", "description": "pong"},
						},
					},
				}
			case "tools/call":
				params, _ := req["params"].(map[string]interface{})
				name, _ := params["name"].(string)
				if name == "echo" {
					resp = map[string]interface{}{
						"jsonrpc": "2.0", "id": id,
						"result": map[string]interface{}{
							"content": []interface{}{
								map[string]interface{}{"type": "text", "text": "hi-stdio"},
							},
						},
					}
				} else {
					resp = map[string]interface{}{
						"jsonrpc": "2.0", "id": id,
						"result": map[string]interface{}{"isError": true,
							"content": []interface{}{map[string]interface{}{"type": "text", "text": "unknown"}}},
					}
				}
			default:
				resp = map[string]interface{}{"jsonrpc": "2.0", "id": id,
					"error": map[string]interface{}{"code": -32601, "message": "method not found"}}
			}
			b, _ := json.Marshal(resp)
			mu.Lock()
			if closed {
				mu.Unlock()
				return
			}
			respW.Write(append(b, '\n'))
			mu.Unlock()
		}
	}()
	return func() {
		mu.Lock()
		closed = true
		mu.Unlock()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
}

// newPipeSession 建立一对 io.Pipe 模拟子进程 stdin/stdout，返回 protocol 写入端（reqW）、
// protocol 读取端（respR）与停止函数。
func newPipeSession(t *testing.T) (io.WriteCloser, io.Reader, func()) {
	t.Helper()
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	srvStop := startFakeStdioServer(t, reqR, respW)
	return reqW, respR, func() {
		srvStop()
		reqR.Close()
		reqW.Close()
		respR.Close()
		respW.Close()
	}
}

func TestMCPStdioProtocol_InitializeListExecute(t *testing.T) {
	reqW, respR, stop := newPipeSession(t)
	defer stop()

	p := NewMCPStdioProtocol(nil)
	p.RegisterSession("rt1", reqW, respR)
	defer p.UnregisterSession("rt1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.Initialize(ctx, "rt1"); err != nil {
		t.Fatalf("Initialize 失败: %v", err)
	}

	tools, err := p.ListTools(ctx, "rt1")
	if err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" || tools[1].Name != "ping" {
		t.Fatalf("工具列表不符: %+v", tools)
	}

	// tools/list 第二次应命中内存缓存（force=true 仍刷新；这里验证可重复调用）
	if _, err := p.ListTools(ctx, "rt1"); err != nil {
		t.Fatalf("ListTools 二次调用失败: %v", err)
	}

	result, err := p.Execute("rt1", "echo", map[string]interface{}{"message": "hi"})
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		t.Fatalf("响应无 content: %+v", result)
	}
	first, _ := content[0].(map[string]interface{})
	if first["text"] != "hi-stdio" {
		t.Fatalf("echo 内容不符: %+v", first)
	}
}

func TestMCPStdioProtocol_ToolErrorMapped(t *testing.T) {
	reqW, respR, stop := newPipeSession(t)
	defer stop()

	p := NewMCPStdioProtocol(nil)
	p.RegisterSession("rt2", reqW, respR)
	defer p.UnregisterSession("rt2")

	// 未知工具名 -> 服务器返回 isError:true -> Execute 返回 error
	_, err := p.Execute("rt2", "nope", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "MCP 工具错误") {
		t.Fatalf("期望 MCP 工具错误，得到: %v", err)
	}
}

func TestMCPStdioProtocol_RequestTimeout(t *testing.T) {
	// 服务端读取后故意不响应，验证 ctx 超时
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	defer reqR.Close()
	defer reqW.Close()
	defer respR.Close()
	defer respW.Close()

	// drain reader：读掉请求但不回响应；respW 保持打开使 respR.Read 阻塞直至超时
	go io.Copy(io.Discard, reqR)

	p := NewMCPStdioProtocol(nil)
	p.RegisterSession("rt3", reqW, respR)
	defer p.UnregisterSession("rt3")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := p.ListTools(ctx, "rt3")
	if err == nil {
		t.Fatal("期望超时错误，得到 nil")
	}
}

func TestMCPStdioProtocol_UnregisterClearsSession(t *testing.T) {
	reqW, respR, stop := newPipeSession(t)
	defer stop()

	p := NewMCPStdioProtocol(nil)
	p.RegisterSession("rt4", reqW, respR)
	if !p.IsRegistered("rt4") {
		t.Fatal("注册后应可查到会话")
	}
	p.UnregisterSession("rt4")
	if p.IsRegistered("rt4") {
		t.Fatal("注销后不应查到会话")
	}
	if _, err := p.ListTools(context.Background(), "rt4"); err == nil {
		t.Fatal("注销后调用应报错")
	}
}

// TestMCPStdioProtocol_ProbeStdioMissingInterpreter 缺失解释器时 ProbeStdio 返回可读错误（D3），
// 不应进入 spawn 流程。
func TestMCPStdioProtocol_ProbeStdioMissingInterpreter(t *testing.T) {
	p := NewMCPStdioProtocol(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	const missing = "definitely-not-a-real-interpreter-xyz123"
	_, err := p.ProbeStdio(ctx, missing, nil, nil, "")
	if err == nil {
		t.Fatal("期望返回错误，实际为 nil")
	}
	ime, ok := err.(*InterpreterMissingError)
	if !ok {
		t.Fatalf("期望 *InterpreterMissingError，实际 %T: %v", err, err)
	}
	if ime.Command != missing {
		t.Errorf("Command = %q，期望 %q", ime.Command, missing)
	}
	if ime.Hint == "" {
		t.Error("Hint 不应为空")
	}
}

// TestMCPStdioProtocol_ProbeStdioEmptyCommand 空命令应直接报错而非 spawn。
func TestMCPStdioProtocol_ProbeStdioEmptyCommand(t *testing.T) {
	p := NewMCPStdioProtocol(nil)
	if _, err := p.ProbeStdio(context.Background(), "", nil, nil, ""); err == nil {
		t.Fatal("空 command 应返回错误")
	}
}

// TestMCPStdioProtocol_InitializeNegotiatesVersion 验证 stdio initialize 请求协议版本升至
// 2025-06-18、声明 roots 能力；server 回旧版（2024-11-05）时协商回落到 server 版本且仍可 list。
func TestMCPStdioProtocol_InitializeNegotiatesVersion(t *testing.T) {
	capture := &mcpInitCapture{}
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reqR)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var req map[string]interface{}
			if err := json.Unmarshal(line, &req); err != nil {
				continue
			}
			id := req["id"]
			if id == nil {
				continue // notification
			}
			method, _ := req["method"].(string)
			var resp map[string]interface{}
			switch method {
			case "initialize":
				capture.set(req)
				resp = map[string]interface{}{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]interface{}{
						"protocolVersion": "2024-11-05",
						"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
						"serverInfo":      map[string]interface{}{"name": "fake", "version": "1.0.0"},
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
			b, _ := json.Marshal(resp)
			respW.Write(append(b, '\n'))
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	defer func() {
		close(stop)
		reqR.Close()
		reqW.Close()
		respR.Close()
		respW.Close()
		<-done
	}()

	p := NewMCPStdioProtocol(nil)
	p.RegisterSession("rt-ver", reqW, respR)
	defer p.UnregisterSession("rt-ver")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Initialize(ctx, "rt-ver"); err != nil {
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
	// server 回旧版 -> 协商回落并采用 server 版本
	s, err := p.session("rt-ver")
	if err != nil {
		t.Fatalf("session 取回失败: %v", err)
	}
	if s.protocolVersion != "2024-11-05" {
		t.Errorf("协商版本 = %q, 期望回退到 2024-11-05", s.protocolVersion)
	}
	// 回旧版后仍可 tools/list
	tools, err := p.ListTools(ctx, "rt-ver")
	if err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("工具列表不符: %+v", tools)
	}
}

// TestMCPStdioProtocol_ResourcesPseudoTool 验证 stdio server 声明 resources capability 时，
// ListTools 合成 read_resource 伪工具，Execute(read_resource) 拦截重映射到 resources/read（M5）。
func TestMCPStdioProtocol_ResourcesPseudoTool(t *testing.T) {
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reqR)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var req map[string]interface{}
			if err := json.Unmarshal(line, &req); err != nil {
				continue
			}
			id := req["id"]
			if id == nil {
				continue // notification
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
						"serverInfo": map[string]interface{}{"name": "res-stdio", "version": "1.0.0"},
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
							map[string]interface{}{"uri": "file:///x.txt", "name": "X"},
						},
					},
				}
			case "resources/read":
				resp = map[string]interface{}{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]interface{}{
						"contents": []interface{}{
							map[string]interface{}{"uri": "file:///x.txt", "text": "stdio-resource"},
						},
					},
				}
			default:
				resp = map[string]interface{}{"jsonrpc": "2.0", "id": id,
					"error": map[string]interface{}{"code": -32601, "message": "method not found"}}
			}
			b, _ := json.Marshal(resp)
			respW.Write(append(b, '\n'))
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	defer func() {
		close(stop)
		reqR.Close()
		reqW.Close()
		respR.Close()
		respW.Close()
		<-done
	}()

	p := NewMCPStdioProtocol(nil)
	p.RegisterSession("rt-res", reqW, respR)
	defer p.UnregisterSession("rt-res")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := p.ListTools(ctx, "rt-res")
	if err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	names := map[string]bool{}
	for _, t := range tools {
		names[t.Name] = true
	}
	if !names["echo"] || !names[mcpPseudoToolReadResource] {
		t.Fatalf("期望 echo + read_resource，实际 %+v", tools)
	}

	// Execute(read_resource) 拦截 -> resources/read -> 资源内容
	result, err := p.Execute("rt-res", mcpPseudoToolReadResource, map[string]interface{}{"uri": "file:///x.txt"})
	if err != nil {
		t.Fatalf("Execute(read_resource) 失败: %v", err)
	}
	contents, _ := result["contents"].([]interface{})
	if len(contents) == 0 {
		t.Fatalf("期望 contents 非空: %+v", result)
	}
	first, _ := contents[0].(map[string]interface{})
	if first["text"] != "stdio-resource" {
		t.Errorf("资源内容 = %v, 期望 stdio-resource", first["text"])
	}
}
