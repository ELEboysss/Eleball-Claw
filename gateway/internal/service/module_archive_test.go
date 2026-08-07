package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// readTarGz 解压 tar.gz 字节流为 map[entryName]内容（仅常规文件），供断言打包产物。
func readTarGz(t *testing.T, data []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		require.NoError(t, err)
		files[hdr.Name] = string(b)
	}
	return files
}

// extractTarGzTo 解压 tar.gz 到 dest（拒绝 zip-slip），模拟 T11 云端解压到 marketplace/<id>/。
func extractTarGzTo(t *testing.T, data []byte, dest string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dest, 0o755))
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	cleanDest := filepath.Clean(dest)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		target := filepath.Join(dest, hdr.Name)
		if !strings.HasPrefix(filepath.Clean(target), cleanDest+string(os.PathSeparator)) {
			t.Fatalf("tar 条目逃逸目标目录: %s", hdr.Name)
		}
		if hdr.Typeflag == tar.TypeDir {
			require.NoError(t, os.MkdirAll(target, 0o755))
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		b, err := io.ReadAll(tr)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(target, b, 0o644))
	}
}

// newArchiveTestSvc 构造一个带内存 DB 的 ModuleService，供打包/回扫测试复用。
func newArchiveTestSvc(t *testing.T) (*ModuleService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))
	skillRuntimeRepo := repository.NewSkillRuntimeRepo(db)
	registry := NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	registry.SetRepo(skillRuntimeRepo)
	manager := NewSkillRuntimeManager(registry, zap.NewNop())
	svc := NewModuleService(registry, manager, skillRuntimeRepo, nil)
	return svc, db
}

