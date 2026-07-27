package service

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/crypto"
	"github.com/eleball/gateway/pkg/llm"
	sqlite "github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Agent Team P3：编排协作单测（能力目录 / CallAssistant 限深、计数上限、子 session、摘要截断、用量累计 / PATCH 新字段）

const orchestratorTestUser = "user-orchestrator"

// scriptedLLM 可编程 AgentLLMClient 桩（按 chatFunc 逐次响应）
type scriptedLLM struct {
	chatFunc func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error)
}

func (s *scriptedLLM) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
	return s.chatFunc(ctx, req)
}

func (s *scriptedLLM) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk)
	close(ch)
	return ch, nil
}

var _ AgentLLMClient = (*scriptedLLM)(nil)

// orchestratorTestEnv 编排测试装配（助手 + 工具加载 + 最小 AgentService）
type orchestratorTestEnv struct {
	db          *gorm.DB
	assistant   *AssistantService
	teamSvc     *TeamService
	agentSvc    *AgentService
	sessionRepo *repository.AgentSessionRepo
	convSvc     *ConversationService
}

func setupOrchestratorTest(t *testing.T) *orchestratorTestEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// :memory: 数据库按连接隔离，限制单连接避免「no such table」
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.ChatConversation{}, &model.ChatMessage{},
		&model.AgentSession{}, &model.AgentSessionOutput{},
		&model.VIPPlan{}, &model.VIPSubscription{},
		&model.AgentItem{}, &model.AgentPurchase{}, &model.AgentUserTool{},
		&model.Assistant{}, &model.AssistantItem{}, &model.Team{},
	))

	userRepo := repository.NewUserRepo(db)
	require.NoError(t, userRepo.Create(&model.User{ID: orchestratorTestUser, Username: "orch", Role: model.UserRoleUser, Status: 1}))

	agentRepo := repository.NewAgentRepo(db)
	assistantSvc := NewAssistantService(db, repository.NewAssistantRepo(db), agentRepo)
	teamSvc := NewTeamService(db, repository.NewTeamRepo(db))
	assistantSvc.SetTeamService(teamSvc)

	driverRegistry := NewToolDriverRegistry()
	driverRegistry.Register(NewModuleDriver(nil, nil))
	// moduleRegistry=nil：跳过模块在线探测，专注编排链路
	loader := NewAgentToolLoader(agentRepo, driverRegistry, nil)

	convSvc := NewConversationService(repository.NewChatConversationRepo(db), newTestVIPService(db), t.TempDir())
	sessionRepo := repository.NewAgentSessionRepo(db)

	baseRegistry := NewToolRegistry()
	resolver := func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return nil, fmt.Errorf("测试未配置 LLM 客户端")
	}
	agentSvc := NewAgentService(convSvc, sessionRepo, userRepo, newTestVIPService(db), nil,
		NewNoOpEleAgentModelService(), NewFileSandbox(t.TempDir(), ""), baseRegistry,
		NewToolSchemaBuilder(baseRegistry), NewAgentTrigger(), resolver, "", 10, nil)
	agentSvc.SetAgentToolLoader(loader)
	agentSvc.SetAssistantService(assistantSvc)

	return &orchestratorTestEnv{
		db: db, assistant: assistantSvc, teamSvc: teamSvc,
		agentSvc: agentSvc, sessionRepo: sessionRepo, convSvc: convSvc,
	}
}

// createAssistant 直接落一条助手记录（gorm default:true 在零值时回填 shared，故显式 update 目标值）
func (e *orchestratorTestEnv) createAssistant(t *testing.T, id, name string, shared bool, teamID string) *model.Assistant {
	t.Helper()
	a := &model.Assistant{
		ID:          id,
		UserID:      orchestratorTestUser,
		Name:        name,
		Description: "描述-" + name,
		TeamID:      teamID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, e.db.Create(a).Error)
	require.NoError(t, e.db.Model(&model.Assistant{}).Where("id = ?", id).Update("shared", shared).Error)
	a.Shared = shared
	return a
}

