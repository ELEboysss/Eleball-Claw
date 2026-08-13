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

// TestModuleService_RescanMarketplace_MCP 验证 marketplace 扫描能识别 transport=mcp_http 的示例模块，
// 并创建正确的 SkillRuntime（含 MCPServerConfig）与驱动别名映射。
func TestModuleService_RescanMarketplace_MCP(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))

	skillRuntimeRepo := repository.NewSkillRuntimeRepo(db)
	registry := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	registry.SetRepo(skillRuntimeRepo)
	manager := NewSkillRuntimeManager(registry, zap.NewNop())
	svc := NewModuleService(registry, manager, skillRuntimeRepo, nil)

	root := t.TempDir()
	mcpDir := filepath.Join(root, "mcp-hello")
	require.NoError(t, os.MkdirAll(mcpDir, 0755))
	manifest := []byte(`{
  "id": "mcp-hello",
  "name": "MCP Hello",
  "description": "test",
  "origin": "cloud",
  "transport": "mcp_http",
  "deployment": "external",
  "capabilities": ["hello"],
  "mcp_server_config": {"url": "http://mcp-hello:8080/mcp"},
  "driver": {"driver_id": "mcp_hello", "name": "MCP Hello Driver"}
}`)
	require.NoError(t, os.WriteFile(filepath.Join(mcpDir, "module.json"), manifest, 0644))

	// claw RescanPackage 经包级 ResolveMarketplaceRoot 解析根（CLAW_MARKETPLACE_DIR）
	t.Setenv("CLAW_MARKETPLACE_DIR", root)
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))

	rt, err := svc.repo.GetByID("mcp-hello")
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Equal(t, model.SkillRuntimeTransportMCPHTTP, rt.Transport)
	// rt.Endpoint 保留 mcp_server_config.url 完整 path（/mcp 不被剥光），网关据此 POST JSON-RPC
	assert.Equal(t, "http://mcp-hello:8080/mcp", rt.Endpoint)
	assert.True(t, rt.Official)
	assert.Equal(t, "mcp_hello", rt.DriverID)
	cfg := rt.GetMCPServerConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "http://mcp-hello:8080/mcp", cfg.URL)

	drv, err := svc.ResolveDriver("mcp_hello")
	require.NoError(t, err)
	require.NotNil(t, drv)
	assert.Equal(t, model.SkillRuntimeTransportMCPHTTP, drv.Transport)
	cfg = drv.GetMCPServerConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "http://mcp-hello:8080/mcp", cfg.URL)
}

