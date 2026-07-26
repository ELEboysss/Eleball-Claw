package service

import (
	"context"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// teamMemoryTestEnv 组共享记忆测试的公共装配
type teamMemoryTestEnv struct {
	db     *gorm.DB
	team   *TeamService
	memory *TeamMemoryService
}

func setupTeamMemoryTest(t *testing.T) *teamMemoryTestEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// :memory: 数据库按连接隔离，限制单连接避免「no such table」
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Team{}, &model.TeamMemory{}, &model.ChatConversation{}))

	teamSvc := NewTeamService(db, repository.NewTeamRepo(db))
	memRepo := repository.NewTeamMemoryRepo(db)
	teamSvc.SetTeamMemoryRepo(memRepo)
	return &teamMemoryTestEnv{
		db:     db,
		team:   teamSvc,
		memory: NewTeamMemoryService(memRepo, teamSvc),
	}
}

// insertMemory 直接落一条记忆（绕过校验，便于构造检索/去重场景）
func (e *teamMemoryTestEnv) insertMemory(t *testing.T, id, teamID, userID, content, tags string, createdAt int64) model.TeamMemory {
	t.Helper()
	m := model.TeamMemory{
		ID:        id,
		TeamID:    teamID,
		UserID:    userID,
		Content:   content,
		Tags:      tags,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	require.NoError(t, e.db.Create(&m).Error)
	return m
}

const teamMemoryTestUser = "user-1"

// TestTeamMemory_ManualCRUD 手动增删查与归属校验
func TestTeamMemory_ManualCRUD(t *testing.T) {
	env := setupTeamMemoryTest(t)
	ctx := context.Background()
	_ = ctx

	team, err := env.team.Create(teamMemoryTestUser, "项目组", "")
	require.NoError(t, err)

	// content 必填
	_, err = env.memory.AddMemory(teamMemoryTestUser, team.ID, "", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不能为空")

	// content 超 500 字拒绝
	long := make([]rune, 501)
	for i := range long {
		long[i] = '字'
	}
	_, err = env.memory.AddMemory(teamMemoryTestUser, team.ID, string(long), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")

	// 正常新增
	m1, err := env.memory.AddMemory(teamMemoryTestUser, team.ID, "用户偏好简洁的回答", "偏好", "")
	require.NoError(t, err)
	assert.NotEmpty(t, m1.ID)
	assert.Equal(t, team.ID, m1.TeamID)

	time.Sleep(10 * time.Millisecond) // 保证 created_at 可区分（秒级时间戳下同秒也可按序插入）
	m2, err := env.memory.AddMemory(teamMemoryTestUser, team.ID, "项目代号是 Eleball", "项目", "")
	require.NoError(t, err)
	// created_at 为秒级时间戳，手动拉开一秒保证倒序断言确定
	require.NoError(t, env.db.Model(&model.TeamMemory{}).Where("id = ?", m2.ID).Update("created_at", m1.CreatedAt+1).Error)

	// 列表：倒序 + 分页 + total
	items, total, err := env.memory.ListMemories(teamMemoryTestUser, team.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)
	assert.Equal(t, m2.ID, items[0].ID) // 后插入的在前（created_at 倒序）

	items, total, err = env.memory.ListMemories(teamMemoryTestUser, team.ID, 2, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 1)
	assert.Equal(t, m1.ID, items[0].ID)

	// 归属校验：他人不可增删查
	_, err = env.memory.AddMemory("user-2", team.ID, "越权写入", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "无权访问该组")
	_, _, err = env.memory.ListMemories("user-2", team.ID, 1, 20)
	require.Error(t, err)
	require.Error(t, env.memory.DeleteMemory("user-2", team.ID, m1.ID))

	// 不存在的组
	_, _, err = env.memory.ListMemories(teamMemoryTestUser, "team-x", 1, 20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "组不存在")

	// 删除：条目不属于该组时拒绝
	otherTeam, err := env.team.Create(teamMemoryTestUser, "另一个组", "")
	require.NoError(t, err)
	require.Error(t, env.memory.DeleteMemory(teamMemoryTestUser, otherTeam.ID, m1.ID))

	require.NoError(t, env.memory.DeleteMemory(teamMemoryTestUser, team.ID, m1.ID))
	_, total, err = env.memory.ListMemories(teamMemoryTestUser, team.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

// TestTeamMemory_DeleteTeamCascadesMemories 删除组时级联清理组记忆
func TestTeamMemory_DeleteTeamCascadesMemories(t *testing.T) {
	env := setupTeamMemoryTest(t)

	team, err := env.team.Create(teamMemoryTestUser, "临时组", "")
	require.NoError(t, err)
	_, err = env.memory.AddMemory(teamMemoryTestUser, team.ID, "待清理的记忆", "", "")
	require.NoError(t, err)

	require.NoError(t, env.team.Delete(teamMemoryTestUser, team.ID))
	var count int64
	require.NoError(t, env.db.Model(&model.TeamMemory{}).Where("team_id = ?", team.ID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

// TestTeamMemory_SearchForInjection 检索排序、topN 与组间隔离
func TestTeamMemory_SearchForInjection(t *testing.T) {
	env := setupTeamMemoryTest(t)
	ctx := context.Background()
	now := time.Now().Unix()

	teamA, err := env.team.Create(teamMemoryTestUser, "组 A", "")
	require.NoError(t, err)
	teamB, err := env.team.Create(teamMemoryTestUser, "组 B", "")
	require.NoError(t, err)

	// 组 A：命中关键词的新旧两条 + 不命中一条
	env.insertMemory(t, "m-old", teamA.ID, teamMemoryTestUser, "用户喜欢深度学习的论文", "", now-60*86400)
	env.insertMemory(t, "m-new", teamA.ID, teamMemoryTestUser, "用户在研究深度学习模型压缩", "深度学习", now)
	env.insertMemory(t, "m-none", teamA.ID, teamMemoryTestUser, "项目每周五例会", "", now)
	// 组 B：同关键词，不应出现在组 A 的检索结果中（组间隔离）
	env.insertMemory(t, "m-b", teamB.ID, teamMemoryTestUser, "组 B 的深度学习笔记", "", now)

	hits := env.memory.SearchForInjection(ctx, teamMemoryTestUser, teamA.ID, "深度学习 模型", 8)
	require.Len(t, hits, 2)
	// 新的、且 tags 也命中的条目应排最前（时间衰减叠加关键词加权）
	assert.Equal(t, "m-new", hits[0].ID)
	assert.Equal(t, "m-old", hits[1].ID)

	// topN 截断
	hits = env.memory.SearchForInjection(ctx, teamMemoryTestUser, teamA.ID, "深度学习", 1)
	require.Len(t, hits, 1)
	assert.Equal(t, "m-new", hits[0].ID)

	// 组 B 检索只能命中自己的记忆
	hits = env.memory.SearchForInjection(ctx, teamMemoryTestUser, teamB.ID, "深度学习", 8)
	require.Len(t, hits, 1)
	assert.Equal(t, "m-b", hits[0].ID)

	// 空 query 退化为按时间倒序
	hits = env.memory.SearchForInjection(ctx, teamMemoryTestUser, teamA.ID, "", 2)
	require.Len(t, hits, 2)
	assert.Equal(t, "m-new", hits[0].ID)

	// 他人组检索返回空
	hits = env.memory.SearchForInjection(ctx, "user-2", teamA.ID, "深度学习", 8)
	assert.Empty(t, hits)
}

// TestTeamMemory_FormatInjectionBlock 注入区块格式与预算截断
func TestTeamMemory_FormatInjectionBlock(t *testing.T) {
	env := setupTeamMemoryTest(t)

	// 空记忆返回空串
	assert.Empty(t, env.memory.FormatInjectionBlock(nil, 0))

	memories := []model.TeamMemory{
		{ID: "m1", Content: "用户偏好简洁的回答", Tags: "偏好"},
		{ID: "m2", Content: "项目代号是 Eleball"},
		{ID: "m3", Content: "预算外应被截断的第三条记忆"},
	}
	block := env.memory.FormatInjectionBlock(memories, TeamMemoryInjectMaxChars)
	assert.Contains(t, block, "组共享记忆（同组其他对话沉淀的事实，供参考，可能过期）：")
	assert.Contains(t, block, "- 用户偏好简洁的回答 [偏好]")
	assert.Contains(t, block, "- 项目代号是 Eleball")
	assert.Contains(t, block, "预算外应被截断的第三条记忆")

	// 预算截断：仅容纳 header + 第一条，后续按相关度顺序丢弃
	headerLen := len([]rune("组共享记忆（同组其他对话沉淀的事实，供参考，可能过期）："))
	firstLineLen := len([]rune("\n- 用户偏好简洁的回答 [偏好]"))
	block = env.memory.FormatInjectionBlock(memories, headerLen+firstLineLen)
	assert.Contains(t, block, "用户偏好简洁的回答")
	assert.NotContains(t, block, "项目代号是 Eleball")

	// 预算连一条都放不下：返回空串
	assert.Empty(t, env.memory.FormatInjectionBlock(memories, 5))
}

// fakeTeamMemoryLLM 返回固定行文本的 AgentLLMClient（提取解析测试用）
type fakeTeamMemoryLLM struct {
	response string
	calls    int
}

func (f *fakeTeamMemoryLLM) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
	f.calls++
	return &llm.ChatChunk{Delta: f.response}, nil
}

func (f *fakeTeamMemoryLLM) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk)
	close(ch)
	return ch, nil
}

// TestTeamMemory_ExtractAndStore 提取解析、写入与「无」短路
func TestTeamMemory_ExtractAndStore(t *testing.T) {
	env := setupTeamMemoryTest(t)
	ctx := context.Background()

	team, err := env.team.Create(teamMemoryTestUser, "项目组", "")
	require.NoError(t, err)

	// 解析：去项目符号/序号、跳过空行与「无」、混入噪音行
	fake := &fakeTeamMemoryLLM{response: "- 用户偏好简洁的回答\n1. 项目代号是 Eleball\n\n无\n  \n"}
	require.NoError(t, env.memory.ExtractAndStore(ctx, fake, "test-model", team.ID, teamMemoryTestUser, "conv-1", "我喜欢简短回复", "好的，已记住"))

	items, total, err := env.memory.ListMemories(teamMemoryTestUser, team.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	contents := []string{items[0].Content, items[1].Content}
	assert.Contains(t, contents, "用户偏好简洁的回答")
	assert.Contains(t, contents, "项目代号是 Eleball")
	// provenance 记录来源对话
	for _, m := range items {
		assert.Equal(t, "conv-1", m.SourceConversationID)
	}

	// 「无」短路：不写入任何条目
	team2, err := env.team.Create(teamMemoryTestUser, "空提取组", "")
	require.NoError(t, err)
	fakeNone := &fakeTeamMemoryLLM{response: "无"}
	require.NoError(t, env.memory.ExtractAndStore(ctx, fakeNone, "test-model", team2.ID, teamMemoryTestUser, "conv-2", "今天天气怎么样", "晴天"))
	_, total, err = env.memory.ListMemories(teamMemoryTestUser, team2.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// 单条超 200 字截断
	longLine := make([]rune, 250)
	for i := range longLine {
		longLine[i] = '事'
	}
	fakeLong := &fakeTeamMemoryLLM{response: string(longLine)}
	require.NoError(t, env.memory.ExtractAndStore(ctx, fakeLong, "test-model", team2.ID, teamMemoryTestUser, "conv-2", "u", "a"))
	items, _, err = env.memory.ListMemories(teamMemoryTestUser, team2.ID, 1, 20)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, 200, len([]rune(items[0].Content)))
}

// TestTeamMemory_ExtractAndStore_Dedup LIKE 粗去重：同组内已存在相似条目则跳过
func TestTeamMemory_ExtractAndStore_Dedup(t *testing.T) {
	env := setupTeamMemoryTest(t)
	ctx := context.Background()

	team, err := env.team.Create(teamMemoryTestUser, "项目组", "")
	require.NoError(t, err)

	fake := &fakeTeamMemoryLLM{response: "用户偏好简洁明了的回答风格"}
	require.NoError(t, env.memory.ExtractAndStore(ctx, fake, "m", team.ID, teamMemoryTestUser, "c1", "u", "a"))

	// 再次提取出前 10 字符完全一致的条目（后续表述不同）→ 判重跳过
	fake2 := &fakeTeamMemoryLLM{response: "用户偏好简洁明了的回答，不要啰嗦"}
	require.NoError(t, env.memory.ExtractAndStore(ctx, fake2, "m", team.ID, teamMemoryTestUser, "c2", "u", "a"))

	_, total, err := env.memory.ListMemories(teamMemoryTestUser, team.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// 内容不同的新条目正常写入；同批次内重复行各自判重
	fake3 := &fakeTeamMemoryLLM{response: "项目代号是 Eleball\n项目代号是 Eleball"}
	require.NoError(t, env.memory.ExtractAndStore(ctx, fake3, "m", team.ID, teamMemoryTestUser, "c3", "u", "a"))
	_, total, err = env.memory.ListMemories(teamMemoryTestUser, team.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
}

// TestTeamMemory_Tokenize 分词：空白切分 + CJK bigram
func TestTeamMemory_Tokenize(t *testing.T) {
	tokens := tokenizeMemoryQuery("深度学习 model 压缩")
	assert.Contains(t, tokens, "深度学习")
	assert.Contains(t, tokens, "model")
	assert.Contains(t, tokens, "深度")
	assert.Contains(t, tokens, "学习")
	// 去重且空查询返回 nil
	assert.Nil(t, tokenizeMemoryQuery("   "))
	uniq := tokenizeMemoryQuery("学习 学习")
	assert.Equal(t, 1, countToken(uniq, "学习"))
}

func countToken(tokens []string, tok string) int {
	n := 0
	for _, t := range tokens {
		if t == tok {
			n++
		}
	}
	return n
}

// TestTeamMemory_InjectedIntoSystemPrompt buildInitialMessages 注入组共享记忆区块（Agent Team P2）
func TestTeamMemory_InjectedIntoSystemPrompt(t *testing.T) {
	env := setupTeamMemoryTest(t)
	ctx := context.Background()

	team, err := env.team.Create(teamMemoryTestUser, "项目组", "")
	require.NoError(t, err)
	env.insertMemory(t, "m1", team.ID, teamMemoryTestUser, "项目代号是 Eleball", "", time.Now().Unix())

	// 装配最小 AgentService（仅 teamMemorySvc 参与 buildInitialMessages 注入路径）
	agentSvc := &AgentService{teamMemorySvc: env.memory}

	// 组内对话：system 消息尾部拼入「组共享记忆」区块
	msgs := agentSvc.buildInitialMessages(ctx, AgentExecuteRequest{Message: "项目代号是什么"}, nil, teamMemoryTestUser, team.ID)
	require.NotEmpty(t, msgs)
	systemContent, ok := msgs[0].Content.(string)
	require.True(t, ok)
	assert.Contains(t, systemContent, "组共享记忆")
	assert.Contains(t, systemContent, "项目代号是 Eleball")

	// 未分组对话（teamID 为空）：不注入
	msgs = agentSvc.buildInitialMessages(ctx, AgentExecuteRequest{Message: "项目代号是什么"}, nil, teamMemoryTestUser, "")
	systemContent, _ = msgs[0].Content.(string)
	assert.NotContains(t, systemContent, "组共享记忆")

	// 未装配 teamMemorySvc：不注入
	agentSvcBare := &AgentService{}
	msgs = agentSvcBare.buildInitialMessages(ctx, AgentExecuteRequest{Message: "项目代号是什么"}, nil, teamMemoryTestUser, team.ID)
	systemContent, _ = msgs[0].Content.(string)
	assert.NotContains(t, systemContent, "组共享记忆")
}