// attachSkill 直接给助手挂一个秘技条目（跳过购买/激活校验，目录技能摘要展开用）
func (e *orchestratorTestEnv) attachSkill(t *testing.T, assistantID, agentID, agentName string) {
	t.Helper()
	require.NoError(t, e.db.Create(&model.AgentItem{
		ID: agentID, Name: agentName, Status: model.AgentStatusApproved,
		CreatorID: "official", CreatorName: "官方", CreatedAt: time.Now(),
	}).Error)
	require.NoError(t, e.db.Create(&model.AssistantItem{
		ID: uuid.New().String(), AssistantID: assistantID, AgentID: agentID, CreatedAt: time.Now(),
	}).Error)
}

// TestBuildCapabilityCatalog 能力目录过滤：shared=false 排除、跨组排除、全局可见、排除自身、上限 20
func TestBuildCapabilityCatalog(t *testing.T) {
	env := setupOrchestratorTest(t)
	ctx := context.Background()

	env.createAssistant(t, "a-global", "全局助手", true, "")
	env.createAssistant(t, "a-hidden", "隐藏助手", false, "")
	env.createAssistant(t, "a-team-x", "X 组助手", true, "team-x")
	env.createAssistant(t, "a-team-y", "Y 组助手", true, "team-y")
	env.createAssistant(t, "a-self", "自己", true, "")
	env.attachSkill(t, "a-global", "sku-search", "联网搜索")

	catalogIDs := func(catalog []AssistantCapability) map[string]AssistantCapability {
		m := make(map[string]AssistantCapability, len(catalog))
		for _, c := range catalog {
			m[c.ID] = c
		}
		return m
	}

	// team-x 组的编排者（绑定 a-self）：可见 全局 + X 组；不可见 隐藏 / Y 组 / 自身
	catalog := catalogIDs(env.assistant.BuildCapabilityCatalog(ctx, orchestratorTestUser, "a-self", "team-x"))
	assert.Contains(t, catalog, "a-global")
	assert.Contains(t, catalog, "a-team-x")
	assert.NotContains(t, catalog, "a-hidden")
	assert.NotContains(t, catalog, "a-team-y")
	assert.NotContains(t, catalog, "a-self")
	// 技能摘要展开
	assert.Equal(t, []string{"联网搜索"}, catalog["a-global"].Skills)

	// 未分组对话（teamID 空）：仅全局可见
	catalog = catalogIDs(env.assistant.BuildCapabilityCatalog(ctx, orchestratorTestUser, "", ""))
	assert.Contains(t, catalog, "a-global")
	assert.NotContains(t, catalog, "a-team-x")

	// 上限 20
	for i := 0; i < 25; i++ {
		env.createAssistant(t, fmt.Sprintf("a-bulk-%02d", i), fmt.Sprintf("批量%d", i), true, "")
	}
	catalogAll := env.assistant.BuildCapabilityCatalog(ctx, orchestratorTestUser, "", "")
	assert.Len(t, catalogAll, capabilityCatalogMaxSize)
}

// TestFormatCatalogBlock 目录区块格式（文档 §4.2）
func TestFormatCatalogBlock(t *testing.T) {
	assert.Empty(t, FormatCatalogBlock(nil))
	block := FormatCatalogBlock([]AssistantCapability{
		{ID: "a-1", Name: "搜索助手", Description: "只做搜索", Skills: []string{"联网搜索", "网页抓取"}},
		{ID: "a-2", Name: "写作助手", Description: "文案", Skills: nil},
	})
	assert.NotContains(t, block, "你可以委派") // 不再注入 system prompt，区块仅含助手清单
	assert.Contains(t, block, "- a-1: 搜索助手 — 只做搜索（技能：联网搜索、网页抓取）")
	assert.Contains(t, block, "- a-2: 写作助手 — 文案（技能：无）")
}

// callAssistantTestFixture CallAssistant 测试公共装配
type callAssistantTestFixture struct {
	env     *orchestratorTestEnv
	tool    *Tool
	rt      *callAssistantRuntime
	toolEnv *ToolEnv
	parent  *model.AgentSession
}

