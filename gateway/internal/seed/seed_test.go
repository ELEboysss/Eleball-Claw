package seed

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	// Exa 变体（keyless：经 mcporter，无 credentials）
	exa, err := repo.GetByID("search-web-exa")
	require.NoError(t, err)
	assert.Equal(t, int64(0), exa.PriceDanwan)
	assert.Equal(t, model.AgentStatusApproved, exa.Status)
	me, err := exa.Manifest()
	require.NoError(t, err)
	require.NotNil(t, me)
	assert.Equal(t, model.ToolDriverType("search_web"), me.Driver)
	assert.Equal(t, "search-web", me.Metadata["module"])
	assert.Equal(t, "exa", me.Metadata["provider"])
	assert.Empty(t, me.Credentials) // keyless，无需用户配置凭证
	require.Len(t, me.Actions, 1)
	assert.Equal(t, "search", me.Actions[0].Name)

	// web_read 变体（keyless：经 mcporter 调 Exa web_fetch_exa，action=web_read）
	wr, err := repo.GetByID("search-web-web_read")
	require.NoError(t, err)
	assert.Equal(t, int64(0), wr.PriceDanwan)
	assert.Equal(t, model.AgentStatusApproved, wr.Status)
	mw, err := wr.Manifest()
	require.NoError(t, err)
	require.NotNil(t, mw)
	assert.Equal(t, model.ToolDriverType("search_web"), mw.Driver)
	assert.Empty(t, mw.Credentials) // keyless
	require.Len(t, mw.Actions, 1)
	assert.Equal(t, "web_read", mw.Actions[0].Name)
	wrReq, ok := mw.Parameters["required"].([]interface{})
	require.True(t, ok)
	assert.Contains(t, wrReq, "url")

	// mcp-hello 示例 SKU 已同步
	hello, err := repo.GetByID("mcp-hello-hello")
	require.NoError(t, err)
	assert.Equal(t, int64(0), hello.PriceDanwan)
	assert.Equal(t, model.AgentStatusApproved, hello.Status)
	mh, err := hello.Manifest()
	require.NoError(t, err)
	require.NotNil(t, mh)
	assert.Equal(t, model.ToolDriverType("mcp_hello"), mh.Driver)

	// prompt-only skill（SKILL.md，driver=none）已同步：skill-maker + copywriting
	maker, err := repo.GetByID("skillmd-skill-maker")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, maker.Status)
	assert.Equal(t, "制造", maker.Category)
	assert.NotEmpty(t, maker.SystemPrompt) // SKILL.md body 即 SystemPrompt
	mm, err := maker.Manifest()
	require.NoError(t, err)
	require.NotNil(t, mm)
	assert.Equal(t, model.ToolDriverNone, mm.Driver)
	assert.Equal(t, "skillmd-skill-maker", mm.ID)

	copyw, err := repo.GetByID("skillmd-copywriting")
	require.NoError(t, err)
	assert.Equal(t, model.AgentStatusApproved, copyw.Status)
	assert.Equal(t, "文案", copyw.Category)
	assert.NotEmpty(t, copyw.SystemPrompt)
	mcw, err := copyw.Manifest()
	require.NoError(t, err)
	require.NotNil(t, mcw)
	assert.Equal(t, model.ToolDriverNone, mcw.Driver)

	// 幂等：重跑不产生重复、不报错
	require.NoError(t, SyncOfficialSKUs(repo, "claw", logger))
	total, err := repo.Count()
	require.NoError(t, err)
	// search-web（百度/必应/Exa/网页阅读）+ mcp-hello + mcp-stdio-echo（echo/ping）+ skill-maker + copywriting = 9
	assert.Equal(t, int64(9), total)
}

// TestSyncPromptSkillSKU 验证 SKILL.md prompt-only skill 注册（S2）：
// 「只有 SKILL.md 无 module.json」目录 -> 1 个 driver=none SKU，body 即 SystemPrompt。
// 覆盖：创建、幂等跳过、body 变更同步、frontmatter 不合法跳过、metadata.category 覆盖默认分类。
// 用 temp dir 隔离，不依赖真实 marketplace 内容。
func TestSyncPromptSkillSKU(t *testing.T) {
	repo := setupSeedTestRepo(t)
	logger := zap.NewNop()
	adminID := "00000000-0000-0000-0000-000000000000"
	now := time.Now()

	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "copywriting")
	require.NoError(t, os.Mkdir(skillDir, 0o755))
	skillMD := "---\nname: copywriting\ndescription: 文案撰写专家\nmetadata:\n  category: 写作\n  version: 1.0.0\n---\n\n你是文案撰写专家，输出有感染力的营销文案。\n\n## 规则\n\n- 简洁有力\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644))

	// 首次：创建 1 个 prompt-only SKU
	c, sy, sk := syncPromptSkillSKU(repo, tmp, "copywriting", adminID, now, logger)
	assert.Equal(t, 1, c)
	assert.Equal(t, 0, sy)
	assert.Equal(t, 0, sk)

	item, err := repo.GetByID("skillmd-copywriting")
	require.NoError(t, err)
	assert.Equal(t, "copywriting", item.Name)
	assert.Equal(t, "文案撰写专家", item.Description)
	assert.Equal(t, "写作", item.Category) // metadata.category 覆盖默认「提示」
	assert.Equal(t, "官方", item.CreatorName)
	assert.Equal(t, model.AgentStatusApproved, item.Status)
	assert.Equal(t, "你是文案撰写专家，输出有感染力的营销文案。\n\n## 规则\n\n- 简洁有力", item.SystemPrompt)
	m, err := item.Manifest()
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, model.ToolDriverNone, m.Driver)
	assert.Equal(t, "skillmd-copywriting", m.ID)

	// 幂等：内容不变 -> skipped=1，不重复创建
	c, sy, sk = syncPromptSkillSKU(repo, tmp, "copywriting", adminID, now, logger)
	assert.Equal(t, 0, c)
	assert.Equal(t, 0, sy)
	assert.Equal(t, 1, sk)
	total, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// body 变更 -> synced=1，SystemPrompt 更新（SKILL.md 是源格式，body 变化即同步）
	skillMD2 := "---\nname: copywriting\ndescription: 文案撰写专家\nmetadata:\n  category: 写作\n---\n\n你是高级文案专家，输出更有感染力的文案。\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD2), 0o644))
	c, sy, sk = syncPromptSkillSKU(repo, tmp, "copywriting", adminID, now, logger)
	assert.Equal(t, 0, c)
	assert.Equal(t, 1, sy)
	assert.Equal(t, 0, sk)
	item, err = repo.GetByID("skillmd-copywriting")
	require.NoError(t, err)
	assert.Equal(t, "你是高级文案专家，输出更有感染力的文案。", item.SystemPrompt)

	// frontmatter 不合法（无 ---）的目录 -> 全 0，不创建
	badDir := filepath.Join(tmp, "bad-skill")
	require.NoError(t, os.Mkdir(badDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(badDir, "SKILL.md"), []byte("# 纯文档无 frontmatter\n"), 0o644))
	c, sy, sk = syncPromptSkillSKU(repo, tmp, "bad-skill", adminID, now, logger)
	assert.Equal(t, 0, c)
	assert.Equal(t, 0, sy)
	assert.Equal(t, 0, sk)
}
