package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// mcpProtocolVersion 客户端期望协商的 MCP 协议版本。
// 2025-06-18 为 Streamable HTTP 稳定版（openhuman 验证可用）；后续可跟进更新版。
const mcpProtocolVersion = "2025-06-18"

// mcpClientCapabilities 客户端在 initialize 中声明的能力。
// roots：声明客户端可提供 roots 列表（当前不主动 listChanged，仅占位参与协商）。
func mcpClientCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"roots": map[string]interface{}{
			"listChanged": false,
		},
	}
}

// mcpClientInfo 客户端标识
func mcpClientInfo() map[string]interface{} {
	return map[string]interface{}{
		"name":    "eleball-gateway",
		"version": "1.0.0",
	}
}

// mcpInitializeResult initialize 响应结果
type mcpInitializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      map[string]interface{} `json:"serverInfo"`
}

// negotiateMCPVersion 从 initialize 响应中提取 server 协商回的协议版本。
// server 返回的版本若与客户端请求不同（通常为旧版回落，如 firecrawl 回 2024-11-05），
// 记录日志并采用 server 版本；缺失或解析失败时沿用客户端请求版本。返回最终采用的协议版本。
func negotiateMCPVersion(result json.RawMessage, requested string, logger *zap.Logger) string {
	if len(result) == 0 {
		return requested
	}
	var res mcpInitializeResult
	if err := json.Unmarshal(result, &res); err != nil || res.ProtocolVersion == "" {
		return requested
	}
	if res.ProtocolVersion != requested && logger != nil {
		logger.Warn("MCP server 回退协议版本",
			zap.String("requested", requested),
			zap.String("negotiated", res.ProtocolVersion))
	}
	return res.ProtocolVersion
}

// parseMCPServerCapabilities 从 initialize 响应中提取 server 声明的能力（resources/prompts 等）。
// 缺失或解析失败返回 nil。供 ListTools 据此决定是否合成 read_resource/get_prompt 伪工具。
func parseMCPServerCapabilities(result json.RawMessage) map[string]interface{} {
	if len(result) == 0 {
		return nil
	}
	var res mcpInitializeResult
	if err := json.Unmarshal(result, &res); err != nil {
		return nil
	}
	return res.Capabilities
}

// MCPTool MCP 工具描述
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

// MCPResource MCP 资源描述（resources/list 返回项）。
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// MCPPrompt MCP 提示描述（prompts/list 返回项）。
type MCPPrompt struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// 伪工具名：每个支持 resources/prompts 的 server 额外暴露的两个通用工具。
// 纳入 tools/list 视图 -> DeriveSKUs 各派生 1 SKU；Execute 拦截其名字重映射到 resources/read|prompts/get。
const (
	mcpPseudoToolReadResource = "read_resource"
	mcpPseudoToolGetPrompt    = "get_prompt"
)

// mcpReadResourcePseudoTool 构造 read_resource 伪工具：调用 resources/read，入参 uri。
// enum 列出探活时 resources/list 缓存的资源 URI，便于 LLM 选用与 UI 展示。
// 已含资源时把清单附进 description，供不支持 enum 的调用方也能读到。
func mcpReadResourcePseudoTool(resources []MCPResource) MCPTool {
	desc := "读取该 MCP server 暴露的资源（resources/read）。"
	uriProp := map[string]interface{}{
		"type":        "string",
		"description": "要读取的资源 URI",
	}
	if len(resources) > 0 {
		uris := make([]string, 0, len(resources))
		var sb strings.Builder
		sb.WriteString(desc)
		sb.WriteString(" 可读资源：")
		for i, r := range resources {
			uris = append(uris, r.URI)
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(r.URI)
			if r.Name != "" && r.Name != r.URI {
				sb.WriteString("(")
				sb.WriteString(r.Name)
				sb.WriteString(")")
			}
		}
		uriProp["enum"] = uris
		desc = sb.String()
	}
	return MCPTool{
		Name:        mcpPseudoToolReadResource,
		Description: desc,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"uri": uriProp,
			},
			"required": []string{"uri"},
		},
	}
}

