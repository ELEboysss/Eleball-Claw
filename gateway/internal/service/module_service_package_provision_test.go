package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// writeProvisionPkg 写一个含免费 tool + 付费 tool + 免费 mcp 三段的 package 目录。
// 端点指向不可达端口（127.0.0.1:1），Start/ForceProbe 快速失败，不 spawn 子进程。
func writeProvisionPkg(t *testing.T, root string) {
	t.Helper()
	modDir := filepath.Join(root, "prov-pkg")
	require.NoError(t, os.MkdirAll(modDir, 0o755))
	pkgJSON := `{
	  "name":"prov-pkg","version":"1.0.0","description":"prov pkg","level":2,
	  "tools":[
	    {"name":"fetch","description":"抓取","transport":"http","endpoint":"http://127.0.0.1:1/fetch"},
	    {"name":"paytool","description":"付费","transport":"http","endpoint":"http://127.0.0.1:1/pay",
	     "pricing":{"type":"per_call","currency":"danwan","amount_per_call":5}}
	  ],
	  "mcpServers":{"main":{"transport":"http","url":"http://127.0.0.1:1/mcp"}}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "package.json"), []byte(pkgJSON), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, ".origin"), []byte("cloud"), 0o644))
}

// TestEnsurePackageProvision T5.2：下载云端包后为免费派生 SKU 补购买记录（D9 官方免费直下），
// 付费 SKU 保持门禁不自动领取；幂等；userID 为空 no-op。
func TestEnsurePackageProvision(t *testing.T) {
	modSvc, _, agentRepo, root := newActivationTestSvc(t)
	writeProvisionPkg(t, root)
	require.NoError(t, modSvc.RescanPackage("claw", zap.NewNop()))

	const userID = "u1"

	// 未调用前：均未购买（含免费 SKU）
	purchased, err := agentRepo.HasPurchased("prov-pkg-fetch", userID)
	require.NoError(t, err)
	assert.False(t, purchased, "未领取前免费 SKU 未购买")

	// 领取：免费 tool + mcp 建购买，付费 tool 跳过
	require.NoError(t, modSvc.EnsurePackageProvision("prov-pkg", userID))

	for _, id := range []string{"prov-pkg-fetch", "prov-pkg-mcp-main"} {
		ok, err := agentRepo.HasPurchased(id, userID)
		require.NoError(t, err)
		assert.True(t, ok, "免费 SKU %s 下载后自动领取", id)
	}
	paid, err := agentRepo.HasPurchased("prov-pkg-paytool", userID)
	require.NoError(t, err)
	assert.False(t, paid, "付费 SKU 不自动领取，保持购买门禁")

	// 幂等：重复领取不报错
	require.NoError(t, modSvc.EnsurePackageProvision("prov-pkg", userID))
	ok, err := agentRepo.HasPurchased("prov-pkg-fetch", userID)
	require.NoError(t, err)
	assert.True(t, ok, "重复领取后仍已购买")

	// 多用户：各自独立领取
	require.NoError(t, modSvc.EnsurePackageProvision("prov-pkg", "u2"))
	ok, err = agentRepo.HasPurchased("prov-pkg-fetch", "u2")
	require.NoError(t, err)
	assert.True(t, ok, "另一用户领取互不影响")

	// userID 空：no-op 不报错
	require.NoError(t, modSvc.EnsurePackageProvision("prov-pkg", ""))
	ok, err = agentRepo.HasPurchased("prov-pkg-fetch", "u1")
	require.NoError(t, err)
	assert.True(t, ok, "空 userID 不破坏既有购买记录")
}
