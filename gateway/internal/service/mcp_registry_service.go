package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// mcp_registry_service.go — E1：官方 MCP Registry 只读搜索客户端。
//
// 供 claw DIY 工作室「装 MCP」页搜索开源社区 MCP server（registry.modelcontextprotocol.io），
// 把 registry 条目映射为安装表单建议（transport/command/args/endpoint + 需用户填写的环境变量），
// 前端「填入安装表单」后复用 G3 探测→安装链路（probeAndInstallMCP），不做盲目一键安装。
// 纯只读代理：15s 超时 + 4MB 响应上限，不执行任何命令。

const (
	// mcpRegistryDefaultBaseURL 官方 MCP Registry 生产地址
	mcpRegistryDefaultBaseURL = "https://registry.modelcontextprotocol.io"
	// mcpRegistryMaxBytes 搜索响应上限（防异常上游放大）
	mcpRegistryMaxBytes = 4 << 20
)

// MCPRegistryEnvVar 安装前需要用户提供的环境变量/请求头（密钥项留空值由用户填写）。
type MCPRegistryEnvVar struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
}

// MCPRegistryInstall 安装表单建议（与 /mcp/install 请求体同构，前端直接预填）。
type MCPRegistryInstall struct {
	Transport string              `json:"transport"` // mcp_stdio | mcp_http
	Command   string              `json:"command,omitempty"`
	Args      []string            `json:"args,omitempty"`
	Endpoint  string              `json:"endpoint,omitempty"`
	EnvVars   []MCPRegistryEnvVar `json:"env_vars,omitempty"`
}

// MCPRegistryEntry 单条搜索结果。
type MCPRegistryEntry struct {
	Name        string              `json:"name"` // registry 全名，如 io.github.modelcontextprotocol/server-filesystem
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description"`
	Version     string              `json:"version"`
	Supported   bool                `json:"supported"`
	Note        string              `json:"note,omitempty"` // supported=false 时的原因
	Install     *MCPRegistryInstall `json:"install,omitempty"`
}