// mcpGetPromptPseudoTool 构造 get_prompt 伪工具：调用 prompts/get，入参 name（+可选 arguments）。
// enum 列出探活时 prompts/list 缓存的提示名。
func mcpGetPromptPseudoTool(prompts []MCPPrompt) MCPTool {
	desc := "获取该 MCP server 暴露的提示模板（prompts/get）。"
	nameProp := map[string]interface{}{
		"type":        "string",
		"description": "要获取的提示名",
	}
	if len(prompts) > 0 {
		names := make([]string, 0, len(prompts))
		var sb strings.Builder
		sb.WriteString(desc)
		sb.WriteString(" 可用提示：")
		for i, pr := range prompts {
			names = append(names, pr.Name)
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(pr.Name)
		}
		nameProp["enum"] = names
		desc = sb.String()
	}
	return MCPTool{
		Name:        mcpPseudoToolGetPrompt,
		Description: desc,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": nameProp,
				"arguments": map[string]interface{}{
					"type":        "object",
					"description": "提示参数（按提示定义）",
				},
			},
			"required": []string{"name"},
		},
	}
}

// MCPHTTPProtocol Streamable HTTP JSON-RPC MCP 协议适配器
// 支持 initialize handshake、tools/list 探活、tools/call 调用。
type MCPHTTPProtocol struct {
	client       *http.Client
	mu           sync.Mutex
	initialized  map[string]bool                   // endpoint -> 是否已 handshake
	negotiated   map[string]string                 // endpoint -> 协商回的协议版本（server 可能回退旧版）
	sessions     map[string]string                 // endpoint -> Mcp-Session-Id（Streamable HTTP 会话，回送后续请求）
	capabilities map[string]map[string]interface{} // endpoint -> server 声明的 capabilities（resources/prompts 等，M5 伪工具合成依据）
	logger       *zap.Logger
}

// NewMCPHTTPProtocol 创建 MCP HTTP 协议适配器
func NewMCPHTTPProtocol(client *http.Client) *MCPHTTPProtocol {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &MCPHTTPProtocol{
		client:       client,
		initialized:  make(map[string]bool),
		negotiated:   make(map[string]string),
		sessions:     make(map[string]string),
		capabilities: make(map[string]map[string]interface{}),
		logger:       zap.NewNop(),
	}
}

