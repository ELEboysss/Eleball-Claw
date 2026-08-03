package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// MCPTool MCP 工具描述
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

// MCPHTTPProtocol Streamable HTTP JSON-RPC MCP 协议适配器
// 支持 initialize handshake、tools/list 探活、tools/call 调用。
type MCPHTTPProtocol struct {
	client      *http.Client
	mu          sync.Mutex
	initialized map[string]bool // endpoint -> 是否已 handshake
}

// NewMCPHTTPProtocol 创建 MCP HTTP 协议适配器
func NewMCPHTTPProtocol(client *http.Client) *MCPHTTPProtocol {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &MCPHTTPProtocol{
		client:      client,
		initialized: make(map[string]bool),
	}
}

// mcpRequest JSON-RPC 2.0 请求
type mcpRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id,omitempty"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

// mcpResponse JSON-RPC 2.0 响应
type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *mcpError) Error() string {
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

// do 发送 JSON-RPC 请求
func (p *MCPHTTPProtocol) do(ctx context.Context, endpoint string, req *mcpRequest, headers map[string]string) (*mcpResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("MCP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MCP HTTP %d: %s", resp.StatusCode, string(body))
	}

	var rpcResp mcpResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("MCP 响应解析失败: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}
	return &rpcResp, nil
}

// Initialize 执行 MCP initialize handshake
func (p *MCPHTTPProtocol) Initialize(ctx context.Context, endpoint string, headers map[string]string) error {
	p.mu.Lock()
	if p.initialized[endpoint] {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "eleball-gateway",
				"version": "1.0.0",
			},
		},
	}

	_, err := p.do(ctx, endpoint, req, headers)
	if err != nil {
		return fmt.Errorf("MCP initialize 失败: %w", err)
	}

	// 发送 initialized notification
	notif := &mcpRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		Params:  map[string]interface{}{},
	}
	_, _ = p.do(ctx, endpoint, notif, headers)

	p.mu.Lock()
	p.initialized[endpoint] = true
	p.mu.Unlock()
	return nil
}

// ListTools 获取 MCP 工具列表（用于健康探测与能力发现）
func (p *MCPHTTPProtocol) ListTools(ctx context.Context, endpoint string, headers map[string]string) ([]MCPTool, error) {
	if err := p.Initialize(ctx, endpoint, headers); err != nil {
		return nil, err
	}

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
		Params:  map[string]interface{}{},
	}

	resp, err := p.do(ctx, endpoint, req, headers)
	if err != nil {
		return nil, err
	}

	var result struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP tools/list 解析失败: %w", err)
	}
	return result.Tools, nil
}

// Execute 调用 MCP 工具
func (p *MCPHTTPProtocol) Execute(endpoint, action string, params map[string]interface{}, headers map[string]string) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := p.Initialize(ctx, endpoint, headers); err != nil {
		return nil, err
	}

	// 过滤内部参数
	arguments := make(map[string]interface{})
	for k, v := range params {
		if k == "__mcp_endpoint__" || k == "__mcp_server__" || k == "__mcp_headers__" {
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

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      action,
			"arguments": arguments,
		},
	}

	resp, err := p.do(ctx, endpoint, req, headers)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP tools/call 解析失败: %w", err)
	}

	// 处理 isError
	if isError, ok := result["isError"].(bool); ok && isError {
		content := extractMCPContent(result)
		return nil, fmt.Errorf("MCP 工具错误: %s", content)
	}

	return result, nil
}

// extractMCPContent 从 MCP 结果中提取文本内容
func extractMCPContent(result map[string]interface{}) string {
	if content, ok := result["content"].([]interface{}); ok {
		for _, item := range content {
			if m, ok := item.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					return text
				}
			}
		}
	}
	return "unknown error"
}

// mcpHTTPBaseURL 提取 MCP endpoint 的基础地址
func mcpHTTPBaseURL(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// NewMCPHTTPProtocolFromConfig 从 MCPServerConfig 创建协议实例
func NewMCPHTTPProtocolFromConfig(endpoint string, headers map[string]string) *MCPHTTPProtocol {
	return &MCPHTTPProtocol{
		client:      &http.Client{Timeout: 30 * time.Second},
		initialized: make(map[string]bool),
	}
}

// EnsureInitialized 确保指定 endpoint 已初始化（供外部调用）
func (p *MCPHTTPProtocol) EnsureInitialized(endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return p.Initialize(ctx, endpoint, nil)
}

// IsInitialized 检查 endpoint 是否已初始化
func (p *MCPHTTPProtocol) IsInitialized(endpoint string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.initialized[endpoint]
}

// Reset 重置 endpoint 的初始化状态（用于连接断开后重连）
func (p *MCPHTTPProtocol) Reset(endpoint string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.initialized, endpoint)
}

// ErrMCPNotInitialized MCP 未初始化错误
var ErrMCPNotInitialized = errors.New("MCP endpoint not initialized")