// MCPRegistryClient 官方 MCP Registry 只读客户端。BaseURL 可替换（测试指向 httptest）。
type MCPRegistryClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewMCPRegistryClient 生产客户端（官方地址 + 15s 超时）。
func NewMCPRegistryClient() *MCPRegistryClient {
	return &MCPRegistryClient{
		BaseURL:    mcpRegistryDefaultBaseURL,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// ---- registry 响应结构（宽松解析，只取需要字段） ----

type mcpRegistryResponse struct {
	Servers []struct {
		Server mcpRegistryServer `json:"server"`
	} `json:"servers"`
}

type mcpRegistryServer struct {
	Name        string               `json:"name"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Version     string               `json:"version"`
	Packages    []mcpRegistryPackage `json:"packages"`
	Remotes     []mcpRegistryRemote  `json:"remotes"`
}

type mcpRegistryPackage struct {
	RegistryType string `json:"registryType"` // npm | pypi | oci | nuget ...
	Identifier   string `json:"identifier"`
	RuntimeHint  string `json:"runtimeHint"` // npx | uvx | docker ...
	Transport    struct {
		Type string `json:"type"` // stdio | streamable-http | sse
	} `json:"transport"`
	EnvironmentVariables []mcpRegistryEnvDecl `json:"environmentVariables"`
	PackageArguments     []mcpRegistryArg     `json:"packageArguments"`
}

type mcpRegistryRemote struct {
	Type    string               `json:"type"` // streamable-http | sse
	URL     string               `json:"url"`
	Headers []mcpRegistryEnvDecl `json:"headers"`
}

type mcpRegistryEnvDecl struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsRequired  bool   `json:"isRequired"`
	IsSecret    bool   `json:"isSecret"`
}

type mcpRegistryArg struct {
	Type  string `json:"type"` // positional | named
	Value string `json:"value"`
}

// Search 搜索官方 MCP Registry 并把条目映射为安装表单建议。
// query 必填；limit 缺省 20、上限 50。上游非 200 / 网络失败返回 error（handler 转 2002）。
func (c *MCPRegistryClient) Search(ctx context.Context, query string, limit int) ([]MCPRegistryEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	u := strings.TrimSuffix(c.BaseURL, "/") + "/v0/servers?search=" + url.QueryEscape(query) + "&limit=" + strconv.Itoa(limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 registry 请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 MCP Registry 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP Registry 返回 HTTP %d", resp.StatusCode)
	}
	var parsed mcpRegistryResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, mcpRegistryMaxBytes)).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("解析 MCP Registry 响应失败: %w", err)
	}
	entries := make([]MCPRegistryEntry, 0, len(parsed.Servers))
	for _, s := range parsed.Servers {
		entries = append(entries, mapRegistryEntry(s.Server))
	}
	return entries, nil
}

// mapRegistryEntry 把 registry server 映射为安装表单建议：
// 优先 remotes 的 streamable-http（零本地安装）；否则 packages 的 npm/pypi stdio
// （npx/uvx 直跑）；oci/docker 等形态不支持一键填入（supported=false + note）。
func mapRegistryEntry(srv mcpRegistryServer) MCPRegistryEntry {
	e := MCPRegistryEntry{
		Name:        srv.Name,
		Title:       srv.Title,
		Description: srv.Description,
		Version:     srv.Version,
	}
	for _, r := range srv.Remotes {
		if r.Type != "streamable-http" || r.URL == "" {
			continue
		}
		e.Supported = true
		e.Install = &MCPRegistryInstall{
			Transport: "mcp_http",
			Endpoint:  r.URL,
			EnvVars:   envDeclsToVars(r.Headers),
		}
		return e
	}
	for _, p := range srv.Packages {
		if p.Transport.Type != "" && p.Transport.Type != "stdio" {
			continue // 非 stdio 形态的 package 不在填入建议范围
		}
		var command string
		var args []string
		switch p.RegistryType {
		case "npm":
			command = orDefault(p.RuntimeHint, "npx")
			if command == "npx" {
				args = []string{"-y", p.Identifier}
			} else {
				args = []string{p.Identifier}
			}
		case "pypi":
			command = orDefault(p.RuntimeHint, "uvx")
			args = []string{p.Identifier}
		default:
			continue // oci/nuget/mcpb 等不支持一键填入
		}
		args = append(args, positionalArgs(p.PackageArguments)...)
		e.Supported = true
		e.Install = &MCPRegistryInstall{
			Transport: "mcp_stdio",
			Command:   command,
			Args:      args,
			EnvVars:   envDeclsToVars(p.EnvironmentVariables),
		}
		return e
	}
	e.Supported = false
	e.Note = "该 server 仅提供 docker 镜像等形态，暂不支持一键填入（可手动参照其文档安装）"
	return e
}

// envDeclsToVars 把 registry 的环境变量/请求头声明转为表单预填项（值一律留空由用户填写，
// 不透传 registry 的 ${...} 模板——密钥不应经搜索结果流入表单）。
func envDeclsToVars(decls []mcpRegistryEnvDecl) []MCPRegistryEnvVar {
	if len(decls) == 0 {
		return nil
	}
	out := make([]MCPRegistryEnvVar, 0, len(decls))
	for _, d := range decls {
		if d.Name == "" {
			continue
		}
		out = append(out, MCPRegistryEnvVar{
			Name:        d.Name,
			Description: d.Description,
			Required:    d.IsRequired,
			Secret:      d.IsSecret,
		})
	}
	return out
}

// positionalArgs 取 package 的 positional 参数值（如 filesystem server 的允许目录）。
// 含 ${...} 占位的参数跳过（需用户自行在表单补充，避免把模板当实参 spawn）。
func positionalArgs(args []mcpRegistryArg) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a.Type != "positional" || a.Value == "" || strings.Contains(a.Value, "${") {
			continue
		}
		out = append(out, a.Value)
	}
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