// TestModuleService_RescanMarketplace_AgentReachMCP 验证 T5.1：agent-reach 官方模块 package 化后
// （package.json + auto_sku + .origin=cloud + docker-compose.claw.yml），claw 侧 rescan 按 side=claw 物化——
// MCP http 运行时 Deployment=docker、Endpoint 取 hostUrl（127.0.0.1:8094，宿主机可达）、auto_sku=true、
// DockerComposePath 指向 docker-compose.claw.yml；headers 含 6 个 ${credentials.KEY} 凭证模板、credentials
// 透传 6 个 module 级凭证；auto_sku=true 跳过通用 {pkg}-mcp-{key} SKU（由 DeriveSKUs 派生逐工具 SKU）。
// 写 fixture（package.json + .origin=cloud + docker-compose.claw.yml），不读真实 marketplace 目录。
func TestModuleService_RescanMarketplace_AgentReachMCP(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))
	skillRuntimeRepo := repository.NewSkillRuntimeRepo(db)
	registry := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	registry.SetRepo(skillRuntimeRepo)
	manager := NewSkillRuntimeManager(registry, zap.NewNop())
	svc := NewModuleService(registry, manager, skillRuntimeRepo, nil)

	root := t.TempDir()
	modDir := filepath.Join(root, "agent-reach")
	require.NoError(t, os.MkdirAll(modDir, 0755))
	// 云端下发的 package 布局：package.json + .origin=cloud + docker-compose.claw.yml
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "package.json"), []byte(agentReachFixturePackageJSON), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, ".origin"), []byte("cloud"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "docker-compose.claw.yml"), []byte("version: '3'\nservices:\n  agent-reach:\n    image: example.registry/eleball/agent-reach:develop\n"), 0644))

	// claw RescanPackage 经包级 ResolveMarketplaceRoot 解析根（CLAW_MARKETPLACE_DIR）
	t.Setenv("CLAW_MARKETPLACE_DIR", root)
	require.NoError(t, svc.RescanPackage("claw", zap.NewNop()))

	// package 布局运行时 ID={pkg}-mcp-{key}=agent-reach-mcp-main（非目录名）
	rt, err := svc.repo.GetByID("agent-reach-mcp-main")
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Equal(t, model.SkillRuntimeTransportMCPHTTP, rt.Transport)
	assert.Equal(t, model.SkillRuntimeDeploymentDocker, rt.Deployment, "claw 侧 http MCP 应 docker 部署（云端下载包由 claw 拉起容器）")
	assert.True(t, rt.AutoSKU, "auto_sku 应为 true（免手写 SKU，探活后 DeriveSKUs 派生逐工具 SKU）")
	// Endpoint 取 hostUrl（claw 宿主机可达；云端 DNS 地址 http://agent-reach:8080 对宿主机不可达）
	assert.Equal(t, "http://127.0.0.1:8094", rt.Endpoint)
	assert.True(t, rt.Official)
	// DockerComposePath 指向 docker-compose.claw.yml（ACR 镜像 + 发布端口版）
	require.NotEmpty(t, rt.DockerComposePath, "claw 侧 http MCP 应带 DockerComposePath（Start 据此拉起容器）")
	assert.True(t, strings.HasSuffix(rt.DockerComposePath, "docker-compose.claw.yml"), rt.DockerComposePath)

	cfg := rt.GetMCPServerConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "http://127.0.0.1:8094", cfg.URL)
	// 6 个凭证请求头模板（${credentials.KEY} 由网关 prepareMCPHeaders 替换为值后注入）
	require.Len(t, cfg.Headers, 6)
	assert.Equal(t, "${credentials.twitter_cookie}", cfg.Headers["X-Twitter-Cookie"])
	assert.Equal(t, "${credentials.bilibili_cookie}", cfg.Headers["X-Bilibili-Cookie"])

	// 6 个 module 级凭证声明（同模块多 SKU 共享 module:agent_reach 桶）
	creds := rt.CredentialsMap()
	require.Len(t, creds, 6)
	require.Contains(t, creds, "twitter_cookie")
	assert.Equal(t, model.CredentialScopeModule, creds["bilibili_cookie"].Scope)
}

// agentReachFixturePackageJSON 云端下发的 agent-reach package.json（T5.1 author 版）测试 fixture。
// 与 gateway/marketplace/agent-reach/package.json 保持字段对齐：auto_sku=true、hostUrl=127.0.0.1:8094、
// 6 个 ${credentials.KEY} 请求头模板 + 6 个凭证声明（github_token + 5 cookie，均 required=false）。
const agentReachFixturePackageJSON = `{
  "name": "agent-reach",
  "version": "2.0.0",
  "description": "网页阅读、搜索、视频字幕、GitHub、社交平台等",
  "author": "eleball",
  "category": "互联网",
  "level": 1,
  "auto_sku": true,
  "mcpServers": {
    "main": {
      "transport": "http",
      "url": "http://agent-reach:8080",
      "hostUrl": "http://127.0.0.1:8094",
      "headers": {
        "X-Twitter-Cookie": "${credentials.twitter_cookie}",
        "X-Reddit-Cookie": "${credentials.reddit_cookie}",
        "X-Xiaohongshu-Cookie": "${credentials.xiaohongshu_cookie}",
        "X-Bilibili-Cookie": "${credentials.bilibili_cookie}",
        "X-YouTube-Cookie": "${credentials.youtube_cookie}",
        "X-Github-Token": "${credentials.github_token}"
      },
      "credentials": {
        "github_token": {
          "type": "api_key",
          "label": "GitHub Token",
          "required": false
        },
        "twitter_cookie": {
          "type": "cookie",
          "label": "Twitter Cookie",
          "required": false
        },
        "reddit_cookie": {
          "type": "cookie",
          "label": "Reddit Cookie",
          "required": false
        },
        "xiaohongshu_cookie": {
          "type": "cookie",
          "label": "小红书 Cookie",
          "required": false
        },
        "bilibili_cookie": {
          "type": "cookie",
          "label": "B站 Cookie",
          "required": false
        },
        "youtube_cookie": {
          "type": "cookie",
          "label": "YouTube Cookie",
          "required": false
        }
      }
    }
  }
}`

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

