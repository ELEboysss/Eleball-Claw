package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 样例 manifest：三段能力齐备（skills + tools + mcpServers），覆盖 process/docker/http 与 stdio/http/sse。
const samplePackageJSON = `{
  "name": "agent-reach",
  "version": "2.0.0",
  "description": "全网洞察与社交平台数据采集",
  "author": "eleball",
  "category": "互联网",
  "level": 2,
  "skills": [
    { "name": "web-read", "description": "网页内容读取" }
  ],
  "tools": [
    {
      "name": "github_repo",
      "description": "查询 GitHub 仓库信息",
      "transport": "process",
      "command": ["python3", "main.py"],
      "env": { "TOKEN": "${credentials.GH_TOKEN}" },
      "parameters": { "type": "object", "properties": { "repo": { "type": "string" } }, "required": ["repo"] },
      "credentials": { "GH_TOKEN": { "type": "api_key", "label": "GitHub Token", "required": true } },
      "pricing": { "type": "per_call", "amount_per_call": 5, "currency": "danwan" }
    },
    {
      "name": "scrape",
      "description": "抓取网页内容",
      "transport": "docker",
      "image": "crpi/eleball/agent-reach:develop",
      "timeout_seconds": 60
    },
    {
      "name": "remote_lookup",
      "description": "远程查询服务",
      "transport": "http",
      "endpoint": "https://api.example.com/lookup"
    }
  ],
  "mcpServers": {
    "github": {
      "transport": "stdio",
      "command": ["npx", "-y", "@modelcontextprotocol/server-github"]
    },
    "search": {
      "transport": "http",
      "url": "https://mcp.example.com/search",
      "headers": { "Authorization": "${credentials.API_KEY}" }
    }
  }
}`

// TestParsePackageManifest_Full 解析三段齐备样例：字段全量命中、transport 专属字段就位。
func TestParsePackageManifest_Full(t *testing.T) {
	m, err := ParsePackageManifest([]byte(samplePackageJSON))
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Equal(t, "agent-reach", m.Name)
	assert.Equal(t, "2.0.0", m.Version)
	assert.Equal(t, "全网洞察与社交平台数据采集", m.Description)
	assert.Equal(t, "eleball", m.Author)
	assert.Equal(t, "互联网", m.Category)
	assert.Equal(t, 2, m.Level)

	// skills 段
	require.Len(t, m.Skills, 1)
	assert.Equal(t, "web-read", m.Skills[0].Name)
	assert.Equal(t, "网页内容读取", m.Skills[0].Description)

	// tools 段：process/docker/http 三型
	require.Len(t, m.Tools, 3)
	pt := m.Tools[0]
	assert.Equal(t, "github_repo", pt.Name)
	assert.Equal(t, "process", pt.Transport)
	assert.Equal(t, []string{"python3", "main.py"}, pt.Command)
	assert.Equal(t, "${credentials.GH_TOKEN}", pt.Env["TOKEN"])
	require.NotNil(t, pt.Parameters)
	assert.Equal(t, "object", pt.Parameters.Type)
	assert.Contains(t, pt.Parameters.Required, "repo")
	require.NotNil(t, pt.Credentials)
	assert.Equal(t, "api_key", pt.Credentials["GH_TOKEN"].Type)
	require.NotNil(t, pt.Pricing)
	assert.Equal(t, "per_call", pt.Pricing.Type)
	assert.Equal(t, "docker", m.Tools[1].Transport)
	assert.Equal(t, "crpi/eleball/agent-reach:develop", m.Tools[1].Image)
	assert.Equal(t, 60, m.Tools[1].TimeoutSeconds)
	assert.Equal(t, "http", m.Tools[2].Transport)
	assert.Equal(t, "https://api.example.com/lookup", m.Tools[2].Endpoint)

	// mcpServers 段：stdio + http 两型
	require.Len(t, m.MCPServers, 2)
	gs := m.MCPServers["github"]
	assert.Equal(t, "stdio", gs.Transport)
	assert.Equal(t, []string{"npx", "-y", "@modelcontextprotocol/server-github"}, gs.Command)
	ss := m.MCPServers["search"]
	assert.Equal(t, "http", ss.Transport)
	assert.Equal(t, "https://mcp.example.com/search", ss.URL)
	assert.Equal(t, "${credentials.API_KEY}", ss.Headers["Authorization"])
}

// TestParsePackageManifest_DefaultLevel level 缺省按 1。
func TestParsePackageManifest_DefaultLevel(t *testing.T) {
	j := `{"name":"x","version":"1.0.0","description":"d"}`
	m, err := ParsePackageManifest([]byte(j))
	require.NoError(t, err)
	assert.Equal(t, 1, m.Level)
}

// TestParsePackageManifest_InvalidFields 非法字段报错（T2.1 验收）：
// 未知字段、非法 transport、transport 专属字段缺失、坏版本、重复名、尾随内容。
func TestParsePackageManifest_InvalidFields(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{
			"未知顶层字段",
			`{"name":"x","version":"1.0.0","description":"d","bogus":1}`,
			"unknown field", // json 标准库错误信息为英文：json: unknown field "bogus"
		},
		{
			"tool transport 非法",
			`{"name":"x","version":"1.0.0","description":"d","tools":[{"name":"t","description":"d","transport":"ssh"}]}`,
			"transport 非法",
		},
		{
			"process 缺 command",
			`{"name":"x","version":"1.0.0","description":"d","tools":[{"name":"t","description":"d","transport":"process"}]}`,
			"command 必填",
		},
		{
			"docker 缺 image",
			`{"name":"x","version":"1.0.0","description":"d","tools":[{"name":"t","description":"d","transport":"docker"}]}`,
			"image 必填",
		},
		{
			"http 缺 endpoint",
			`{"name":"x","version":"1.0.0","description":"d","tools":[{"name":"t","description":"d","transport":"http"}]}`,
			"endpoint 必填",
		},
		{
			"mcp stdio 缺 command",
			`{"name":"x","version":"1.0.0","description":"d","mcpServers":{"s":{"transport":"stdio"}}}`,
			"command 必填",
		},
		{
			"mcp sse 缺 url",
			`{"name":"x","version":"1.0.0","description":"d","mcpServers":{"s":{"transport":"sse"}}}`,
			"url 必填",
		},
		{
			"版本非语义化",
			`{"name":"x","version":"1.0","description":"d"}`,
			"version 非法",
		},
		{
			"skills 名重复",
			`{"name":"x","version":"1.0.0","description":"d","skills":[{"name":"a"},{"name":"a"}]}`,
			"重复",
		},
		{
			"尾随第二个 JSON",
			`{"name":"x","version":"1.0.0","description":"d"}{"name":"y"}`,
			"多余内容",
		},
		{
			"name 非 slug",
			`{"name":"Bad_Name","version":"1.0.0","description":"d"}`,
			"name 非法",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePackageManifest([]byte(tc.json))
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), tc.want), "err=%v want contains %q", err, tc.want)
		})
	}
}

// TestParsePackageManifest_InvalidJSON 非法 JSON 语法报错。
func TestParsePackageManifest_InvalidJSON(t *testing.T) {
	_, err := ParsePackageManifest([]byte(`{"name":`))
	require.Error(t, err)
}