func setupCallAssistant(t *testing.T, client AgentLLMClient) *callAssistantTestFixture {
	t.Helper()
	env := setupOrchestratorTest(t)
	ctx := context.Background()

	env.createAssistant(t, "a-target", "目标助手", true, "")
	conv, err := env.convSvc.CreateConversation(ctx, orchestratorTestUser, CreateConversationReq{Title: "主对话"})
	require.NoError(t, err)
	parent, err := env.agentSvc.createSession(ctx, orchestratorTestUser, conv.ID, "主任务", "")
	require.NoError(t, err)

	rt := &callAssistantRuntime{client: client, model: "test-model"}
	catalog := []AssistantCapability{{ID: "a-target", Name: "目标助手", Description: "描述-目标助手"}}
	tool := env.agentSvc.buildCallAssistantTool(catalog, parent, rt)
	toolEnv := &ToolEnv{
		UserID:         orchestratorTestUser,
		ConversationID: conv.ID,
		SessionID:      parent.ID,
		Sandbox:        env.agentSvc.sandbox,
		SessionRepo:    env.sessionRepo,
		DelegateCalls:  new(int),
	}
	return &callAssistantTestFixture{env: env, tool: tool, rt: rt, toolEnv: toolEnv, parent: parent}
}

// TestCallAssistant_Schema 工具 schema：assistant_id enum 约束、required 字段
func TestCallAssistant_Schema(t *testing.T) {
	fix := setupCallAssistant(t, &scriptedLLM{chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
		return &llm.ChatChunk{Delta: "ok"}, nil
	}})
	assert.Equal(t, "CallAssistant", fix.tool.Name)
	props := fix.tool.Parameters["properties"].(map[string]interface{})
	idProp := props["assistant_id"].(map[string]interface{})
	assert.Equal(t, []string{"a-target"}, idProp["enum"])
	assert.ElementsMatch(t, []string{"assistant_id", "task"}, fix.tool.Parameters["required"])
}

// TestCallAssistant_SubLoopDepthLimit 子 registry 不含 CallAssistant（结构性限深）：
// 子模型尝试嵌套调用 CallAssistant 时应收到「未知工具」错误并继续给出最终回答；
// 同时校验子 session 记录 parent_session_id、子 Usage 经 accumulator 进账。
func TestCallAssistant_SubLoopDepthLimit(t *testing.T) {
	var schemaToolNames []string
	callIdx := 0
	client := &scriptedLLM{chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
		for _, ts := range req.Tools {
			if fn, ok := ts["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok {
					schemaToolNames = append(schemaToolNames, name)
				}
			}
		}
		callIdx++
		if callIdx == 1 {
			// 子模型尝试嵌套委派（应因子 registry 无 CallAssistant 而失败）
			return &llm.ChatChunk{
				ToolCalls: []llm.ToolCall{{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "CallAssistant", Arguments: `{"assistant_id":"a-target","task":"嵌套"}`}}},
				Usage:     &llm.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			}, nil
		}
		return &llm.ChatChunk{Delta: "子任务结果", Usage: &llm.Usage{PromptTokens: 2, CompletionTokens: 2, TotalTokens: 4}}, nil
	}}
	fix := setupCallAssistant(t, client)

	out, err := fix.tool.Func(context.Background(), map[string]interface{}{
		"assistant_id": "a-target",
		"task":         "搜索一下 Eleball",
		"context":      "项目背景",
	}, fix.toolEnv)
	require.NoError(t, err)
	assert.Equal(t, "子任务结果", out["summary"])
	assert.Equal(t, 1, out["tool_calls"])
	assert.NotEmpty(t, out["session_id"])

	// 子请求的工具 schema 不含 CallAssistant（结构性限深）
	assert.NotContains(t, schemaToolNames, "CallAssistant")

	// 子 session：parent 关联 + ToolChain 记录嵌套调用失败
	child, err := fix.env.sessionRepo.GetByID(out["session_id"].(string))
	require.NoError(t, err)
	assert.Equal(t, fix.parent.ID, child.ParentSessionID)
	assert.Equal(t, "succeeded", child.Status)
	assert.Contains(t, child.ToolChain, "未知工具: CallAssistant")

	// Agent Team P5：子用量改即时扣费（DeductWithSource），不再经 accumulator；本用例 billingService 为 nil 跳过扣费（计费由 TestCallAssistant_PerDelegateBilling 覆盖）
	// 委派计数 +1
	assert.Equal(t, 1, *fix.toolEnv.DelegateCalls)
}

// TestCallAssistant_DepthGuard Depth>0 直接拒绝（防御性兜底）
func TestCallAssistant_DepthGuard(t *testing.T) {
	fix := setupCallAssistant(t, &scriptedLLM{chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
		return &llm.ChatChunk{Delta: "ok"}, nil
	}})
	fix.toolEnv.Depth = 1
	_, err := fix.tool.Func(context.Background(), map[string]interface{}{
		"assistant_id": "a-target",
		"task":         "嵌套任务",
	}, fix.toolEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "限深")
}

