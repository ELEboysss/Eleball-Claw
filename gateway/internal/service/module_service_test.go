package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// TestModuleService_RescanMarketplace_MCP 验证 marketplace 扫描能识别 transport_type=mcp 的示例模块，
// 并创建正确的 SkillRuntime（含 MCPServerConfig）与驱动别名映射。
func TestModuleService_RescanMarketplace_MCP(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))

	skillRuntimeRepo := repository.NewSkillRuntimeRepo(db)
	registry := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	registry.SetRepo(skillRuntimeRepo)
	manager := NewSkillRuntimeManager(registry, zap.NewNop())
	moduleRepo := repository.NewModuleRepo(db)
	driverRepo := repository.NewDriverRepo(db)
	svc := NewModuleService(registry, manager, skillRuntimeRepo, nil)
	svc.SetModuleRepo(moduleRepo)
	svc.SetDriverRepo(driverRepo)

	root := t.TempDir()
	mcpDir := filepath.Join(root, "mcp-hello")
	require.NoError(t, os.MkdirAll(mcpDir, 0755))
	manifest := []byte(`{
  "module_id": "mcp-hello",
  "name": "MCP Hello",
  "description": "test",
  "transport_type": "mcp",
  "capabilities": ["hello"],
  "mcp_server_config": {"url": "http://mcp-hello:8080/mcp"},
  "driver": {"driver_id": "mcp_hello", "name": "MCP Hello Driver"}
}`)
	require.NoError(t, os.WriteFile(filepath.Join(mcpDir, "module.json"), manifest, 0644))

	require.NoError(t, svc.ensureMarketplaceModules(root, zap.NewNop()))

	rt, err := svc.repo.GetByID("mcp-hello")
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Equal(t, model.SkillRuntimeTransportMCPHTTP, rt.Transport)
	assert.Equal(t, "http://mcp-hello:8080", rt.Endpoint)
	assert.True(t, rt.Official)
	assert.Equal(t, "mcp_hello", rt.DriverID)
	cfg := rt.GetMCPServerConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "http://mcp-hello:8080/mcp", cfg.URL)

	drv, err := svc.ResolveDriver("mcp_hello")
	require.NoError(t, err)
	require.NotNil(t, drv)
	assert.Equal(t, string(model.ModuleTransportTypeMCP), drv.TransportType)
	require.NotNil(t, drv.MCPServerConfig)
	assert.Equal(t, "http://mcp-hello:8080/mcp", drv.MCPServerConfig.URL)
}

// TestModuleService_RescanMarketplace_AgentReachMCP 验证 G1：agent-reach 从 execute 迁移到
// mcp_http + auto_sku 后，rescan 能正确创建 mcp_http 运行时（端点经 mcpModuleBaseURL 收敛为根路径、
// auto_sku=true、mcp_server_config.headers 含 6 个 ${credentials.KEY} 凭证模板、credentials 声明 6 个
// module 级凭证；exa 搜索经 mcporter 零配置接入，不占凭证槽）。读取真实 marketplace/agent-reach/module.json，守护 G1 配置不被回退。
func TestModuleService_RescanMarketplace_AgentReachMCP(t *testing.T) {
	// 定位真实 marketplace/agent-reach/module.json（兼容 go test 不同 cwd）
	var manifestPath string
	for _, p := range []string{
		filepath.Join("..", "..", "marketplace", "agent-reach", "module.json"),
		filepath.Join("..", "..", "..", "marketplace", "agent-reach", "module.json"),
		filepath.Join("marketplace", "agent-reach", "module.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			manifestPath = p
			break
		}
	}
	require.NotEmpty(t, manifestPath, "未找到 agent-reach/module.json")
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))
	skillRuntimeRepo := repository.NewSkillRuntimeRepo(db)
	registry := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	registry.SetRepo(skillRuntimeRepo)
	manager := NewSkillRuntimeManager(registry, zap.NewNop())
	svc := NewModuleService(registry, manager, skillRuntimeRepo, nil)

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "agent-reach"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "agent-reach", "module.json"), data, 0644))

	require.NoError(t, svc.ensureMarketplaceModules(root, zap.NewNop()))

	rt, err := svc.repo.GetByID("agent-reach")
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Equal(t, model.SkillRuntimeTransportMCPHTTP, rt.Transport)
	assert.True(t, rt.AutoSKU, "auto_sku 应为 true（G1 迁移后免手写 SKU）")
	// mcpModuleBaseURL 把 mcp_server_config.url 收敛为根路径（无 /mcp），网关据此 POST JSON-RPC
	assert.Equal(t, "http://localhost:8094", rt.Endpoint)
	assert.Equal(t, "agent_reach", rt.DriverID)

	cfg := rt.GetMCPServerConfig()
	require.NotNil(t, cfg)
	// 6 个凭证请求头模板（${credentials.KEY} 由网关 prepareMCPHeaders 替换为值后注入；exa 经 mcporter 零配置，无凭证）
	require.Len(t, cfg.Headers, 6)
	assert.Equal(t, "${credentials.twitter_cookie}", cfg.Headers["X-Twitter-Cookie"])
	assert.Equal(t, "${credentials.bilibili_cookie}", cfg.Headers["X-Bilibili-Cookie"])

	// 6 个 module 级凭证声明（同模块多 SKU 共享 module:agent_reach 桶；exa 零配置不入此列）
	creds := rt.CredentialsMap()
	require.Len(t, creds, 6)
	require.Contains(t, creds, "twitter_cookie")
	assert.Equal(t, model.CredentialScopeModule, creds["bilibili_cookie"].Scope)
}