// SetLogger 设置日志器（用于记录协议版本协商回退等）
func (p *MCPHTTPProtocol) SetLogger(logger *zap.Logger) {
	if logger == nil {
		return
	}
	p.mu.Lock()
	p.logger = logger
	p.mu.Unlock()
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

// mcpSSEMaxBytes SSE 响应体大小上限：防恶意 server 以无界 SSE 流放大响应、
// 或仅发通知不发响应导致客户端无限读取。10MB 兼顾大 tools/list/tools/call。
const mcpSSEMaxBytes int64 = 10 * 1024 * 1024

// errMCPSSETooLarge SSE 流超过大小上限（经 mcpCountingReader 触发）。
var errMCPSSETooLarge = errors.New("MCP SSE stream exceeds size limit")

// errMCPSSENoResponse SSE 流读完未含 JSON-RPC 响应（仅通知或空流）。
var errMCPSSENoResponse = errors.New("MCP SSE stream contained no JSON-RPC response")

// mcpCountingReader 计数读取器：累计读取字节达到 max 后下次 Read 返回
// errMCPSSETooLarge，限制 SSE 解析总量。max<=0 表示不限。
type mcpCountingReader struct {
	r   io.Reader
	n   int64
	max int64
}

func (c *mcpCountingReader) Read(p []byte) (int, error) {
	if c.max > 0 && c.n >= c.max {
		return 0, errMCPSSETooLarge
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// isMCPSSEResponse 判断响应 Content-Type 是否为 SSE 流（text/event-stream）。
// 用 mime.ParseMediaType 去掉 charset 等参数后比较，容忍 "; charset=utf-8"。
func isMCPSSEResponse(ct string) bool {
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mediaType == "text/event-stream"
}

// readMCPSSEResponse 从 SSE 流中解析首个 JSON-RPC 响应（含 result 或 error 的帧）。
// 忽略通知帧（notifications/*，仅 method 无 result/error）与服务端发起的请求
// （method+id 但无 result/error，如 sampling/roots 请求，当前客户端不处理）。
// 多个 data: 行按 SSE 规范以 \n 拼接成帧数据；空白行分帧；":" 起始为注释跳过。
// 超过 maxBytes 的流返回大小超限错误（防流式放大）。
func readMCPSSEResponse(body io.Reader, maxBytes int64) (*mcpResponse, error) {
	cr := &mcpCountingReader{r: body, max: maxBytes}
	br := bufio.NewReader(cr)
	var dataLines []string
	flush := func() (*mcpResponse, error) {
		if len(dataLines) == 0 {
			return nil, nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		// 先探帧类型：仅含 result 或 error 才是响应
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data), &probe); err != nil {
			return nil, nil // 非 JSON data（空或注释），跳过
		}
		if _, hasResult := probe["result"]; !hasResult {
			if _, hasError := probe["error"]; !hasError {
				return nil, nil // 通知帧或服务端请求，跳过
			}
		}
		var resp mcpResponse
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			return nil, nil
		}
		return &resp, nil
	}
	for {
		line, err := br.ReadString('\n')
		s := strings.TrimRight(line, "\n")
		s = strings.TrimRight(s, "\r")
		switch {
		case s == "":
			// 空行：分帧派发
			if r, e := flush(); e != nil {
				return nil, e
			} else if r != nil {
				return r, nil
			}
		case strings.HasPrefix(s, ":"):
			// SSE 注释行，跳过
		default:
			field, value := s, ""
			if i := strings.Index(s, ":"); i >= 0 {
				field = s[:i]
				value = s[i+1:]
				if strings.HasPrefix(value, " ") {
					value = value[1:] // SSE 规范：去掉单个前导空格
				}
			}
			if field == "data" {
				dataLines = append(dataLines, value)
			}
			// event/id/retry 字段当前无需
		}
		if err != nil {
			if err == io.EOF {
				// 冲刷末尾未以空行收束的帧
				if r, e := flush(); e != nil {
					return nil, e
				} else if r != nil {
					return r, nil
				}
				return nil, errMCPSSENoResponse
			}
			if errors.Is(err, errMCPSSETooLarge) {
				return nil, fmt.Errorf("MCP SSE 响应超过 %d 字节上限（疑似恶意 server 流式放大）", maxBytes)
			}
			return nil, fmt.Errorf("MCP SSE 读取失败: %w", err)
		}
	}
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

	// 回送服务端在 initialize 下发的 Mcp-Session-Id（Streamable HTTP 会话）
	p.mu.Lock()
	sessionID := p.sessions[endpoint]
	p.mu.Unlock()
	if sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionID)
	}

	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("MCP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 捕获服务端下发/刷新的 Mcp-Session-Id（缺失则保留既有，不覆盖）
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		p.mu.Lock()
		p.sessions[endpoint] = sid
		p.mu.Unlock()
	}

	ct := resp.Header.Get("Content-Type")

	if resp.StatusCode != http.StatusOK {
		// 非 200 且 SSE：尝试从流中提取 JSON-RPC error 对象（标准 server 用 SSE 报错）
		if isMCPSSEResponse(ct) {
			if errResp, _ := readMCPSSEResponse(resp.Body, mcpSSEMaxBytes); errResp != nil && errResp.Error != nil {
				return nil, errResp.Error
			}
			return nil, fmt.Errorf("MCP HTTP %d: SSE 响应未含错误对象", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MCP HTTP %d: %s", resp.StatusCode, string(body))
	}

	// 按响应 Content-Type 解析：SSE 流 或 单 JSON（兼容两类 server）
	var rpcResp *mcpResponse
	if isMCPSSEResponse(ct) {
		r, err := readMCPSSEResponse(resp.Body, mcpSSEMaxBytes)
		if err != nil {
			return nil, fmt.Errorf("MCP SSE 响应解析失败: %w", err)
		}
		rpcResp = r
	} else {
		var single mcpResponse
		if err := json.NewDecoder(resp.Body).Decode(&single); err != nil {
			return nil, fmt.Errorf("MCP 响应解析失败: %w", err)
		}
		rpcResp = &single
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}
	return rpcResp, nil
}

// Initialize 执行 MCP initialize handshake
func (p *MCPHTTPProtocol) Initialize(ctx context.Context, endpoint string, headers map[string]string) error {
	p.mu.Lock()
	if p.initialized[endpoint] {
		p.mu.Unlock()
		return nil
	}
	logger := p.logger
	p.mu.Unlock()

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    mcpClientCapabilities(),
			"clientInfo":      mcpClientInfo(),
		},
	}

	resp, err := p.do(ctx, endpoint, req, headers)
	if err != nil {
		return fmt.Errorf("MCP initialize 失败: %w", err)
	}
	// 协商协议版本：server 可能回退到旧版（如 firecrawl 回 2024-11-05），采用 server 版本并记录。
	negotiated := negotiateMCPVersion(resp.Result, mcpProtocolVersion, logger)
	// M5：捕获 server 声明的 capabilities（resources/prompts 等），供 ListTools 据此合成伪工具。
	serverCaps := parseMCPServerCapabilities(resp.Result)

	// 发送 initialized notification
	notif := &mcpRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		Params:  map[string]interface{}{},
	}
	_, _ = p.do(ctx, endpoint, notif, headers)

	p.mu.Lock()
	p.initialized[endpoint] = true
	p.negotiated[endpoint] = negotiated
	p.capabilities[endpoint] = serverCaps
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
	// M5：据 server capabilities 合成 read_resource/get_prompt 伪工具，纳入 tools 视图 -> DeriveSKUs 出 SKU。
	result.Tools = p.synthesizePseudoTools(ctx, endpoint, headers, result.Tools)
	return result.Tools, nil
}

