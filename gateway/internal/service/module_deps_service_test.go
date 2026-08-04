package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRequirements(t *testing.T) {
	content := `# 主依赖
requests>=2.20
rich[colors]==13.7.0
# 以下为开发依赖
pytest~=7.0; python_version<'3.12'
-e .
--index-url https://pypi.org/simple
beautifulsoup4
`
	deps := parseRequirements(content)
	// 期望：requests, rich, pytest, beautifulsoup4（跳过注释、选项行 -e/--index-url）。
	want := []struct{ name, spec string }{
		{"requests", ">=2.20"},
		{"rich", "[colors]==13.7.0"},
		{"pytest", "~=7.0"},
		{"beautifulsoup4", ""},
	}
	if len(deps) != len(want) {
		t.Fatalf("应解析 %d 个依赖，实得 %d: %v", len(want), len(deps), deps)
	}
	for i, w := range want {
		if deps[i].Name != w.name || deps[i].Spec != w.spec {
			t.Errorf("deps[%d] = {%s,%s}, want {%s,%s}", i, deps[i].Name, deps[i].Spec, w.name, w.spec)
		}
	}
}

func TestParseRequirementsEmpty(t *testing.T) {
	if got := parseRequirements(""); len(got) != 0 {
		t.Errorf("空内容应返回空，实得 %v", got)
	}
	if got := parseRequirements("# only comments\n\n"); len(got) != 0 {
		t.Errorf("纯注释应返回空，实得 %v", got)
	}
}

func TestParsePackageDeps(t *testing.T) {
	data := []byte(`{
		"dependencies": { "express": "^4.17.0", "axios": "^1.6.0" },
		"devDependencies": { "jest": "^29.0.0" }
	}`)
	deps := parsePackageDeps(data)
	if len(deps) != 3 {
		t.Fatalf("应解析 3 个依赖，实得 %d: %v", len(deps), deps)
	}
	names := map[string]string{}
	for _, d := range deps {
		names[d.Name] = d.Spec
	}
	if names["express"] != "^4.17.0" {
		t.Errorf("express spec 错误: %s", names["express"])
	}
	if names["jest"] != "^29.0.0" {
		t.Errorf("jest spec 错误: %s", names["jest"])
	}
}

func TestParsePackageDepsInvalid(t *testing.T) {
	if got := parsePackageDeps([]byte("not json")); got != nil {
		t.Errorf("无效 JSON 应返回 nil，实得 %v", got)
	}
}

func TestManagedVenvPython(t *testing.T) {
	dir := t.TempDir()
	if managedVenvPython(dir) != "" {
		t.Error("无 venv 时应返回空")
	}
	// 建 .venv/bin/python（unix 布局）
	binDir := filepath.Join(dir, ".venv", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	py := filepath.Join(binDir, "python")
	if err := os.WriteFile(py, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := managedVenvPython(dir); got != py {
		t.Errorf("应找到 %s，实得 %s", py, got)
	}
}

func TestNodeModulesExists(t *testing.T) {
	dir := t.TempDir()
	if nodeModulesExists(dir) {
		t.Error("无 node_modules 应返回 false")
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !nodeModulesExists(dir) {
		t.Error("有 node_modules 应返回 true")
	}
}

func TestPrependPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	env := []string{"FOO=bar", "PATH=/usr/bin:/bin"}
	got := prependPath(env, "/opt/node/bin")
	want := "PATH=/opt/node/bin" + sep + "/usr/bin:/bin"
	found := false
	for _, e := range got {
		if e == want {
			found = true
		}
	}
	if !found {
		t.Errorf("PATH 未正确前插（want %q）: %v", want, got)
	}
	// 无 PATH 时追加。
	env2 := []string{"FOO=bar"}
	got2 := prependPath(env2, "/opt/node/bin")
	hasPath := false
	for _, e := range got2 {
		if e == "PATH=/opt/node/bin" {
			hasPath = true
		}
	}
	if !hasPath {
		t.Errorf("无 PATH 时应追加: %v", got2)
	}
}

func TestNodeBinDirFor(t *testing.T) {
	// 系统 node 路径 -> 其所在目录（filepath.Dir 平台相关，用同函数计算期望值）。
	nodePath := filepath.Join("usr", "bin", "node")
	want := filepath.Dir(nodePath)
	if got := nodeBinDirFor(nodePath); got != want {
		t.Errorf("系统 node bin dir 应为 %s，实得 %s", want, got)
	}
	if nodeBinDirFor("") != "" {
		t.Error("空路径应返回空")
	}
}

// TestDepsStatus_NonStdioModule 非 stdio+process 模块应 has_deps=false。
func TestDepsStatus_NonStdioModule(t *testing.T) {
	s := &ModuleService{}
	// 无 registry -> resolveStdioProcessRuntime 返回 nil。
	st, err := s.DepsStatus("anything")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if st.HasDeps {
		t.Error("无 registry 时应 has_deps=false")
	}
}