// echoUserScript 测试用用户脚本：最小 stdio MCP echo server（含标记便于校验拷贝）。
const echoUserScript = `#!/usr/bin/env python3
# user-echo-marker
import json, sys

def make_result(i, r): return {"jsonrpc": "2.0", "id": i, "result": r}
def make_error(i, c, m): return {"jsonrpc": "2.0", "id": i, "error": {"code": c, "message": m}}

def tools_list():
    return [{"name": "echo", "description": "回显",
             "inputSchema": {"type": "object", "properties": {"message": {"type": "string"}}, "required": ["message"]}}]

def tools_call(n, a):
    if n == "echo":
        return {"content": [{"type": "text", "text": a.get("message", "")}]}
    return {"isError": True, "content": [{"type": "text", "text": "unknown"}]}

for line in sys.stdin:
    line = line.strip()
    if not line: continue
    req = json.loads(line)
    i = req.get("id"); m = req.get("method"); p = req.get("params", {}) or {}
    if m == "initialize":
        r = make_result(i, {"protocolVersion": "2024-11-05", "capabilities": {"tools": {}}, "serverInfo": {"name": "user-echo", "version": "1.0.0"}})
    elif m == "notifications/initialized":
        continue
    elif m == "tools/list":
        r = make_result(i, {"tools": tools_list()})
    elif m == "tools/call":
        r = make_result(i, tools_call(p.get("name"), p.get("arguments", {})))
    else:
        r = make_error(i, -32601, "not found")
    sys.stdout.write(json.dumps(r) + "\n"); sys.stdout.flush()
`