// hasCapability 判断 endpoint 协商的 server capabilities 是否声明了某能力（resources/prompts）。
// MCP 规范中 capabilities 对象存在该 key 即表示支持（值可为空对象）。
func (p *MCPHTTPProtocol) hasCapability(endpoint, capability string) bool {
	p.mu.Lock()
	caps := p.capabilities[endpoint]
	p.mu.Unlock()
	if caps == nil {
		return false
	}
	_, ok := caps[capability]
	return ok
}

// synthesizePseudoTools 据 server capabilities + resources/prompts 清单合成伪工具追加到 tools。
// 已存在同名真实工具时不合成（避免重复）；resources/list 或 prompts/list 拉取失败则跳过对应伪工具
// （仅记录日志，不影响 tools/list 本身）。供 ListTools 调用。
func (p *MCPHTTPProtocol) synthesizePseudoTools(ctx context.Context, endpoint string, headers map[string]string, tools []MCPTool) []MCPTool {
	existing := make(map[string]bool, len(tools))
	for _, t := range tools {
		existing[t.Name] = true
	}
	p.mu.Lock()
	logger := p.logger
	p.mu.Unlock()

	if p.hasCapability(endpoint, "resources") && !existing[mcpPseudoToolReadResource] {
		if resources, err := p.ListResources(ctx, endpoint, headers); err == nil {
			tools = append(tools, mcpReadResourcePseudoTool(resources))
		} else if logger != nil {
			logger.Warn("MCP resources/list 拉取失败，跳过 read_resource 伪工具",
				zap.String("endpoint", endpoint), zap.Error(err))
		}
	}
	if p.hasCapability(endpoint, "prompts") && !existing[mcpPseudoToolGetPrompt] {
		if prompts, err := p.ListPrompts(ctx, endpoint, headers); err == nil {
			tools = append(tools, mcpGetPromptPseudoTool(prompts))
		} else if logger != nil {
			logger.Warn("MCP prompts/list 拉取失败，跳过 get_prompt 伪工具",
				zap.String("endpoint", endpoint), zap.Error(err))
		}
	}
	return tools
}

