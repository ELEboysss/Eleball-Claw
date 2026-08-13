package service

import (
	"context"
	"fmt"

	"github.com/eleball/gateway/internal/model"
)

// MCPClient 传输无关的 MCP 客户端接口（T2.5 kimi-code 式重构）。
// 连接/工具发现/调用/资源/提示均不感知底层传输（http/stdio/sse）。
// read_resource/get_prompt 伪工具由协议层在 ListTools 时合成、CallTool 拦截重映射。
type MCPClient interface {
	// Connect 建立会话：HTTP=initialize handshake；stdio=会话须已由 Supervisor（SkillRuntimeManager）注册。
	Connect(ctx context.Context) error
	// Disconnect 释放连接：HTTP=Reset endpoint（清会话/协商状态）；stdio=no-op（会话归 Supervisor 进程主管）。
	Disconnect() error
	// ListTools 列出工具（含协议层合成的 read_resource/get_prompt 伪工具）。
	ListTools(ctx context.Context) ([]MCPTool, error)
	// CallTool 调用 MCP 工具：协议层过滤内部键（__mcp_* 等）并拦截伪工具到 resources/read|prompts/get。
	CallTool(ctx context.Context, name string, arguments map[string]interface{}) (map[string]interface{}, error)
	// ListResources 列出资源（resources/list）。
	ListResources(ctx context.Context) ([]MCPResource, error)
	// ListPrompts 列出提示（prompts/list）。
	ListPrompts(ctx context.Context) ([]MCPPrompt, error)
}

// mcpHTTPClient MCP Streamable HTTP 客户端适配器：绑定 endpoint+headers，包装 MCPHTTPProtocol。
// 探活头经 probeHeaders 提取字面量头（跳过 ${credentials.KEY} 模板，与 registry 探活一致）。
type mcpHTTPClient struct {
	endpoint string
	headers  map[string]string
	proto    *MCPHTTPProtocol
}

func (c *mcpHTTPClient) Connect(ctx context.Context) error {
	return c.proto.Initialize(ctx, c.endpoint, c.headers)
}

func (c *mcpHTTPClient) Disconnect() error {
	c.proto.Reset(c.endpoint)
	return nil
}

func (c *mcpHTTPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	return c.proto.ListTools(ctx, c.endpoint, c.headers)
}

func (c *mcpHTTPClient) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (map[string]interface{}, error) {
	return c.proto.Execute(c.endpoint, name, arguments, c.headers)
}

func (c *mcpHTTPClient) ListResources(ctx context.Context) ([]MCPResource, error) {
	return c.proto.ListResources(ctx, c.endpoint, c.headers)
}

func (c *mcpHTTPClient) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	return c.proto.ListPrompts(ctx, c.endpoint, c.headers)
}

// mcpStdioClient MCP stdio 客户端适配器：绑定 runtimeID，包装 MCPStdioProtocol。
// 会话由 SkillRuntimeManager spawn 子进程后 RegisterSession 建立，本客户端不持有生命周期。
type mcpStdioClient struct {
	runtimeID string
	proto     *MCPStdioProtocol
}

func (c *mcpStdioClient) Connect(ctx context.Context) error {
	return c.proto.Initialize(ctx, c.runtimeID)
}

func (c *mcpStdioClient) Disconnect() error {
	// 会话归 Supervisor 进程主管（spawn/回收），客户端仅通信不关闭会话。
	return nil
}

func (c *mcpStdioClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	return c.proto.ListTools(ctx, c.runtimeID)
}

func (c *mcpStdioClient) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (map[string]interface{}, error) {
	return c.proto.Execute(c.runtimeID, name, arguments)
}

func (c *mcpStdioClient) ListResources(ctx context.Context) ([]MCPResource, error) {
	return c.proto.ListResources(ctx, c.runtimeID)
}

func (c *mcpStdioClient) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	return c.proto.ListPrompts(ctx, c.runtimeID)
}

// newMCPClient 按 SkillRuntime.Transport 构造传输无关 MCP 客户端。
// 当前支持 mcp_http / mcp_stdio；mcp_sse 后置未实现，返回可读错误不阻塞主链路。
func newMCPClient(rt *model.SkillRuntime, httpProto *MCPHTTPProtocol, stdioProto *MCPStdioProtocol) (MCPClient, error) {
	if rt == nil {
		return nil, fmt.Errorf("runtime 记录不能为空")
	}
	switch rt.Transport {
	case model.SkillRuntimeTransportMCPHTTP:
		if httpProto == nil {
			return nil, fmt.Errorf("MCP HTTP 协议未初始化")
		}
		return &mcpHTTPClient{endpoint: rt.Endpoint, headers: probeHeaders(rt), proto: httpProto}, nil
	case model.SkillRuntimeTransportMCPStdio:
		if stdioProto == nil {
			return nil, fmt.Errorf("MCP stdio 协议未初始化")
		}
		return &mcpStdioClient{runtimeID: rt.ID, proto: stdioProto}, nil
	case model.SkillRuntimeTransportMCPSSE:
		return nil, fmt.Errorf("MCP SSE 传输后置未实现")
	default:
		return nil, fmt.Errorf("不支持的 MCP transport: %s", rt.Transport)
	}
}