// TestModuleService_WriteUserModule E2E：/mcp/generate 全链路（阶段 E3）。
// 探测用户脚本 -> 写 module.json+main.py 到 marketplace home -> rescan 注册 -> autostart 在线
// -> supervisor 探活触发 DeriveSKUs 出 echo SKU。并验证官方模块防覆盖。
func TestModuleService_WriteUserModule(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python 不在 PATH，跳过 stdio E2E 测试")
	}
	// 隔离 marketplace root（EnsureMarketplaceRoot 会 SeedOfficial 播种官方模块文件）
	root := t.TempDir()
	t.Setenv("CLAW_MARKETPLACE_DIR", root)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1) // 单连接：supervisor 后台探活写库与断言共用同一 in-memory DB
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}, &model.AgentItem{}))
	skillRuntimeRepo := repository.NewSkillRuntimeRepo(db)
	agentRepo := repository.NewAgentRepo(db)
	registry := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	registry.SetRepo(skillRuntimeRepo)
	mcpStdio := NewMCPStdioProtocol(nil)
	registry.SetMCPStdioProtocol(mcpStdio)
	manager := NewSkillRuntimeManager(registry, zap.NewNop())
	manager.SetMCPStdioProtocol(mcpStdio)
	manager.SetSKUService(NewSkillRuntimeSKUService(agentRepo, nil))
	svc := NewModuleService(registry, manager, skillRuntimeRepo, agentRepo)

	// 用户脚本目录
	userScriptDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(userScriptDir, "main.py"), []byte(echoUserScript), 0o644))

	// 探测用户脚本（模拟 handler 的 ProbeStdio）
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tools, err := mcpStdio.ProbeStdio(ctx, "python", []string{"main.py"}, nil, userScriptDir)
	require.NoError(t, err)
	require.NotEmpty(t, tools)

	// 一键生成（module_id 缺省，据 name 推导为 my-echo-tool-<uuid8>，T6 加消歧后缀）
	result, err := svc.WriteUserModule(UserModuleGenerateRequest{
		Name:        "My Echo Tool",
		Description: "测试用户模块",
		Command:     "python",
		Args:        []string{"main.py"},
		WorkDir:     userScriptDir,
	}, tools)
	require.NoError(t, err)
	// T6：新生成模块 ID = slug + uuid8 后缀，使重名模块不撞；后续断言用实际 ID 而非硬编码
	assert.True(t, strings.HasPrefix(result.ModuleID, "my-echo-tool-"), "got %s", result.ModuleID)
	moduleID := result.ModuleID
	defer manager.Stop(moduleID)

	// module.json + main.py 落盘
	moduleDir := filepath.Join(root, moduleID)
	require.FileExists(t, filepath.Join(moduleDir, "module.json"))
	require.FileExists(t, filepath.Join(moduleDir, "main.py"))
	// main.py 是用户脚本内容（含标记）而非骨架
	data, err := os.ReadFile(filepath.Join(moduleDir, "main.py"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "user-echo-marker")
	// module.json 含 auto_sku + mcp_stdio
	mj, err := os.ReadFile(filepath.Join(moduleDir, "module.json"))
	require.NoError(t, err)
	assert.Contains(t, string(mj), "\"auto_sku\": true")
	assert.Contains(t, string(mj), "\"transport\": \"mcp_stdio\"")

	// rescan 注册了 SkillRuntime（AutoSKU + DriverID）
	rt, err := skillRuntimeRepo.GetByID(moduleID)
	require.NoError(t, err)
	assert.True(t, rt.AutoSKU)
	assert.Equal(t, moduleID, rt.DriverID)

	// autostart -> 在线 -> DeriveSKUs 出 echo SKU
	waitOnline(t, registry, moduleID, 10*time.Second)
	deadline := time.Now().Add(15 * time.Second)
	var echo *model.AgentItem
	for time.Now().Before(deadline) {
		if it, err := agentRepo.GetByID(moduleID + "-echo"); err == nil && it != nil {
			echo = it
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotNil(t, echo, "echo SKU 未自动派生")

	// main_py_content 优先：即便 work_dir 有同名脚本，web 编辑器草稿内容也应胜出落盘。
	draftScript := "#!/usr/bin/env python3\n# drafted-content-marker\nimport sys\nsys.exit(0)\n"
	result2, err := svc.WriteUserModule(UserModuleGenerateRequest{
		Name:          "Drafted Tool",
		Command:       "python",
		Args:          []string{"main.py"},
		WorkDir:       userScriptDir, // 此目录已有 echoUserScript 的 main.py
		MainPyContent: draftScript,
	}, tools)
	require.NoError(t, err)
	defer manager.Stop(result2.ModuleID)
	// T6：drafted-tool-<uuid8>
	assert.True(t, strings.HasPrefix(result2.ModuleID, "drafted-tool-"), "got %s", result2.ModuleID)
	d2, err := os.ReadFile(filepath.Join(root, result2.ModuleID, "main.py"))
	require.NoError(t, err)
	assert.Contains(t, string(d2), "drafted-content-marker")
	assert.NotContains(t, string(d2), "user-echo-marker", "草稿应优先于 work_dir 脚本拷贝")

	// 官方模块防覆盖：firecrawl 经 rescan 已注册为 Official，禁止覆盖
	_, err = svc.WriteUserModule(UserModuleGenerateRequest{
		ModuleID: "firecrawl", Name: "x", WorkDir: userScriptDir,
	}, tools)
	assert.Error(t, err, "应禁止覆盖官方模块")
}

// TestStripCodeFences 验证剥离模型输出可能的 markdown 代码围栏。
func TestStripCodeFences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"无围栏原样返回", "print('hi')\n", "print('hi')"},
		{"python 围栏", "```python\nprint('hi')\n```", "print('hi')"},
		{"裸围栏", "```\nprint('hi')\n```", "print('hi')"},
		{"带前后空白", "  ```python\nx=1\n```  ", "x=1"},
		{"只有开头围栏不匹配", "```python\nx=1", "x=1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, stripCodeFences(c.in))
		})
	}
}

