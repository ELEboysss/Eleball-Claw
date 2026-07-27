package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockRunner 跨平台运行器桩
type mockRunner struct {
	ocrResult     string
	ocrErr        error
	shellOutput   string
	shellErr      error
	lastOCRImage  string
	lastShellCmd  string
	lastShellArgs []string
}

func (m *mockRunner) OCR(ctx context.Context, imagePath string) (string, error) {
	m.lastOCRImage = imagePath
	return m.ocrResult, m.ocrErr
}

func (m *mockRunner) Shell(ctx context.Context, command string, args []string, cwd string) (string, error) {
	m.lastShellCmd = command
	m.lastShellArgs = args
	return m.shellOutput, m.shellErr
}

// mockSearchProvider 搜索提供者桩
type mockSearchProvider struct {
	result map[string]interface{}
	err    error
	query  string
}

func (m *mockSearchProvider) Name() string { return "mock" }
func (m *mockSearchProvider) Search(ctx context.Context, query string) (map[string]interface{}, error) {
	m.query = query
	return m.result, m.err
}

func TestToolRegistry_ReadWriteFile(t *testing.T) {
	base := t.TempDir()
	sandbox := NewFileSandbox(base, "")
	runner := &mockRunner{}
	search := &mockSearchProvider{result: map[string]interface{}{"results": []string{}}}
	registry := NewToolRegistryWithDeps(runner, search)

	env := &ToolEnv{
		UserID:         "u1",
		ConversationID: "c1",
		SessionID:      "s1",
		Sandbox:        sandbox,
	}

	// WriteFile
	writeTool, _ := registry.Get("WriteFile")
	out, err := writeTool.Func(context.Background(), map[string]interface{}{
		"path":    "hello.txt",
		"content": "hello world",
	}, env)
	if err != nil {
		t.Fatalf("WriteFile 失败: %v", err)
	}
	if out["written"] != true {
		t.Fatalf("WriteFile 未返回 written=true")
	}

	// ReadFile
	readTool, _ := registry.Get("ReadFile")
	out, err = readTool.Func(context.Background(), map[string]interface{}{"path": "hello.txt"}, env)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	if out["content"] != "hello world" {
		t.Fatalf("ReadFile 内容不对: %v", out["content"])
	}
}

func TestToolRegistry_StrReplaceFile(t *testing.T) {
	base := t.TempDir()
	sandbox := NewFileSandbox(base, "")
	runner := &mockRunner{}
	search := &mockSearchProvider{}
	registry := NewToolRegistryWithDeps(runner, search)
	env := &ToolEnv{UserID: "u1", ConversationID: "c1", Sandbox: sandbox}

	writeTool, _ := registry.Get("WriteFile")
	writeTool.Func(context.Background(), map[string]interface{}{"path": "doc.txt", "content": "foo bar baz"}, env)

	replaceTool, _ := registry.Get("StrReplaceFile")
	out, err := replaceTool.Func(context.Background(), map[string]interface{}{
		"path":       "doc.txt",
		"old_string": "bar",
		"new_string": "qux",
	}, env)
	if err != nil {
		t.Fatalf("StrReplaceFile 失败: %v", err)
	}
	if out["modified"] != true {
		t.Fatalf("StrReplaceFile 未返回 modified=true")
	}

	readTool, _ := registry.Get("ReadFile")
	out, _ = readTool.Func(context.Background(), map[string]interface{}{"path": "doc.txt"}, env)
	if out["content"] != "foo qux baz" {
		t.Fatalf("替换后内容不对: %v", out["content"])
	}
}

func TestToolRegistry_ShellSafety(t *testing.T) {
	base := t.TempDir()
	sandbox := NewFileSandbox(base, "")
	runner := &mockRunner{shellOutput: "ok"}
	search := &mockSearchProvider{}
	registry := NewToolRegistryWithDeps(runner, search)
	env := &ToolEnv{UserID: "u1", ConversationID: "c1", Sandbox: sandbox}

	shellTool, _ := registry.Get("Shell")
	_, err := shellTool.Func(context.Background(), map[string]interface{}{
		"command": "ls",
		"args":    []interface{}{"-la", "; rm -rf /"},
	}, env)
	if err == nil {
		t.Fatal("Shell 应拦截危险参数")
	}
	if !strings.Contains(err.Error(), "非法字符") {
		t.Fatalf("错误提示不对: %v", err)
	}

	out, err := shellTool.Func(context.Background(), map[string]interface{}{
		"command": "echo",
		"args":    []interface{}{"hello"},
	}, env)
	if err != nil {
		t.Fatalf("Shell 正常执行失败: %v", err)
	}
	if out["output"] != "ok" {
		t.Fatalf("Shell 输出不对: %v", out["output"])
	}
}

