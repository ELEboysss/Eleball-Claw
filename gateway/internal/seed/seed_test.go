package seed

import (
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// setupSeedTestRepo 构造内存 SQLite + AgentRepo（单连接避免「no such table」）
func setupSeedTestRepo(t *testing.T) *repository.AgentRepo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.AgentItem{}))
	return repository.NewAgentRepo(db)
}

// TestSearchWebSKUs 预置百度千帆/必应两条免费官方 SKU，manifest 含 credentials 声明，幂等重跑不产生重复
func TestSearchWebSKUs(t *testing.T) {
	repo := setupSeedTestRepo(t)
	logger := zap.NewNop()

	require.NoError(t, SearchWebSKUs(repo, logger))

	// 百度千帆变体
	baidu, err := repo.GetByID("search-web-baidu")
	require.NoError(t, err)
	assert.Equal(t, int64(0), baidu.PriceDanwan)
	assert.Nil(t, baidu.PriceElegant)
	assert.Equal(t, model.AgentStatusApproved, baidu.Status)
	assert.Equal(t, "搜索", baidu.Category)
	assert.Equal(t, "官方", baidu.CreatorName)
	assert.Equal(t, model.AgentLevelHuang, baidu.Level)

	m, err := baidu.Manifest()
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, model.ToolDriverType("search_web"), m.Driver)
	assert.Equal(t, "remote", m.RuntimeType)
	assert.Equal(t, "search-web", m.Metadata["module"])
	require.Contains(t, m.Credentials, "baidu_api_key")
	assert.Equal(t, model.CredentialTypeAPIKey, m.Credentials["baidu_api_key"].Type)
	assert.True(t, m.Credentials["baidu_api_key"].Required)
	require.Len(t, m.Actions, 1)
	assert.Equal(t, "search", m.Actions[0].Name)
	// query 为必填参数
	required, ok := m.Parameters["required"].([]interface{})
	require.True(t, ok)
	assert.Contains(t, required, "query")

	// 必应变体
	bing, err := repo.GetByID("search-web-bing")
	require.NoError(t, err)
	assert.Equal(t, int64(0), bing.PriceDanwan)
	assert.Equal(t, model.AgentStatusApproved, bing.Status)
	mb, err := bing.Manifest()
	require.NoError(t, err)
	require.NotNil(t, mb)
	assert.Equal(t, model.ToolDriverType("search_web"), mb.Driver)
	require.Contains(t, mb.Credentials, "bing_search_api_key")
	assert.True(t, mb.Credentials["bing_search_api_key"].Required)

	// 幂等：重跑不产生重复、不报错
	require.NoError(t, SearchWebSKUs(repo, logger))
	total, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
}