// TestBuildDraftMainPyUserPrompt 验证起草 user prompt 含能力描述、启动方式与凭证->环境变量映射。
func TestBuildDraftMainPyUserPrompt(t *testing.T) {
	req := DraftMainPyRequest{
		CapabilityDescription: "翻译工具：暴露 translate 工具，中文转英文",
		Command:               "python",
		Args:                  []string{"main.py"},
		CredentialsMeta: map[string]model.CredentialDef{
			"firecrawl_api_key": {Type: "api_key", Label: "Firecrawl Key"},
		},
	}
	out := buildDraftMainPyUserPrompt(req)
	assert.Contains(t, out, "翻译工具")
	assert.Contains(t, out, "python main.py")
	assert.Contains(t, out, "firecrawl_api_key")
	assert.Contains(t, out, "FIRECRAWL_API_KEY")
	assert.Contains(t, out, "os.environ.get('FIRECRAWL_API_KEY')")

	// 无凭证时标注无需读取
	out2 := buildDraftMainPyUserPrompt(DraftMainPyRequest{CapabilityDescription: "纯本地能力"})
	assert.Contains(t, out2, "无需读取任何凭证")
}

// TestDraftMainPy_Errors 验证 DraftMainPy 的前置校验（不触达真实 LLM）。
func TestDraftMainPy_Errors(t *testing.T) {
	svc := &ModuleService{} // chatService 为 nil
	_, err := svc.DraftMainPy(context.Background(), DraftMainPyRequest{CapabilityDescription: "x"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "对话服务未初始化")

	// 注入一个 nil chatService 占位以越过 chatService==nil 分支，验证能力描述/模型校验
	svc.chatService = &ChatProxyService{}
	_, err = svc.DraftMainPy(context.Background(), DraftMainPyRequest{Provider: "eleagent", Model: "qwen/x"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "能力描述")

	_, err = svc.DraftMainPy(context.Background(), DraftMainPyRequest{CapabilityDescription: "x", Provider: "eleagent"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "模型")
}

// TestTestCall_Errors 验证 TestCall 前置校验（不依赖在线进程）。
func TestTestCall_Errors(t *testing.T) {
	// 无 registry
	svc := &ModuleService{}
	_, err := svc.TestCall(context.Background(), "m", TestCallRequest{ToolName: "t"}, "u")
	assert.Error(t, err)

	// 有 registry 但模块不存在
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))
	repo := repository.NewSkillRuntimeRepo(db)
	registry := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	registry.SetRepo(repo)
	svc2 := &ModuleService{registry: registry}
	_, err = svc2.TestCall(context.Background(), "no-such-module", TestCallRequest{ToolName: "t"}, "u")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "模块不存在")

	// tool_name 为空
	_, err = svc2.TestCall(context.Background(), "m", TestCallRequest{}, "u")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tool_name")
}

// TestProbeHeaders 验证 G3：probeHeaders 提取 MCPServerConfig 中的字面量请求头供 mcp_http 探活，
// 跳过 ${credentials.KEY} 模板头（探活无凭证上下文），nil/空配置返回 nil。
// 模板头模块（agent-reach）tools/list 本不鉴权，跳过无影响；字面量头（远端 MCP 鉴权）原样发送。
func TestProbeHeaders(t *testing.T) {
	// 无 MCPServerConfig
	rt := &model.SkillRuntime{}
	assert.Nil(t, probeHeaders(rt))

	// 空 Headers
	rt.SetMCPServerConfig(&model.MCPServerConfig{URL: "http://x"})
	assert.Nil(t, probeHeaders(rt))

	// 纯模板头（agent-reach 式）-> 全跳过 -> nil
	rt.SetMCPServerConfig(&model.MCPServerConfig{
		URL: "http://x",
		Headers: map[string]string{
			"X-Twitter-Cookie": "${credentials.twitter_cookie}",
			"X-Github-Token":   "${credentials.github_token}",
		},
	})
	assert.Nil(t, probeHeaders(rt))

	// 字面量头（G3 远端 MCP 鉴权）-> 原样发送
	rt.SetMCPServerConfig(&model.MCPServerConfig{
		URL: "http://x",
		Headers: map[string]string{
			"Authorization": "Bearer abc123",
			"X-Api-Key":     "literal-key",
		},
	})
	h := probeHeaders(rt)
	require.NotNil(t, h)
	assert.Equal(t, "Bearer abc123", h["Authorization"])
	assert.Equal(t, "literal-key", h["X-Api-Key"])

	// 混合：字面量保留，模板跳过
	rt.SetMCPServerConfig(&model.MCPServerConfig{
		URL: "http://x",
		Headers: map[string]string{
			"Authorization":    "Bearer abc123",
			"X-Twitter-Cookie": "${credentials.twitter_cookie}",
		},
	})
	h = probeHeaders(rt)
	require.NotNil(t, h)
	assert.Len(t, h, 1)
	assert.Equal(t, "Bearer abc123", h["Authorization"])
}

