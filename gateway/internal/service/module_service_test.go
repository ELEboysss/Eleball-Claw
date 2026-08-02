package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// TestModuleService_RescanMarketplace_MCP 验证 marketplace 扫描能识别 transport_type=mcp 的示例模块，
// 并创建正确的 ModuleRecord 与 DriverRecord（含 MCPServerConfig）。
func TestModuleService_RescanMarketplace_MCP(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ModuleRecord{}, &model.DriverRecord{}))

	moduleRepo := repository.NewModuleRepo(db)
	driverRepo := repository.NewDriverRepo(db)
	registry := NewModuleRegistry(&config.AgentReachConfig{})
	registry.SetRepo(moduleRepo)
	registry.SetDriverRepo(driverRepo)
	svc := NewModuleService(registry, moduleRepo, driverRepo)

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

	mod, err := svc.GetModule("mcp-hello")
	require.NoError(t, err)
	require.NotNil(t, mod)
	assert.Equal(t, model.ModuleTransportTypeMCP, mod.TransportType)
	assert.Equal(t, "http://mcp-hello:8080/mcp", mod.URL)
	assert.True(t, mod.Official)

	drv, err := svc.ResolveDriver("mcp_hello")
	require.NoError(t, err)
	require.NotNil(t, drv)
	assert.Equal(t, string(model.ModuleTransportTypeMCP), drv.TransportType)
	require.NotNil(t, drv.MCPServerConfig)
	assert.Equal(t, "http://mcp-hello:8080/mcp", drv.MCPServerConfig.URL)
}
