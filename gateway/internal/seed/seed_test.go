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

// TestSyncClawOfficialSKUs 泛化扫描本地官方 SKU（sku_scope=claw，即 search-web
// 百度千帆/必应两条免费 SKU），manifest 含 credentials 声明，幂等重跑不产生重复。
func TestSyncClawOfficialSKUs(t *testing.T) {
	repo := setupSeedTestRepo(t)
	logger := zap.NewNop()

	require.NoError(t, SyncOfficialSKUs(repo, "claw", logger))

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

	// mcp-hello 示例 SKU 已同步
	hello, err := repo.GetByID("mcp-hello-hello")
	require.NoError(t, err)
	assert.Equal(t, int64(0), hello.PriceDanwan)
	assert.Equal(t, model.AgentStatusApproved, hello.Status)
	mh, err := hello.Manifest()
	require.NoError(t, err)
	require.NotNil(t, mh)
	assert.Equal(t, model.ToolDriverType("mcp_hello"), mh.Driver)

	// 幂等：重跑不产生重复、不报错
	require.NoError(t, SyncOfficialSKUs(repo, "claw", logger))
	total, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
}

// TestSkillMakerSKU 预置官方 Prompt 型秘技「秘技制造机」：SystemPrompt 非空、ManifestJSON 空，
// 免费、官方、开发类；幂等重跑不产生重复。
func TestSkillMakerSKU(t *testing.T) {
	repo := setupSeedTestRepo(t)
	logger := zap.NewNop()

	require.NoError(t, SkillMakerSKU(repo, logger))

	item, err := repo.GetByID("skill-maker")
	require.NoError(t, err)
	assert.Equal(t, "秘技制造机", item.Name)
	assert.Equal(t, int64(0), item.PriceDanwan)
	assert.Nil(t, item.PriceElegant)
	assert.Equal(t, model.AgentStatusApproved, item.Status)
	assert.Equal(t, "开发", item.Category)
	assert.Equal(t, "官方", item.CreatorName)
	assert.Equal(t, model.AgentLevelXuan, item.Level)

	// Prompt 型关键断言：SystemPrompt 非空、ManifestJSON 为空、Manifest() 返回 nil
	assert.NotEmpty(t, item.SystemPrompt)
	assert.Empty(t, item.ManifestJSON)
	m, err := item.Manifest()
	require.NoError(t, err)
	assert.Nil(t, m)
	// SystemPrompt 含方法论关键短语
	assert.Contains(t, item.SystemPrompt, "标准接口")
	assert.Contains(t, item.SystemPrompt, "ToolManifest")

	// 幂等：重跑不报错、不产生重复
	require.NoError(t, SkillMakerSKU(repo, logger))
	total, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// 同步路径：篡改后重跑能回写文件内容
	item.SystemPrompt = "被改坏的旧值"
	require.NoError(t, repo.Update(item))
	require.NoError(t, SkillMakerSKU(repo, logger))
	restored, err := repo.GetByID("skill-maker")
	require.NoError(t, err)
	assert.NotEqual(t, "被改坏的旧值", restored.SystemPrompt)
	assert.Contains(t, restored.SystemPrompt, "标准接口")
}