func TestToolRegistry_SearchWeb(t *testing.T) {
	runner := &mockRunner{}
	search := &mockSearchProvider{result: map[string]interface{}{"results": []string{"r1"}}}
	registry := NewToolRegistryWithDeps(runner, search)
	registry.RegisterBuiltinSearchWeb()
	env := &ToolEnv{}

	searchTool, _ := registry.Get("SearchWeb")
	out, err := searchTool.Func(context.Background(), map[string]interface{}{"query": "golang"}, env)
	if err != nil {
		t.Fatalf("SearchWeb 失败: %v", err)
	}
	if search.query != "golang" {
		t.Fatalf("搜索词未传递: %v", search.query)
	}
	if len(out["results"].([]string)) != 1 {
		t.Fatalf("搜索结果不对: %v", out["results"])
	}
}

func TestToolRegistry_OCR(t *testing.T) {
	base := t.TempDir()
	sandbox := NewFileSandbox(base, "")
	imgPath := filepath.Join(base, "u1", "conversations", "c1", "img.png")
	if err := os.MkdirAll(filepath.Dir(imgPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgPath, []byte("image"), 0644); err != nil {
		t.Fatal(err)
	}

	runner := &mockRunner{ocrResult: "识别结果"}
	search := &mockSearchProvider{}
	registry := NewToolRegistryWithDeps(runner, search)
	env := &ToolEnv{UserID: "u1", ConversationID: "c1", Sandbox: sandbox}

	ocrTool, _ := registry.Get("OCR")
	out, err := ocrTool.Func(context.Background(), map[string]interface{}{"path": "img.png"}, env)
	if err != nil {
		t.Fatalf("OCR 失败: %v", err)
	}
	if out["text"] != "识别结果" {
		t.Fatalf("OCR 结果不对: %v", out["text"])
	}
	if runner.lastOCRImage != imgPath {
		t.Fatalf("OCR 调用路径不对: %v", runner.lastOCRImage)
	}
}

func TestToolRegistry_Grep(t *testing.T) {
	base := t.TempDir()
	sandbox := NewFileSandbox(base, "")
	runner := &mockRunner{}
	search := &mockSearchProvider{}
	registry := NewToolRegistryWithDeps(runner, search)
	env := &ToolEnv{UserID: "u1", ConversationID: "c1", Sandbox: sandbox}

	// 先写入测试文件
	writeTool, _ := registry.Get("WriteFile")
	_, err := writeTool.Func(context.Background(), map[string]interface{}{
		"path":    "test.txt",
		"content": "hello world\nfoo bar\nhello golang",
	}, env)
	if err != nil {
		t.Fatalf("写测试文件失败: %v", err)
	}

	grepTool, _ := registry.Get("Grep")
	out, err := grepTool.Func(context.Background(), map[string]interface{}{
		"path":    "test.txt",
		"pattern": "hello",
	}, env)
	if err != nil {
		t.Fatalf("Grep 失败: %v", err)
	}
	matches := out["matches"].([]string)
	if len(matches) != 2 {
		t.Fatalf("期望 2 条匹配，实际 %d: %v", len(matches), matches)
	}

	// 越界访问应被拒绝
	_, err = grepTool.Func(context.Background(), map[string]interface{}{
		"path":    "../../etc/passwd",
		"pattern": "root",
	}, env)
	if err == nil {
		t.Fatal("Grep 应拒绝越界路径")
	}
}

func TestToolRegistry_FetchURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertEqual(t, "GET", r.Method)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Test Page</title></head><body><p>Hello World</p></body></html>`))
	}))
	defer server.Close()

	runner := &mockRunner{}
	search := &mockSearchProvider{}
	registry := NewToolRegistryWithDeps(runner, search)
	env := &ToolEnv{}

	fetchTool, _ := registry.Get("FetchURL")
	out, err := fetchTool.Func(context.Background(), map[string]interface{}{"url": server.URL}, env)
	if err != nil {
		t.Fatalf("FetchURL 失败: %v", err)
	}
	if out["title"] != "Test Page" {
		t.Fatalf("标题提取不对: %v", out["title"])
	}
	if !strings.Contains(out["text"].(string), "Hello World") {
		t.Fatalf("正文提取不对: %v", out["text"])
	}
}

func TestToolRegistry_FetchURL_EmptyURL(t *testing.T) {
	runner := &mockRunner{}
	search := &mockSearchProvider{}
	registry := NewToolRegistryWithDeps(runner, search)
	fetchTool, _ := registry.Get("FetchURL")
	_, err := fetchTool.Func(context.Background(), map[string]interface{}{"url": ""}, &ToolEnv{})
	if err == nil {
		t.Fatal("空 URL 应报错")
	}
}

func TestToolSchemaBuilder_BuildWithOptions(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterBuiltinSearchWeb()
	builder := NewToolSchemaBuilder(registry)

	// 启用联网：应包含 SearchWeb / FetchURL
	withWeb := builder.BuildWithOptions(true, true)
	names := make([]string, 0, len(withWeb))
	for _, tool := range withWeb {
		fn := tool["function"].(map[string]interface{})
		names = append(names, fn["name"].(string))
	}
	if !containsString(names, "SearchWeb") || !containsString(names, "FetchURL") {
		t.Fatalf("启用联网时应包含 SearchWeb/FetchURL，实际 %v", names)
	}

	// 关闭联网：应过滤 SearchWeb / FetchURL
	withoutWeb := builder.BuildWithOptions(true, false)
	for _, tool := range withoutWeb {
		fn := tool["function"].(map[string]interface{})
		name := fn["name"].(string)
		if name == "SearchWeb" || name == "FetchURL" {
			t.Fatalf("关闭联网时不应包含 %s", name)
		}
	}
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func TestToolRegistry_ListAvailable_RespectsVIP(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterBuiltinSearchWeb()
	all := registry.List()
	if len(all) != 8 {
		t.Fatalf("默认工具数量应为 8，实际 %d", len(all))
	}
	free := registry.ListAvailable(false)
	if len(free) != 2 || free[0].Name != "SearchWeb" || free[1].Name != "FetchURL" {
		t.Fatalf("非 VIP 用户应看到 SearchWeb 和 FetchURL，实际 %v", free)
	}
	vip := registry.ListAvailable(true)
	if len(vip) != 8 {
		t.Fatalf("VIP 用户应看到 8 个工具，实际 %d", len(vip))
	}
}

func TestShellSafe_DangerousChars(t *testing.T) {
	cases := []string{
		"ls; rm -rf /",
		"cat /etc/passwd | grep root",
		"echo $(whoami)",
		"echo `date`",
		"ls && pwd",
		"cmd < file",
		"cmd > file",
	}
	for _, c := range cases {
		if err := shellSafe(c); err == nil {
			t.Fatalf("应拦截危险命令: %s", c)
		}
	}
	if err := shellSafe("ls"); err != nil {
		t.Fatalf("普通命令不应拦截: %v", err)
	}
}

func TestDefaultSearchProvider_NoConfig(t *testing.T) {
	// 确保没有任何搜索源 key 配置时使用 dummy 提示
	os.Unsetenv("BAIDU_API_KEY")
	os.Unsetenv("BING_SEARCH_API_KEY")
	os.Unsetenv("SEARXNG_URL")
	sp := NewSearchProvider()
	if sp.Name() != "dummy" {
		t.Fatalf("未配置时应为 dummy provider，实际 %s", sp.Name())
	}
	res, err := sp.Search(context.Background(), "test")
	if err != nil {
		t.Fatalf("dummy search 不应报错: %v", err)
	}
	if _, ok := res["results"]; !ok {
		t.Fatalf("dummy search 应返回 results 字段")
	}
}

var _ PlatformToolRunner = (*mockRunner)(nil)
var _ SearchProvider = (*mockSearchProvider)(nil)