// TestModuleService_WriteUserModule E2E：/mcp/generate 全链路（T3.1 秘技包）。
// 探测用户脚本 -> 写 package.json+main.py+.origin 到 marketplace home -> RescanPackage 注册
// 运行时 {pkg}-mcp-main + 派生 MCP SKU -> autostart 在线 -> TestCall 试跑返回结果。并验证官方模块防覆盖。
func TestModuleService_WriteUserModule(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python 不在 PATH，跳过 stdio E2E 测试")
	}
	// 隔离 marketplace root（EnsureMarketplaceRoot 会 SeedOfficial 播种官方模块文件）
	root := t.TempDir()
	t.Setenv("CLAW_MARKETPLACE_DIR", root)

	// T5.1：官方模块全部 package 化（云端下载，不再内嵌）。写 firecrawl 官方包 fixture，
	// 使「官方模块防覆盖」有可判定的目录（package 布局 registry.Get("firecrawl") 取不到，
	// WriteUserModule 改按 packageDirOrigin 推断）。WriteUserModule 每次内部 RescanPackage 会扫到。
	fcDir := filepath.Join(root, "firecrawl")
	require.NoError(t, os.MkdirAll(fcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fcDir, "package.json"), []byte(`{"name":"firecrawl","version":"2.0.0","description":"firecrawl","category":"抓取","level":1,"auto_sku":true,"mcpServers":{"main":{"transport":"http","url":"http://firecrawl:8080","hostUrl":"http://127.0.0.1:8095"}}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(fcDir, ".origin"), []byte("cloud"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(fcDir, "docker-compose.claw.yml"), []byte("version: '3'\nservices:\n  firecrawl:\n    image: example.registry/eleball/firecrawl:develop\n"), 0o644))

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
		Version:     "1.2.3",
		Category:    "utility",
		Command:     "python",
		Args:        []string{"main.py"},
		WorkDir:     userScriptDir,
	}, tools)
	require.NoError(t, err)
	// T6：新生成模块 ID = slug + uuid8 后缀，使重名模块不撞；后续断言用实际 ID 而非硬编码
	assert.True(t, strings.HasPrefix(result.ModuleID, "my-echo-tool-"), "got %s", result.ModuleID)
	moduleID := result.ModuleID
	runtimeID := result.RuntimeID
	assert.Equal(t, moduleID+"-mcp-main", runtimeID, "运行时 ID = {pkg}-mcp-main")
	defer manager.Stop(runtimeID)

	// package.json + main.py + .origin 落盘（T3.1 秘技包布局）
	moduleDir := filepath.Join(root, moduleID)
	require.FileExists(t, filepath.Join(moduleDir, "package.json"))
	require.FileExists(t, filepath.Join(moduleDir, "main.py"))
	require.FileExists(t, filepath.Join(moduleDir, ".origin"))
	// main.py 是用户脚本内容（含标记）而非骨架
	data, err := os.ReadFile(filepath.Join(moduleDir, "main.py"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "user-echo-marker")
	// .origin 侧车 = user（package.json 不声明 origin 防伪造，T1.3）
	originBytes, err := os.ReadFile(filepath.Join(moduleDir, ".origin"))
	require.NoError(t, err)
	assert.Equal(t, "user", strings.TrimSpace(string(originBytes)))
	// package.json 含 mcpServers.main（stdio）+ 版本/分类透传
	pj, err := os.ReadFile(filepath.Join(moduleDir, "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(pj), "\"mcpServers\"")
	assert.Contains(t, string(pj), "\"transport\": \"stdio\"")
	assert.Contains(t, string(pj), "\"version\": \"1.2.3\"")
	assert.Contains(t, string(pj), "\"category\": \"utility\"")
	// 结果透出 runtime/sku ID = {pkg}-mcp-main
	assert.Equal(t, runtimeID, result.RuntimeID)
	assert.Equal(t, runtimeID, result.SKUID)

	// rescan 注册了运行时 {pkg}-mcp-main（mcp_stdio/process，WorkDir=模块目录）
	rt, err := skillRuntimeRepo.GetByID(runtimeID)
	require.NoError(t, err)
	assert.Equal(t, runtimeID, rt.DriverID)
	assert.Equal(t, model.SkillRuntimeTransportMCPStdio, rt.Transport)
	assert.Equal(t, model.SkillRuntimeDeploymentProcess, rt.Deployment)
	assert.Equal(t, "python", rt.Command)
	assert.Equal(t, moduleDir, rt.WorkDir)
	assert.Equal(t, "1.2.3", rt.Version)

	// WriteUserModule 内同步 RescanPackage -> 派生 MCP SKU {pkg}-mcp-main（非 auto_sku 派生）
	sku, err := agentRepo.GetByID(runtimeID)
	require.NoError(t, err)
	skm, err := sku.Manifest()
	require.NoError(t, err)
	assert.Equal(t, "mcp", skm.Metadata["package_derived"])

	// autostart -> 在线
	waitOnline(t, registry, runtimeID, 10*time.Second)

	// 试跑（T3.1 验收）：TestCall 直接调用 echo 工具返回结果
	testCtx, cancelTest := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelTest()
	tres, err := svc.TestCall(testCtx, runtimeID, TestCallRequest{ToolName: "echo", Arguments: map[string]interface{}{"message": "hi"}}, "u1")
	require.NoError(t, err)
	tcontent, _ := tres["content"].([]interface{})
	require.NotEmpty(t, tcontent)

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
	defer manager.Stop(result2.RuntimeID)
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
	assert.Equal(t, model.SkillRuntimeTransportMCPHTTP, drv.Transport)

	// 2 SKU 派生到 agent_items
	skus, err := agentRepo.ListByModuleSKUs(result.RuntimeID)
	require.NoError(t, err)
	assert.Len(t, skus, 2)
}

// TestParseMCPConfig 验证标准 MCP 配置解析（M4）：Claude Desktop / Cursor / .mcp.json 通用格式，
// 有 url -> mcp_http，有 command -> mcp_stdio，两者皆无跳过；Name 取 key；不认识字段忽略不报错。
func TestParseMCPConfig(t *testing.T) {
	raw := []byte(`{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
      "env": {"FOO": "bar"}
    },
    "git": {
      "command": "uvx",
      "args": ["mcp-server-git", "--repository", "/repo"]
    },
    "remote-api": {
      "url": "https://mcp.example.com/mcp",
      "headers": {"Authorization": "Bearer xxx"}
    },
    "empty-entry": {},
    "type-only": {"type": "stdio", "alwaysAllow": ["fs"]}
  }
}`)
	reqs, err := ParseMCPConfig(raw)
	require.NoError(t, err)
	require.Len(t, reqs, 3, "empty-entry 与 type-only（无 command/url）应跳过，剩 2 stdio + 1 http")

	// map 迭代顺序不确定，按 Name 归类校验
	byName := map[string]*MCPInstallRequest{}
	for _, r := range reqs {
		byName[r.Name] = r
	}

	fs := byName["filesystem"]
	require.NotNil(t, fs)
	assert.Equal(t, "mcp_stdio", fs.Transport)
	assert.Equal(t, "npx", fs.Command)
	assert.Equal(t, []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}, fs.Args)
	assert.Equal(t, map[string]string{"FOO": "bar"}, fs.Env)

	git := byName["git"]
	require.NotNil(t, git)
	assert.Equal(t, "mcp_stdio", git.Transport)
	assert.Equal(t, "uvx", git.Command)
	assert.Equal(t, []string{"mcp-server-git", "--repository", "/repo"}, git.Args)

	remote := byName["remote-api"]
	require.NotNil(t, remote)
	assert.Equal(t, "mcp_http", remote.Transport)
	assert.Equal(t, "https://mcp.example.com/mcp", remote.Endpoint)
	assert.Equal(t, map[string]string{"Authorization": "Bearer xxx"}, remote.Headers)

	// type-only 条目有 type 但无 command/url，应被跳过（不出现在结果中）
	_, hasTypeOnly := byName["type-only"]
	assert.False(t, hasTypeOnly, "无 command/url 的条目应跳过")
	_, hasEmpty := byName["empty-entry"]
	assert.False(t, hasEmpty, "空条目应跳过")

	// 无效 JSON 报错
	_, err = ParseMCPConfig([]byte(`{not json`))
	assert.Error(t, err)

	// 空 mcpServers 返回空切片（不报错）
	reqs, err = ParseMCPConfig([]byte(`{"mcpServers": {}}`))
	require.NoError(t, err)
	assert.Empty(t, reqs)
}