// TestCallAssistant_DelegateLimit 每次 execute 委派上限 5 次
func TestCallAssistant_DelegateLimit(t *testing.T) {
	client := &scriptedLLM{chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
		return &llm.ChatChunk{Delta: "ok"}, nil
	}}
	fix := setupCallAssistant(t, client)

	for i := 0; i < callAssistantMaxDelegates; i++ {
		_, err := fix.tool.Func(context.Background(), map[string]interface{}{
			"assistant_id": "a-target",
			"task":         fmt.Sprintf("子任务 %d", i),
		}, fix.toolEnv)
		require.NoError(t, err, "第 %d 次委派应成功", i+1)
	}
	_, err := fix.tool.Func(context.Background(), map[string]interface{}{
		"assistant_id": "a-target",
		"task":         "第六次委派",
	}, fix.toolEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "上限")
	assert.Equal(t, callAssistantMaxDelegates, *fix.toolEnv.DelegateCalls)
}

// TestCallAssistant_SummaryTruncation 摘要截断至 4000 字符
func TestCallAssistant_SummaryTruncation(t *testing.T) {
	client := &scriptedLLM{chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
		return &llm.ChatChunk{Delta: strings.Repeat("长", 5000)}, nil
	}}
	fix := setupCallAssistant(t, client)

	out, err := fix.tool.Func(context.Background(), map[string]interface{}{
		"assistant_id": "a-target",
		"task":         "生成长文",
	}, fix.toolEnv)
	require.NoError(t, err)
	summary := out["summary"].(string)
	assert.Equal(t, callAssistantSummaryMaxRunes+3, len([]rune(summary))) // 4000 字 + "..."
	assert.True(t, strings.HasSuffix(summary, "..."))
}

// TestCallAssistant_NotInCatalog 目标助手不在能力目录内时拒绝
func TestCallAssistant_NotInCatalog(t *testing.T) {
	fix := setupCallAssistant(t, &scriptedLLM{chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
		return &llm.ChatChunk{Delta: "ok"}, nil
	}})
	_, err := fix.tool.Func(context.Background(), map[string]interface{}{
		"assistant_id": "a-not-exist",
		"task":         "任务",
	}, fix.toolEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "能力目录")
}