// TestPackageModule_ScriptModule 验证脚本模块打包：递归收录 module.json/main.py/skus/，
// 排除 __pycache__/*.pyc 构建产物；且产物可被扫描器原样读回（含 source_origin=user 来源）。
func TestPackageModule_ScriptModule(t *testing.T) {
	svc, _ := newArchiveTestSvc(t)

	root := t.TempDir()
	t.Setenv("CLAW_MARKETPLACE_DIR", root) // 隔离 marketplace 根，避免读到真实 home

	const moduleID = "my-script-mod"
	modDir := filepath.Join(root, moduleID)
	require.NoError(t, os.MkdirAll(filepath.Join(modDir, "skus"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(modDir, "__pycache__"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "module.json"), []byte(`{
  "id": "my-script-mod",
  "name": "我的脚本",
  "description": "user script",
  "source": "marketplace",
  "source_origin": "user",
  "source_actor": "alice",
  "transport": "mcp_stdio",
  "deployment": "process",
  "command": "python",
  "args": ["main.py"],
  "auto_sku": true,
  "sku_scope": "claw",
  "capabilities": ["echo"],
  "driver": {"driver_id": "my-script-mod", "name": "我的脚本"}
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "main.py"), []byte("print('hi')\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "skus", "echo.json"), []byte(`{"name":"echo"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "__pycache__", "main.cpython-314.pyc"), []byte("binary junk"), 0o644))

	res, err := svc.PackageModule(moduleID)
	require.NoError(t, err)
	assert.Equal(t, "my-script-mod.tar.gz", res.Filename)
	assert.NotEmpty(t, res.Data)

	files := readTarGz(t, res.Data)
	assert.Contains(t, files, "module.json")
	assert.Contains(t, files, "main.py")
	assert.Contains(t, files, "skus/echo.json")
	// 构建产物必须被排除
	for name := range files {
		assert.False(t, strings.Contains(name, "__pycache__"), "产物不应含 __pycache__: %s", name)
		assert.False(t, strings.HasSuffix(name, ".pyc"), "产物不应含 .pyc: %s", name)
	}
	// 无 <id>/ 前缀（扁平布局）
	for name := range files {
		assert.False(t, strings.HasPrefix(name, moduleID+"/"), "产物应为扁平布局: %s", name)
	}

	// 回扫验证：解压到新 marketplace 根的 <id>/ 下，扫描器应读回带 user 来源的运行时。
	root2 := t.TempDir()
	extractTarGzTo(t, res.Data, filepath.Join(root2, moduleID))
	require.NoError(t, svc.ensureMarketplaceModules(root2, zap.NewNop()))
	rt, err := svc.repo.GetByID(moduleID)
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Equal(t, "我的脚本", rt.Name)
	assert.Equal(t, model.SkillRuntimeTransportMCPStdio, rt.Transport)
	assert.Equal(t, model.SkillRuntimeDeploymentProcess, rt.Deployment)
	assert.Equal(t, model.SkillRuntimeOriginUser, rt.SourceOrigin)
	assert.Equal(t, "alice", rt.SourceActor)
	assert.Equal(t, moduleID, rt.DriverID)
	assert.True(t, rt.AutoSKU)
	assert.Equal(t, []string{"echo"}, rt.CapabilitiesList())
}

// TestPackageModule_MCPRuntime 验证 DB-only MCP 安装模块打包：无磁盘文件时从 SkillRuntime
// 物化 module.json，产物可被扫描器读回（含 source_origin=mcp 来源、transport/deployment 配置）。
func TestPackageModule_MCPRuntime(t *testing.T) {
	svc, _ := newArchiveTestSvc(t)

	root := t.TempDir()
	t.Setenv("CLAW_MARKETPLACE_DIR", root) // 根下无该模块目录 -> 走 DB 物化

	const moduleID = "mcp-remote-testpack"
	rt := &model.SkillRuntime{
		ID:           moduleID,
		Name:         "TestPack MCP",
		Description:  "db-only mcp",
		Source:       model.SkillRuntimeSourceMCPRemote,
		SourceOrigin: model.SkillRuntimeOriginMCP,
		SourceActor:  "TestPack MCP",
		Transport:    model.SkillRuntimeTransportMCPStdio,
		Deployment:   model.SkillRuntimeDeploymentProcess,
		Command:      "python",
		AutoSKU:      true,
		DriverID:     moduleID,
	}
	rt.SetArgs([]string{"main.py"})
	rt.SetCapabilities([]string{"tool_a", "tool_b"})
	require.NoError(t, svc.repo.CreateOrUpdate(rt))

	res, err := svc.PackageModule(moduleID)
	require.NoError(t, err)
	assert.Equal(t, "mcp-remote-testpack.tar.gz", res.Filename)

	files := readTarGz(t, res.Data)
	assert.Len(t, files, 1, "DB-only MCP 产物应仅含 module.json")
	assert.Contains(t, files, "module.json")

	// 物化的 module.json 字段
	var m marketplaceModuleManifest
	require.NoError(t, json.Unmarshal([]byte(files["module.json"]), &m))
	assert.Equal(t, moduleID, m.GetID())
	assert.Equal(t, "TestPack MCP", m.Name)
	assert.Equal(t, "mcp_stdio", m.GetTransport())
	assert.Equal(t, "process", m.GetDeployment())
	assert.Equal(t, "mcp", m.SourceOrigin)
	assert.Equal(t, "TestPack MCP", m.SourceActor)
	assert.Equal(t, moduleID, m.Driver.ID)
	assert.True(t, m.AutoSKU)
	assert.Equal(t, []string{"tool_a", "tool_b"}, m.Capabilities)
	assert.Equal(t, "claw", m.SKUScope) // process -> claw

	// 回扫验证
	root2 := t.TempDir()
	extractTarGzTo(t, res.Data, filepath.Join(root2, moduleID))
	require.NoError(t, svc.ensureMarketplaceModules(root2, zap.NewNop()))
	rt2, err := svc.repo.GetByID(moduleID)
	require.NoError(t, err)
	require.NotNil(t, rt2)
	assert.Equal(t, "TestPack MCP", rt2.Name)
	assert.Equal(t, model.SkillRuntimeTransportMCPStdio, rt2.Transport)
	assert.Equal(t, model.SkillRuntimeDeploymentProcess, rt2.Deployment)
	assert.Equal(t, model.SkillRuntimeOriginMCP, rt2.SourceOrigin)
	assert.Equal(t, "TestPack MCP", rt2.SourceActor)
	assert.Equal(t, moduleID, rt2.DriverID)
	assert.True(t, rt2.AutoSKU)
	assert.Equal(t, []string{"tool_a", "tool_b"}, rt2.CapabilitiesList())
	assert.Equal(t, "python", rt2.Command)
	assert.Equal(t, []string{"main.py"}, rt2.ArgsList())
}

// TestPackageModule_NotFound 无磁盘目录且未注册的模块应返回错误。
func TestPackageModule_NotFound(t *testing.T) {
	svc, _ := newArchiveTestSvc(t)
	t.Setenv("CLAW_MARKETPLACE_DIR", t.TempDir())
	_, err := svc.PackageModule("does-not-exist")
	require.Error(t, err)
}