// ListResources 获取 MCP 资源列表（resources/list）。server 须声明 resources capability。
func (p *MCPHTTPProtocol) ListResources(ctx context.Context, endpoint string, headers map[string]string) ([]MCPResource, error) {
	if err := p.Initialize(ctx, endpoint, headers); err != nil {
		return nil, err
	}
	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "resources/list",
		Params:  map[string]interface{}{},
	}
	resp, err := p.do(ctx, endpoint, req, headers)
	if err != nil {
		return nil, err
	}
	var result struct {
		Resources []MCPResource `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP resources/list 解析失败: %w", err)
	}
	return result.Resources, nil
}

// ReadResource 读取指定资源内容（resources/read）。
func (p *MCPHTTPProtocol) ReadResource(ctx context.Context, endpoint, uri string, headers map[string]string) (map[string]interface{}, error) {
	if err := p.Initialize(ctx, endpoint, headers); err != nil {
		return nil, err
	}
	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      5,
		Method:  "resources/read",
		Params:  map[string]interface{}{"uri": uri},
	}
	resp, err := p.do(ctx, endpoint, req, headers)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP resources/read 解析失败: %w", err)
	}
	return result, nil
}

// ListPrompts 获取 MCP 提示列表（prompts/list）。server 须声明 prompts capability。
func (p *MCPHTTPProtocol) ListPrompts(ctx context.Context, endpoint string, headers map[string]string) ([]MCPPrompt, error) {
	if err := p.Initialize(ctx, endpoint, headers); err != nil {
		return nil, err
	}
	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      6,
		Method:  "prompts/list",
		Params:  map[string]interface{}{},
	}
	resp, err := p.do(ctx, endpoint, req, headers)
	if err != nil {
		return nil, err
	}
	var result struct {
		Prompts []MCPPrompt `json:"prompts"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP prompts/list 解析失败: %w", err)
	}
	return result.Prompts, nil
}

// GetPrompt 获取指定提示渲染结果（prompts/get）。
func (p *MCPHTTPProtocol) GetPrompt(ctx context.Context, endpoint, name string, arguments map[string]interface{}, headers map[string]string) (map[string]interface{}, error) {
	if err := p.Initialize(ctx, endpoint, headers); err != nil {
		return nil, err
	}
	params := map[string]interface{}{"name": name}
	if arguments != nil {
		params["arguments"] = arguments
	}
	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      7,
		Method:  "prompts/get",
		Params:  params,
	}
	resp, err := p.do(ctx, endpoint, req, headers)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("MCP prompts/get 解析失败: %w", err)
	}
	return result, nil
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

	// M5：伪工具拦截 -> resources/read / prompts/get（不进 tools/call）。
	switch action {
	case mcpPseudoToolReadResource:
		uri, _ := arguments["uri"].(string)
		return p.ReadResource(ctx, endpoint, uri, headers)
	case mcpPseudoToolGetPrompt:
		name, _ := arguments["name"].(string)
		var promptArgs map[string]interface{}
		if a, ok := arguments["arguments"].(map[string]interface{}); ok {
			promptArgs = a
		}
		return p.GetPrompt(ctx, endpoint, name, promptArgs, headers)
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
		client:       &http.Client{Timeout: 30 * time.Second},
		initialized:  make(map[string]bool),
		negotiated:   make(map[string]string),
		sessions:     make(map[string]string),
		capabilities: make(map[string]map[string]interface{}),
		logger:       zap.NewNop(),
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

// Reset 重置 endpoint 的初始化状态（用于连接断开后重连）。
// 会话 ID 与协商版本随 endpoint 失效一并清空，避免回送过期 session。
func (p *MCPHTTPProtocol) Reset(endpoint string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.initialized, endpoint)
	delete(p.negotiated, endpoint)
	delete(p.sessions, endpoint)
	delete(p.capabilities, endpoint)
}

// ErrMCPNotInitialized MCP 未初始化错误
var ErrMCPNotInitialized = errors.New("MCP endpoint not initialized")