// TestCallAssistant_EmptyArgsRecovery 空参时返回含可委派助手 ID 的可恢复错误，
// 引导 LLM 照抄正确 assistant_id 并补 task（避免直接失败 / 弱模型空参调用）。
func TestCallAssistant_EmptyArgsRecovery(t *testing.T) {
	fix := setupCallAssistant(t, &scriptedLLM{chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
		return &llm.ChatChunk{Delta: "ok"}, nil
	}})
	// assistant_id 与 task 均空：错误应提示不能为空并列出可委派助手 ID
	_, err := fix.tool.Func(context.Background(), map[string]interface{}{}, fix.toolEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不能为空")
	assert.Contains(t, err.Error(), "a-target")

	// 仅 task 空：仍提示不能为空
	_, err = fix.tool.Func(context.Background(), map[string]interface{}{"assistant_id": "a-target"}, fix.toolEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不能为空")
}

// TestBuildChildSystemPrompt 子 agent system prompt：自定义优先；默认模板含技能名与技能提示（§9 修复）
func TestBuildChildSystemPrompt(t *testing.T) {
	assistant := &model.Assistant{Name: "搜索助手", Description: "只做搜索"}
	skills := []*model.AgentItem{
		{Name: "联网搜索", SystemPrompt: "始终以表格输出搜索结果"},
		{Name: "网页抓取"},
	}
	prompt := buildChildSystemPrompt(assistant, skills)
	assert.Contains(t, prompt, "你是专家助手 搜索助手，擅长 只做搜索。可用技能：联网搜索、网页抓取。独立完成任务，直接给出结果。")
	assert.Contains(t, prompt, "技能提示（联网搜索）：始终以表格输出搜索结果")
	assert.NotContains(t, prompt, "技能提示（网页抓取）")

	custom := &model.Assistant{Name: "自定义", SystemPrompt: "你是严格的代码审查员"}
	assert.Equal(t, "你是严格的代码审查员", buildChildSystemPrompt(custom, skills))
}

// TestAssistantService_UpdateP3Fields PATCH 新字段：system_prompt/shared/team_id 与组归属校验
func TestAssistantService_UpdateP3Fields(t *testing.T) {
	env := setupOrchestratorTest(t)

	view, err := env.assistant.Create(orchestratorTestUser, "协作助手", "可被委派")
	require.NoError(t, err)
	assert.True(t, view.Shared) // 新建默认 shared=true

	team, err := env.teamSvc.Create(orchestratorTestUser, "研发组", "")
	require.NoError(t, err)

	// 更新三字段
	prompt := "你是资深搜索专家"
	shared := false
	updated, err := env.assistant.Update(orchestratorTestUser, view.ID, AssistantUpdateInput{
		SystemPrompt: &prompt,
		Shared:       &shared,
		TeamID:       &team.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, prompt, updated.SystemPrompt)
	assert.False(t, updated.Shared)
	assert.Equal(t, team.ID, updated.TeamID)

	// 持久化复核（防止 gorm default:true 吞掉 false 更新）
	reloaded, err := env.assistant.Get(orchestratorTestUser, view.ID)
	require.NoError(t, err)
	assert.False(t, reloaded.Shared)
	assert.Equal(t, team.ID, reloaded.TeamID)

	// 不存在的组 / 他人组：拒绝
	_, err = env.assistant.Update(orchestratorTestUser, view.ID, AssistantUpdateInput{TeamID: strPtr("team-not-exist")})
	require.Error(t, err)

	// 置空 = 移出组（全局可见）
	updated, err = env.assistant.Update(orchestratorTestUser, view.ID, AssistantUpdateInput{TeamID: strPtr("")})
	require.NoError(t, err)
	assert.Empty(t, updated.TeamID)
}

// TestAssistantService_UpdateLLMConfig PATCH LLM 配置：mode/provider/model/base_url + api_key 加密入库 + llm_api_key_set
func TestAssistantService_UpdateLLMConfig(t *testing.T) {
	env := setupOrchestratorTest(t)
	// 装配加密器（32 字节主密钥，hex）
	ke, err := crypto.NewKeyEncryption("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	env.assistant.SetKeyEncryption(ke)

	view, err := env.assistant.Create(orchestratorTestUser, "LLM 助手", "可绑模型")
	require.NoError(t, err)

	// follow -> eleagent（复用服务端凭据，无 api_key）
	eleMode := "eleagent"
	eleModel := "qwen/Qwen3-8B"
	updated, err := env.assistant.Update(orchestratorTestUser, view.ID, AssistantUpdateInput{
		LLMMode:  &eleMode,
		LLMModel: &eleModel,
	})
	require.NoError(t, err)
	assert.Equal(t, "eleagent", updated.LLMMode)
	assert.Equal(t, "qwen/Qwen3-8B", updated.LLMModel)
	assert.False(t, updated.LLMAPIKeySet) // eleagent 无 api_key

	// byok：设 api_key -> 加密入库 + llm_api_key_set=true，密钥本身不回读明文
	byokMode := "byok"
	provider := "OPENAI"
	baseURL := "https://api.openai.com/v1"
	apiKey := "sk-test-123"
	updated, err = env.assistant.Update(orchestratorTestUser, view.ID, AssistantUpdateInput{
		LLMMode:     &byokMode,
		LLMProvider: &provider,
		LLMModel:    &eleModel,
		LLMBaseURL:  &baseURL,
		LLMAPIKey:   &apiKey,
	})
	require.NoError(t, err)
	assert.Equal(t, "byok", updated.LLMMode)
	assert.True(t, updated.LLMAPIKeySet)
	assert.NotEmpty(t, updated.LLMAPIKey)                // 密文非空
	assert.NotEqual(t, "sk-test-123", updated.LLMAPIKey) // 非明文

	// 持久化复核：重读后解密得到明文
	reloaded, err := env.assistant.Get(orchestratorTestUser, view.ID)
	require.NoError(t, err)
	assert.True(t, reloaded.LLMAPIKeySet)
	plain, err := ke.Decrypt(reloaded.LLMAPIKey, reloaded.LLMAPIKeyNonce)
	require.NoError(t, err)
	assert.Equal(t, "sk-test-123", plain)

	// 清除 api_key（空字符串）
	empty := ""
	updated, err = env.assistant.Update(orchestratorTestUser, view.ID, AssistantUpdateInput{LLMAPIKey: &empty})
	require.NoError(t, err)
	assert.False(t, updated.LLMAPIKeySet)
	assert.Empty(t, updated.LLMAPIKey)

	// encrypt 未装配时拒绝写 api_key
	env.assistant.SetKeyEncryption(nil)
	_, err = env.assistant.Update(orchestratorTestUser, view.ID, AssistantUpdateInput{LLMAPIKey: &apiKey})
	require.Error(t, err)
}

// TestResolveSubClient_Validation 子 LLM 解析：follow 回落主 client/billing；eleagent/byok 配置校验
func TestResolveSubClient_Validation(t *testing.T) {
	env := setupOrchestratorTest(t)
	ctx := context.Background()
	rt := &callAssistantRuntime{client: &scriptedLLM{chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
		return &llm.ChatChunk{Delta: "ok"}, nil
	}}, model: "main-model", billingProvider: "eleagent", billingModel: "qwen/main"}

	// follow：回落主 client/model/billing
	a := &model.Assistant{LLMMode: ""}
	c, m, bp, bm, err := env.agentSvc.resolveSubClient(ctx, a, rt)
	require.NoError(t, err)
	assert.NotNil(t, c)
	assert.Equal(t, "main-model", m)
	assert.Equal(t, "eleagent", bp)
	assert.Equal(t, "qwen/main", bm)

	// eleagent 未配模型
	a2 := &model.Assistant{LLMMode: "eleagent"}
	_, _, _, _, err = env.agentSvc.resolveSubClient(ctx, a2, rt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未配置 Ele Agent 模型")

	// byok 配置不完整（缺 base_url）
	a3 := &model.Assistant{LLMMode: "byok", LLMProvider: "OPENAI", LLMModel: "gpt-4o-mini"}
	_, _, _, _, err = env.agentSvc.resolveSubClient(ctx, a3, rt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BYOK 配置不完整")
}

// TestCallAssistant_StreamsProgress 子任务进度经 SSE writer 流式下发（带 session_id 标记）
func TestCallAssistant_StreamsProgress(t *testing.T) {
	callIdx := 0
	client := &scriptedLLM{chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
		callIdx++
		if callIdx == 1 {
			// 子模型先尝试调一个子 registry 没有的工具（触发 onToolCall，写 tool_call/tool_result 事件）
			return &llm.ChatChunk{
				ToolCalls: []llm.ToolCall{{ID: "tc1", Type: "function", Function: llm.ToolCallFunction{Name: "SomeTool", Arguments: `{}`}}},
				Usage:     &llm.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			}, nil
		}
		return &llm.ChatChunk{Delta: "子任务结果", Usage: &llm.Usage{PromptTokens: 2, CompletionTokens: 2, TotalTokens: 4}}, nil
	}}
	fix := setupCallAssistant(t, client)
	var buf bytes.Buffer
	fix.rt.writer = &buf

	out, err := fix.tool.Func(context.Background(), map[string]interface{}{
		"assistant_id": "a-target",
		"task":         "做点什么",
	}, fix.toolEnv)
	require.NoError(t, err)
	assert.Equal(t, "子任务结果", out["summary"])

	// 流式事件落到 writer：含 tool_call/tool_result + session_id 标记
	stream := buf.String()
	assert.Contains(t, stream, "event: tool_call")
	assert.Contains(t, stream, "event: tool_result")
	assert.Contains(t, stream, "session_id")
	assert.Contains(t, stream, out["session_id"].(string))
}

// TestTeamService_DeleteClearsAssistantRefs 删除组时清 assistants.team_id（文档 §3）
func TestTeamService_DeleteClearsAssistantRefs(t *testing.T) {
	env := setupOrchestratorTest(t)
	team, err := env.teamSvc.Create(orchestratorTestUser, "临时组", "")
	require.NoError(t, err)
	env.createAssistant(t, "a-in-team", "组内助手", true, team.ID)

	require.NoError(t, env.teamSvc.Delete(orchestratorTestUser, team.ID))
	reloaded, err := env.assistant.Get(orchestratorTestUser, "a-in-team")
	require.NoError(t, err)
	assert.Empty(t, reloaded.TeamID)
}

func strPtr(s string) *string { return &s }