// TestInstallMCPRuntime_HTTP 验证 G3：动态安装远端 http MCP server（Smithery 式）。
// 桩 MCP server 返回 2 个工具 -> InstallMCPRuntime 建 source=mcp_remote 运行时
// -> ForceProbe 探活（probeHeaders 发送字面量鉴权头，缺失则 401）-> DeriveSKUs 派生 2 SKU。
// 自驱动路由回溯：ResolveDriver(driverID) 经 GetByDriverID 找到本运行时。
func TestInstallMCPRuntime_HTTP(t *testing.T) {
	// 桩 MCP server：处理 initialize / notifications/initialized / tools/list，强制鉴权头
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req mcpRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"test","version":"1.0"}}`),
			})
		case "notifications/initialized":
			_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: json.RawMessage(`{"tools":[{"name":"scrape","description":"scrape url","inputSchema":{"type":"object"}},{"name":"crawl","description":"crawl site","inputSchema":{"type":"object"}}]}`),
			})
		}
	}))
	defer srv.Close()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	// 单连接：sqlite :memory: 每连接独立 DB，多模型 AutoMigrate + 跨表查询需固定单连接
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}, &model.AgentItem{}))

	skillRuntimeRepo := repository.NewSkillRuntimeRepo(db)
	agentRepo := repository.NewAgentRepo(db)
	registry := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	registry.SetRepo(skillRuntimeRepo)
	registry.SetSKUService(NewSkillRuntimeSKUService(agentRepo, zap.NewNop()))
	manager := NewSkillRuntimeManager(registry, zap.NewNop())
	svc := NewModuleService(registry, manager, skillRuntimeRepo, agentRepo)

	// handler 侧一次性探测（模拟 InstallMCP handler 的 probe 步骤）
	tools, err := NewMCPHTTPProtocol(nil).ListTools(context.Background(), srv.URL, map[string]string{"Authorization": "Bearer test-token"})
	require.NoError(t, err)
	require.Len(t, tools, 2)

	result, err := svc.InstallMCPRuntime(&MCPInstallRequest{
		Transport: "mcp_http",
		Name:      "Remote Test",
		Endpoint:  srv.URL,
		Headers:   map[string]string{"Authorization": "Bearer test-token"},
	}, tools)
	require.NoError(t, err)
	require.NotNil(t, result)
	// T6：mcp-remote-remote-test-<uuid8>
	assert.True(t, strings.HasPrefix(result.RuntimeID, "mcp-remote-remote-test-"), "got %s", result.RuntimeID)
	assert.Equal(t, result.RuntimeID, result.DriverID)
	assert.Equal(t, "mcp_http", result.Transport)
	assert.Equal(t, 2, result.SKUCount)

	// 运行时持久化校验
	rt, err := skillRuntimeRepo.GetByID(result.RuntimeID)
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Equal(t, model.SkillRuntimeSourceMCPRemote, rt.Source)
	assert.Equal(t, model.SkillRuntimeTransportMCPHTTP, rt.Transport)
	assert.Equal(t, model.SkillRuntimeDeploymentExternal, rt.Deployment)
	assert.True(t, rt.AutoSKU)
	assert.Equal(t, result.RuntimeID, rt.DriverID)
	assert.Equal(t, srv.URL, rt.Endpoint)
	cfg := rt.GetMCPServerConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "Bearer test-token", cfg.Headers["Authorization"])

	// 自驱动路由回溯
	drv, err := svc.ResolveDriver(result.DriverID)
	require.NoError(t, err)
	assert.Equal(t, string(model.ModuleTransportTypeMCP), drv.TransportType)

	// 2 SKU 派生到 agent_items
	skus, err := agentRepo.ListByModuleSKUs(result.RuntimeID)
	require.NoError(t, err)
	assert.Len(t, skus, 2)
}
