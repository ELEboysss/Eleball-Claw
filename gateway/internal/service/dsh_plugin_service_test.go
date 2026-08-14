package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestTarball 构造内存 npm tarball（package/ 前缀条目）。
func buildTestTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		data := []byte(content)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// newDSHTestServer 起 httptest：/pkg 返回 registry 元数据（tarball 指向 /dl），/dl 返回 tarball。
func newDSHTestServer(t *testing.T, tarball []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(nil)
	mux.HandleFunc("/dsh-plugin-demo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"dist-tags": map[string]string{"latest": "1.2.0"},
			"versions": map[string]interface{}{
				"1.2.0": map[string]interface{}{
					"description": "demo plugin",
					"dist":        map[string]string{"tarball": srv.URL + "/dl"},
				},
			},
		})
	})
	mux.HandleFunc("/dl", func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	})
	srv.Config.Handler = mux
	return srv
}

const testSkillMD = `---
name: demo-skill
description: 演示秘技
license: MIT
allowed-tools: ReadFile Grep
metadata:
  title: 演示秘技
---

你是演示人格。
`

// TestDSHPluginPreview 预览：tarball 扫描出 SKILL.md 与 mcpServers。
func TestDSHPluginPreview(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"package/package.json":         `{"name":"dsh-plugin-demo","description":"demo plugin","mcpServers":{"fs":{"command":"npx","args":["-y","@mcp/fs"]}}}`,
		"package/skills/demo/SKILL.md": testSkillMD,
		"package/README.md":            "# demo",
	})
	srv := newDSHTestServer(t, tarball)
	defer srv.Close()

	svc := &DSHPluginService{RegistryBase: srv.URL, HTTPClient: srv.Client()}
	preview, err := svc.Preview(context.Background(), "dsh-plugin-demo")
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if preview.Version != "1.2.0" || preview.Description != "demo plugin" {
		t.Fatalf("版本/描述不对: %+v", preview)
	}
	if len(preview.Skills) != 1 {
		t.Fatalf("应扫出 1 个秘技: %+v", preview.Skills)
	}
	sk := preview.Skills[0]
	if sk.Slug != "demo-skill" || sk.Name != "演示秘技" || sk.Description == "" {
		t.Fatalf("秘技元数据不对: %+v", sk)
	}
	if len(preview.MCPServers) != 1 || preview.MCPServers[0].Name != "fs" || preview.MCPServers[0].Command != "npx" {
		t.Fatalf("MCP 配置不对: %+v", preview.MCPServers)
	}
}

// TestDSHPluginPreview_NotFound 不存在包返回友好错误。
func TestDSHPluginPreview_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	svc := &DSHPluginService{RegistryBase: srv.URL, HTTPClient: srv.Client()}
	_, err := svc.Preview(context.Background(), "nope")
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("应返回包不存在错误: %v", err)
	}
}

// TestDSHPluginPreview_NoImportable 无可导入内容时报错。
func TestDSHPluginPreview_NoImportable(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"package/package.json": `{"name":"dsh-plugin-demo"}`,
	})
	srv := newDSHTestServer(t, tarball)
	defer srv.Close()
	svc := &DSHPluginService{RegistryBase: srv.URL, HTTPClient: srv.Client()}
	_, err := svc.Preview(context.Background(), "dsh-plugin-demo")
	if err == nil || !strings.Contains(err.Error(), "未找到可导入内容") {
		t.Fatalf("应报无可导入内容: %v", err)
	}
}

// TestParseNPMSpec 包名/版本解析。
func TestParseNPMSpec(t *testing.T) {
	cases := []struct {
		in, name, version string
	}{
		{"foo", "foo", ""},
		{"foo@1.0.0", "foo", "1.0.0"},
		{"@scope/foo", "@scope/foo", ""},
		{"@scope/foo@2.0.0", "@scope/foo", "2.0.0"},
	}
	for _, c := range cases {
		name, version, err := parseNPMSpec(c.in)
		if err != nil || name != c.name || version != c.version {
			t.Fatalf("parseNPMSpec(%q)=(%q,%q,%v), want (%q,%q)", c.in, name, version, err, c.name, c.version)
		}
	}
	if _, _, err := parseNPMSpec(""); err == nil {
		t.Fatal("空包名应报错")
	}
}

// TestDSHPluginImport_Skills 导入秘技：落盘 + SKU 同步钩子 + frontmatter 全字段保留。
func TestDSHPluginImport_Skills(t *testing.T) {
	marketDir := t.TempDir()
	t.Setenv("CLAW_MARKETPLACE_DIR", marketDir)
	tarball := buildTestTarball(t, map[string]string{
		"package/package.json":              `{"name":"dsh-plugin-demo"}`,
		"package/.dsh/skills/demo/SKILL.md": testSkillMD,
	})
	srv := newDSHTestServer(t, tarball)
	defer srv.Close()

	svc := &DSHPluginService{RegistryBase: srv.URL, HTTPClient: srv.Client(), moduleSvc: &ModuleService{}}
	var synced []string
	result, err := svc.Import(context.Background(), "dsh-plugin-demo@1.2.0", nil, nil, "u1",
		func(dir, skillID, creatorID, creatorName string) (int, int, int) {
			synced = append(synced, skillID)
			return 1, 0, 0
		})
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(result.Skills) != 1 || result.Skills[0] != "demo-skill" {
		t.Fatalf("应导入 demo-skill: %+v", result)
	}
	if len(synced) != 1 || synced[0] != "demo-skill" {
		t.Fatalf("SKU 同步钩子未触发: %v", synced)
	}
	// frontmatter 全字段保留（license/allowed-tools 不被重写丢弃）
	m, err := ParseSkillMD(filepath.Join(marketDir, "demo-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("落盘 SKILL.md 应可解析: %v", err)
	}
	if m.License != "MIT" || m.AllowedTools != "ReadFile Grep" || m.DisplayName() != "演示秘技" {
		t.Fatalf("frontmatter 字段丢失: %+v", m)
	}
	if !strings.Contains(m.Body, "演示人格") {
		t.Fatalf("body 丢失: %q", m.Body)
	}
}
