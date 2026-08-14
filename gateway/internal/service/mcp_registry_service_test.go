package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registryFixture 模拟官方 MCP Registry 的 /v0/servers 响应：
// - 带 streamable-http remote 的 server（优先映射 mcp_http）
// - 仅 npm package 的 server（映射 npx stdio，含必填密钥环境变量 + positional 参数）
// - 仅 pypi package 的 server（映射 uvx）
// - 仅 oci package 的 server（supported=false）
const registryFixture = `{
  "servers": [
    {"server": {
      "name": "io.example/remote-srv",
      "title": "Remote Srv",
      "description": "远端 server",
      "version": "1.0.0",
      "remotes": [{"type": "streamable-http", "url": "https://mcp.example.com/mcp",
        "headers": [{"name": "Authorization", "description": "API 令牌", "isRequired": true, "isSecret": true}]}],
      "packages": [{"registryType": "npm", "identifier": "@example/remote-srv", "transport": {"type": "stdio"}}]
    }},
    {"server": {
      "name": "io.github.modelcontextprotocol/server-filesystem",
      "description": "文件系统",
      "version": "2.1.0",
      "packages": [{
        "registryType": "npm",
        "identifier": "@modelcontextprotocol/server-filesystem",
        "transport": {"type": "stdio"},
        "runtimeHint": "npx",
        "environmentVariables": [{"name": "FS_TOKEN", "description": "访问令牌", "isRequired": true, "isSecret": true}],
        "packageArguments": [{"type": "positional", "value": "/data"}, {"type": "positional", "value": "${HOME}"}]
      }]
    }},
    {"server": {
      "name": "io.example/py-srv",
      "description": "python server",
      "version": "0.3.0",
      "packages": [{"registryType": "pypi", "identifier": "mcp-server-py", "transport": {"type": "stdio"}}]
    }},
    {"server": {
      "name": "io.example/oci-srv",
      "description": "仅 docker 形态",
      "version": "1.0.0",
      "packages": [{"registryType": "oci", "identifier": "ghcr.io/example/oci-srv"}]
    }}
  ],
  "metadata": {"count": 4}
}`

// TestMCPRegistryClient_Search 验证 E1：搜索代理 + 安装表单建议映射。
func TestMCPRegistryClient_Search(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v0/servers", r.URL.Path)
		assert.Equal(t, "filesystem", r.URL.Query().Get("search"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(registryFixture))
	}))
	defer srv.Close()

	client := &MCPRegistryClient{BaseURL: srv.URL}
	entries, err := client.Search(context.Background(), "filesystem", 0)
	require.NoError(t, err)
	require.Len(t, entries, 4)
	byName := map[string]MCPRegistryEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	// streamable-http remote 优先于 npm package
	remote := byName["io.example/remote-srv"]
	require.True(t, remote.Supported)
	require.NotNil(t, remote.Install)
	assert.Equal(t, "mcp_http", remote.Install.Transport)
	assert.Equal(t, "https://mcp.example.com/mcp", remote.Install.Endpoint)
	require.Len(t, remote.Install.EnvVars, 1)
	assert.Equal(t, "Authorization", remote.Install.EnvVars[0].Name)
	assert.True(t, remote.Install.EnvVars[0].Required)
	assert.True(t, remote.Install.EnvVars[0].Secret)

	// npm package -> npx -y <identifier>，positional 参数保留（${...} 占位跳过）
	npm := byName["io.github.modelcontextprotocol/server-filesystem"]
	require.True(t, npm.Supported)
	require.NotNil(t, npm.Install)
	assert.Equal(t, "mcp_stdio", npm.Install.Transport)
	assert.Equal(t, "npx", npm.Install.Command)
	assert.Equal(t, []string{"-y", "@modelcontextprotocol/server-filesystem", "/data"}, npm.Install.Args)
	require.Len(t, npm.Install.EnvVars, 1)
	assert.Equal(t, "FS_TOKEN", npm.Install.EnvVars[0].Name)

	// pypi package -> uvx
	py := byName["io.example/py-srv"]
	require.True(t, py.Supported)
	assert.Equal(t, "uvx", py.Install.Command)
	assert.Equal(t, []string{"mcp-server-py"}, py.Install.Args)

	// oci/docker 形态 -> supported=false + note
	oci := byName["io.example/oci-srv"]
	assert.False(t, oci.Supported)
	assert.Nil(t, oci.Install)
	assert.NotEmpty(t, oci.Note)
}

// TestMCPRegistryClient_SearchUpstreamFail 上游非 200 / 不可达返回 error（handler 转 2002）。
func TestMCPRegistryClient_SearchUpstreamFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := &MCPRegistryClient{BaseURL: srv.URL}
	_, err := client.Search(context.Background(), "x", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")

	// 不可达地址
	bad := &MCPRegistryClient{BaseURL: "http://127.0.0.1:1"}
	_, err = bad.Search(context.Background(), "x", 10)
	require.Error(t, err)
}

// TestMCPRegistryEntry_JSONShape 契约形状自检（前端依赖 supported/install.env_vars 字段名）。
func TestMCPRegistryEntry_JSONShape(t *testing.T) {
	e := MCPRegistryEntry{
		Name: "n", Version: "1.0.0", Supported: true,
		Install: &MCPRegistryInstall{Transport: "mcp_stdio", Command: "npx",
			Args: []string{"-y", "pkg"}, EnvVars: []MCPRegistryEnvVar{{Name: "K", Required: true, Secret: true}}},
	}
	data, err := json.Marshal(e)
	require.NoError(t, err)
	s := string(data)
	assert.Contains(t, s, `"supported":true`)
	assert.Contains(t, s, `"env_vars"`)
	assert.Contains(t, s, `"secret":true`)
}
