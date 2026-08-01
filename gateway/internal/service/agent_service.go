package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	"go.uber.org/zap"
)

const (
	MaxAgentSessionsVIP    = 100
	MaxAgentSessionsNormal = 10
)

// AgentLLMClientResolver 根据用户当前模型配置解析 LLM 客户端
type AgentLLMClientResolver func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error)

// AgentService Agent 工作流服务
type AgentService struct {
	conversationSvc      *ConversationService
	sessionRepo          *repository.AgentSessionRepo
	userRepo             *repository.UserRepo
	vipService           *VIPService
	billingService       *BillingService
	eleAgentModelService *EleAgentModelService
	sandbox              *FileSandbox
	registry             *ToolRegistry
	schemaBuilder        *ToolSchemaBuilder
	trigger              *AgentTrigger
	toolLoop             *ToolCallingLoop
	agentToolLoader      *AgentToolLoader
	assistantSvc         *AssistantService
	teamMemorySvc        *TeamMemoryService
	clientResolver       AgentLLMClientResolver
	model                string
	maxSteps             int
	maxCostPerTask       int64 // AR-03：CallAssistant 子任务单次成本上限（弹丸，0 不限制）
	logger               *zap.Logger
	// unrestricted=true 时跳过 VIP 门控（Agent 模式/文件工具/试用次数），并容忍本地 user 未命中。
	// claw 本地不限 Agent 模式（云端账户统一后，本地无 users 行），置 true；云端 cmd/server 保持 false。
	unrestricted bool
	// C1：工具审批注册表（跨 execute 共享，循环注册+阻塞，/agent/approve 投递）。
	approvals *approvalRegistry
	// C1：权限决策引擎（规则解析 + always-allow 持久化）。nil 时审批闸跳过规则直接 ask/allow。
	permissionSvc *PermissionService
	// C3：plan 文件目录（claw 装配 basePath/plans）。空则 ExitPlanMode 不落 plan 文件，仅传内容。
	plansDir string
}

// SetUnrestricted 设置是否跳过 VIP 门控（claw 本地不限 Agent 模式）。
func (s *AgentService) SetUnrestricted(b bool) {
	s.unrestricted = b
}

// NewAgentService 创建服务
func NewAgentService(
	conversationSvc *ConversationService,
	sessionRepo *repository.AgentSessionRepo,
	userRepo *repository.UserRepo,
	vipService *VIPService,
	billingService *BillingService,
	eleAgentModelService *EleAgentModelService,
	sandbox *FileSandbox,
	registry *ToolRegistry,
	schemaBuilder *ToolSchemaBuilder,
	trigger *AgentTrigger,
	clientResolver AgentLLMClientResolver,
	model string,
	maxSteps int,
	logger *zap.Logger,
) *AgentService {
	if maxSteps <= 0 {
		maxSteps = 500
	}
	svc := &AgentService{
		conversationSvc:      conversationSvc,
		sessionRepo:          sessionRepo,
		userRepo:             userRepo,
		vipService:           vipService,
		billingService:       billingService,
		eleAgentModelService: eleAgentModelService,
		sandbox:              sandbox,
		registry:             registry,
		schemaBuilder:        schemaBuilder,
		trigger:              trigger,
		toolLoop:             NewToolCallingLoop(registry, maxSteps),
		clientResolver:       clientResolver,
		model:                model,
		maxSteps:             maxSteps,
		logger:               logger,
		approvals:            newApprovalRegistry(),
	}
	svc.toolLoop.SetLogger(logger)
	return svc
}

// SetPermissionService 装配权限决策引擎（C1）。claw 启用时注入；云端不注入（审批闸跳过）。
func (s *AgentService) SetPermissionService(svc *PermissionService) {
	s.permissionSvc = svc
}

// SetPlansDir C3：设置 plan 文件目录（claw 装配 basePath/plans）。空则 ExitPlanMode 不落盘。
func (s *AgentService) SetPlansDir(dir string) {
	s.plansDir = dir
}

// SetMaxRetries 设置 Agent 工具循环上游可重试错误的最大尝试次数（对应 llm.max_retries 配置）
func (s *AgentService) SetMaxRetries(n int) {
	if s.toolLoop != nil {
		s.toolLoop.SetMaxRetries(n)
	}
}

// SetTokenBudget 设置单次 Agent 执行的 token 预算上限（AR-03，对应 agent.max_tokens_per_execute）
func (s *AgentService) SetTokenBudget(b int) {
	if s.toolLoop != nil {
		s.toolLoop.SetTokenBudget(b)
	}
}

// SetMaxCostPerTask 设置 CallAssistant 子任务的单次成本上限（AR-03，对应 agent.max_cost_per_task）。
// 0 或负数表示不限制；编排器据此装配 env.CostGuard，每轮按 billingService.EstimateCost 估算累计成本并门控。
func (s *AgentService) SetMaxCostPerTask(c int64) {
	if c > 0 {
		s.maxCostPerTask = c
	}
}

// AgentExecuteRequest Agent 执行请求
type AgentExecuteRequest struct {
	SessionID       string            `json:"session_id"`
	ConversationID  string            `json:"conversation_id"`
	Message         string            `json:"message"`
	Content         string            `json:"content"`
	Attachments     []AgentAttachment `json:"attachments"`
	History         []llm.Message     `json:"history"`
	Model           string            `json:"model"`
	Provider        string            `json:"provider"`
	BaseURL         string            `json:"base_url"`
	APIKey          string            `json:"api_key"`
	EnableTools     *bool             `json:"enable_tools,omitempty"`
	EnableWebSearch *bool             `json:"enable_web_search,omitempty"`
	SearchProvider  *string           `json:"search_provider,omitempty"`
	// AssistantID 本次执行应用的助手（非空优先于会话绑定的 assistant_id）
	AssistantID string `json:"assistant_id"`
	// AR-06：claw 本地工作目录（用户授权的项目目录）。仅 claw（unrestricted=true）启用；
	// 云端多租户忽略此字段，不启用 cwd 解析。
	Cwd string `json:"cwd"`
	// PermissionMode C1 权限模式覆盖（default/acceptEdits/plan/auto）。空则用会话持久化的 conv.PermissionMode。
	PermissionMode *string `json:"permission_mode,omitempty"`
}

// normalizeRequest 兼容前端可能使用的 content / message 字段
func (req *AgentExecuteRequest) normalize() {
	if req.Message == "" && req.Content != "" {
		req.Message = req.Content
	}
}

// SetAgentToolLoader 设置动态工具加载器（可选）
func (s *AgentService) SetAgentToolLoader(loader *AgentToolLoader) {
	s.agentToolLoader = loader
}

// SetAssistantService 设置助手服务（可选；设置后按请求/会话绑定的助手过滤动态工具）
func (s *AgentService) SetAssistantService(svc *AssistantService) {
	s.assistantSvc = svc
}

// SetTeamMemoryService 设置组共享记忆服务（Agent Team P2，可选；设置后组内对话执行前注入记忆、执行后异步提取）
func (s *AgentService) SetTeamMemoryService(svc *TeamMemoryService) {
	s.teamMemorySvc = svc
}

// Execute 执行 Agent 工作流
func (s *AgentService) Execute(ctx context.Context, req AgentExecuteRequest, w io.Writer) error {
	req.normalize()

	// 1. 权限判定
	userID, ok := ctx.Value("user_id").(string)
	if !ok {
		s.writeEvent(w, "error", map[string]string{"message": "未登录"})
		s.writeEvent(w, "done", nil)
		return nil
	}
	// unrestricted（claw）：容忍本地 user 未命中（云端账户统一后本地无 users 行），跳过 VIP 门控。
	var user *model.User
	var err error
	if s.unrestricted {
		user, err = s.userRepo.GetByID(userID)
		if user == nil {
			user = &model.User{ID: userID, Role: model.UserRoleUser} // 兜底：非 admin、VIP0（门控已跳过）
		}
	} else {
		user, err = s.userRepo.GetByID(userID)
		if err != nil {
			s.writeEvent(w, "error", map[string]string{"message": "获取用户信息失败"})
			s.writeEvent(w, "done", nil)
			return nil
		}
	}
	hasAgentMode := true
	hasFileTools := true
	if !s.unrestricted {
		hasAgentMode, _ = s.vipService.HasFeature(userID, model.VIPFeatureAgentMode)
		hasFileTools, _ = s.vipService.HasFeature(userID, model.VIPFeatureFileTools)
	}
	isAdmin := user.Role == model.UserRoleAdmin

	// 2. 获取或创建 conversation
	conv, err := s.conversationSvc.GetOrCreate(ctx, userID, req.ConversationID)
	if err != nil {
		s.writeEvent(w, "error", map[string]string{"message": err.Error()})
		s.writeEvent(w, "done", nil)
		return nil
	}
	req.ConversationID = conv.ID

	// 3. 同步 Agent 工具开关与联网搜索设置
	enableTools := conv.EnableTools
	if req.EnableTools != nil {
		enableTools = *req.EnableTools
	}
	enableWebSearch := conv.EnableWebSearch
	if req.EnableWebSearch != nil {
		enableWebSearch = *req.EnableWebSearch
	}
	searchProvider := conv.SearchProvider
	if req.SearchProvider != nil && *req.SearchProvider != "" {
		searchProvider = *req.SearchProvider
	}
	// C1：权限模式解析（会话默认 + 请求覆盖），注入 env 供工具循环审批闸消费
	permissionMode := model.NormalizePermissionMode(conv.PermissionMode)
	if req.PermissionMode != nil && *req.PermissionMode != "" {
		permissionMode = model.NormalizePermissionMode(*req.PermissionMode)
	}
	// 同时持久化到 conversation（AR-23：req.Cwd 非空时一并写回会话，供后续 execute 回填）
	if req.EnableTools != nil || req.EnableWebSearch != nil || req.SearchProvider != nil || req.Cwd != "" {
		updateReq := UpdateConversationReq{
			EnableTools:     req.EnableTools,
			EnableWebSearch: req.EnableWebSearch,
			SearchProvider:  req.SearchProvider,
		}
		if req.Cwd != "" {
			cwdVal := req.Cwd
			updateReq.Cwd = &cwdVal
		}
		if err := s.conversationSvc.Update(ctx, conv.ID, userID, updateReq); err != nil {
			s.logger.Warn("同步 conversation 工具/搜索设置失败", zap.Error(err))
		}
	}

	// 3.1 计费参数准备：Agent 工作流统一按当前主交互模型计费，仅消耗弹丸
	billingProvider, billingModel := s.billingProviderAndModel(req)

	// 累计本次执行的主模型 token 用量，用于最终扣费
	var totalUsage *llm.Usage
	defer func() {
		if s.billingService != nil && totalUsage != nil {
			// AR-03：会话成本归集--主执行用量归属当前对话
			attr := BillingAttribution{ConversationID: conv.ID}
			if err := s.billingService.DeductWithAttribution(userID, billingProvider, billingModel, CurrencyDanwan, "agent", attr, totalUsage); err != nil && s.logger != nil {
				s.logger.Warn("Agent 工作流扣费失败", zap.String("user_id", userID), zap.String("provider", billingProvider), zap.String("model", billingModel), zap.Error(err))
			}
		}
	}()

	// 3.2 余额预检：仅对 Ele Agent 付费模型生效，BYOK 模型免费
	if s.billingService != nil {
		if err := s.billingService.CheckBalance(userID, billingProvider, billingModel, CurrencyDanwan); err != nil {
			s.writeEvent(w, "error", map[string]string{"message": err.Error()})
			s.writeEvent(w, "done", nil)
			return nil
		}
	}

	// 4. 开关关闭：走普通对话
	if !enableTools {
		usage, _ := s.chatStream(ctx, req, w)
		totalUsage = usage
		return nil
	}

	// 5. Agent 工具仅对 Ele Agent 模型开放（BYOK 模型不提供入口）
	if req.Provider != "" && llm.Provider(req.Provider) != llm.ProviderEleAgent {
		s.writeEvent(w, "error", map[string]string{
			"message": "Agent 工具当前仅支持 Ele Agent 模型，请在模型设置中切换",
		})
		s.writeEvent(w, "done", nil)
		return nil
	}

	// 5.1 用户未开通 Agent 模式时禁止启用工具
	if !hasAgentMode && !isAdmin {
		s.writeEvent(w, "error", map[string]string{
			"message": "该功能需要升级弹丸VIP",
		})
		s.writeEvent(w, "done", nil)
		return nil
	}

	// 5.2 提前校验当前 Ele Agent 模型是否支持工具调用，避免消耗试用次数后再报错
	if llm.Provider(req.Provider) == llm.ProviderEleAgent && s.eleAgentModelService != nil {
		subProvider, subModel, err := parseEleAgentModel(req.Model)
		if err == nil && !s.eleAgentModelService.GetModelToolSupport(subProvider, subModel) {
			s.writeEvent(w, "error", map[string]string{
				"message": "当前选择的 Ele Agent 模型不支持工具调用，请在模型设置中切换至支持 Agent 工具的模型",
			})
			s.writeEvent(w, "done", nil)
			return nil
		}
	}

	// 5.3 VIP0 用户通过试用次数使用 Agent 模式，每次进入消耗一次（unrestricted/claw 跳过）
	if !s.unrestricted && !isAdmin && user.VIPLevel == 0 {
		if err := s.vipService.ConsumeAgentTrial(userID); err != nil {
			s.writeEvent(w, "error", map[string]string{
				"message": "Agent 模式试用次数已用完，请升级弹丸VIP",
			})
			s.writeEvent(w, "done", nil)
			return nil
		}
	}

	// 6. 非 VIP 且上传了需要服务器端工具的附件
	triggerResult := s.trigger.Detect(req.Message, req.Attachments)
	if !hasFileTools && triggerResult.NeedsServerTools {
		s.writeEvent(w, "error", map[string]string{
			"message": "文件处理需要更多 Agent 工具支持，请升级弹丸VIP",
		})
		s.writeEvent(w, "done", nil)
		return nil
	}

	// 7. 预处理附件
	preprocessed, err := s.trigger.PreprocessAttachments(ctx, req.Attachments, conv.ID)
	if err != nil {
		s.writeEvent(w, "error", map[string]string{"message": err.Error()})
		s.writeEvent(w, "done", nil)
		return nil
	}

	// AR-06：claw cwd 解析（仅 unrestricted=true 即 claw 启用；云端忽略 req.Cwd 维持多租户隔离）。
	// EvalSymlinks 防软链逃逸，Stat 校验为目录。无效时 cwd 留空，回退会话沙箱。
	resolvedCwd := ""
	if s.unrestricted {
		// AR-23：cwd 跟会话走--前端没传 req.Cwd 时回填会话持久化的 conv.Cwd
		cwdSrc := req.Cwd
		if cwdSrc == "" {
			cwdSrc = conv.Cwd
		}
		if cwdSrc != "" {
			if abs, aErr := filepath.Abs(cwdSrc); aErr == nil {
				if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
					if resolved, eErr := filepath.EvalSymlinks(abs); eErr == nil {
						resolvedCwd = filepath.Clean(resolved)
					} else {
						resolvedCwd = filepath.Clean(abs)
					}
				}
			}
		}
	}

	// 8. 创建 Agent Session
	session, err := s.createSession(ctx, userID, conv.ID, req.Message, resolvedCwd)
	if err != nil {
		s.writeEvent(w, "error", map[string]string{"message": err.Error()})
		s.writeEvent(w, "done", nil)
		return nil
	}

	// 执行结束后更新 Session 状态；执行成功为 succeeded，任何错误事件发出后为 failed
	execErr := error(nil)
	toolChainJSON := ""
	stepCount := 0 // AR-07：工具调用步数（result 声明在 defer 之后，无法在 defer 内引用，故用闭包变量暂存）
	defer func() {
		status := "succeeded"
		if execErr != nil {
			status = "failed"
		}
		// AR-07：累计 token 与估算成本（弹丸）。totalUsage 在 defer 时已定型；claw 无 billing 时 costAmount=0
		var totalTokens int64
		var costAmount int64
		if totalUsage != nil {
			totalTokens = int64(totalUsage.TotalTokens)
			if totalTokens == 0 {
				totalTokens = int64(totalUsage.PromptTokens + totalUsage.CompletionTokens)
			}
			if s.billingService != nil {
				costAmount = s.billingService.EstimateCost(billingProvider, billingModel, CurrencyDanwan, totalUsage)
			}
		}
		if err := s.updateSessionStatus(session.ID, status, toolChainJSON, totalTokens, stepCount, costAmount); err != nil && s.logger != nil {
			s.logger.Warn("更新 Agent Session 状态失败", zap.String("session_id", session.ID), zap.String("status", status), zap.Error(err))
		}
	}()

	// 9. 构建可用工具列表（根据联网开关决定是否暴露 SearchWeb / FetchURL）
	// 克隆注册表并注入用户购买的动态工具，实现集市 SKU 的动态加载。
	// 助手过滤：请求 assistant_id 优先，缺省回落会话绑定值；指定助手时仅注入该助手包含的秘技，
	// 助手条目为空时不注入任何动态工具（空列表，不报错）；未指定助手则注入全部已激活秘技。
	assistantID := req.AssistantID
	if assistantID == "" {
		assistantID = conv.AssistantID
	}
	var dynamicTools []*Tool
	if s.agentToolLoader != nil {
		var loaded []*Tool
		var loadErr error
		if assistantID != "" && s.assistantSvc != nil {
			agentIDs, aidErr := s.assistantSvc.AgentIDsFor(userID, assistantID)
			if aidErr != nil && s.logger != nil {
				s.logger.Warn("解析助手秘技集合失败", zap.String("assistant_id", assistantID), zap.Error(aidErr))
			}
			loaded, loadErr = s.agentToolLoader.LoadToolsForUserFiltered(ctx, userID, agentIDs)
		} else {
			loaded, loadErr = s.agentToolLoader.LoadToolsForUser(ctx, userID)
		}
		if loadErr != nil && s.logger != nil {
			s.logger.Warn("加载用户动态工具失败", zap.String("user_id", userID), zap.Error(loadErr))
		}
		dynamicTools = loaded
	}

	// Agent Team P3：构建协作助手能力目录；目录非空时注入 CallAssistant 编排工具
	// （随 dynamicTools 进 schema 与注册表）。能力清单写入工具 description（见 buildCallAssistantTool），
	// 不再注入 system prompt，避免每轮把「可委派」推到 LLM 眼前而挤占 SearchWeb/FetchURL 等既有工具。
	// callRT 由下方客户端解析后填充，CallAssistant 闭包在实际被工具循环调用时读取。
	var callRT callAssistantRuntime
	if s.assistantSvc != nil && s.agentToolLoader != nil {
		catalog := s.assistantSvc.BuildCapabilityCatalog(ctx, userID, assistantID, conv.TeamID)
		if len(catalog) > 0 {
			dynamicTools = append(dynamicTools, s.buildCallAssistantTool(catalog, session, &callRT))
		}
	}

	registry := s.registry.Clone()
	for _, t := range dynamicTools {
		registry.Register(t)
	}
	availableTools := s.schemaBuilder.BuildWithOptionsAndDynamic(hasFileTools, enableWebSearch, dynamicTools, permissionMode)

	// 10. 构建初始消息（Agent Team P2：组内对话注入组共享记忆区块；P3：能力目录区块）
	// C7：按模型视觉能力构建附件 content parts（非视觉模型图片走 OCR 降级）
	agentModelName := req.Model
	if agentModelName == "" {
		agentModelName = s.model
	}
	messages := s.buildInitialMessages(ctx, req, preprocessed, userID, conv.TeamID, resolvedCwd, true, s.supportsVision(req.Provider, agentModelName), permissionMode)

	// 11. Function Calling 循环
	// AR-03：执行中余额校验节流计数器（每 balanceCheckEvery 步查一次 DB，避免每轮查库）
	balanceCheckStep := 0
	const balanceCheckEvery = 5
	// C1：claw 装配审批器（云端 nil，审批闸跳过）。审批器持有本次 SSE writer，
	// 阻塞期间 /agent/approve 经共享 registry 投递决策。
	var approver Approver
	if s.unrestricted {
		approver = &sseApprover{svc: s, writer: w}
	}
	env := &ToolEnv{
		UserID:         userID,
		ConversationID: conv.ID,
		SessionID:      session.ID,
		Sandbox:        s.sandbox,
		Cwd:            resolvedCwd, // AR-06：claw 工作目录（空则文件工具回退会话沙箱）
		SessionRepo:    s.sessionRepo,
		SearchProvider: searchProvider,
		// C1 权限审批
		PermissionMode: permissionMode,
		PermissionSvc:  s.permissionSvc,
		Approver:       approver,
		// C3 plan 模式：plan 文件目录（claw 装配；云端空，ExitPlanMode 仅传内容）
		PlansDir: s.plansDir,
		// Agent Team P3：委派计数器（每次 execute 独立，上限 5）+ 子调用用量累计钩子
		// （子 Usage 只经此钩子进 totalUsage 一次，与 result.Usage 的 addUsage 不重叠）
		DelegateCalls: new(int),
		UsageAccumulator: func(u *llm.Usage) {
			totalUsage = addUsage(totalUsage, u)
		},
		// AR-03：执行中余额校验，超支则优雅结束循环
		BudgetGuard: func() error {
			if s.billingService == nil {
				return nil
			}
			balanceCheckStep++
			if balanceCheckStep%balanceCheckEvery != 0 {
				return nil
			}
			return s.billingService.CheckBalance(userID, billingProvider, billingModel, CurrencyDanwan)
		},
	}
	// AR-06：cwd 非空时用带 projectRoot 的沙箱克隆（per-session，放行 cwd 第三根读写）
	if resolvedCwd != "" {
		env.Sandbox = s.sandbox.WithProjectRoot(resolvedCwd)
	}

	modelName := s.model
	if req.Model != "" {
		modelName = req.Model
	}

	// 根据用户当前模型配置解析 LLM 客户端
	llmClient, err := s.resolveClient(ctx, req, modelName)
	if err != nil {
		execErr = err
		s.writeEvent(w, "error", map[string]string{"message": err.Error()})
		s.writeEvent(w, "done", nil)
		return nil
	}

	// Ele Agent 模型名为 subProvider/subModel，上游真实请求只使用 subModel
	modelName = normalizeAgentModelName(req.Provider, modelName)

	// Agent Team P3：填充编排运行时（CallAssistant 被工具循环调用时读取）
	// Agent Team P5：透传 SSE writer（子任务进度流式）+ 主对话计费口径（follow 模式复用）
	callRT.client = llmClient
	callRT.model = modelName
	callRT.writer = w
	callRT.billingProvider = billingProvider
	callRT.billingModel = billingModel

	result, err := s.toolLoop.RunWithRegistry(ctx, registry, llmClient, modelName, availableTools, messages, env,
		func(record ToolCallRecord) error {
			s.writeEvent(w, "tool_call", map[string]interface{}{
				"step":      record.Step,
				"tool":      record.Tool,
				"arguments": json.RawMessage(record.Arguments),
			})
			s.writeEvent(w, "tool_result", map[string]interface{}{
				"step":          record.Step,
				"tool":          record.Tool,
				"status":        map[bool]string{true: "succeeded", false: "failed"}[record.Error == ""],
				"output":        record.Output,
				"error_message": record.Error,
			})
			// 如果工具产生了可下载资源，下发 resource 事件供前端展示下载入口
			if record.Output != nil {
				if resourceID, ok := record.Output["resource_id"].(string); ok && resourceID != "" {
					fileName, _ := record.Output["path"].(string)
					mimeType, _ := record.Output["mime_type"].(string)
					s.writeEvent(w, "resource", map[string]interface{}{
						"resource_id":  resourceID,
						"file_name":    fileName,
						"mime_type":    mimeType,
						"download_url": fmt.Sprintf("/v1/agent/resources/%s", resourceID),
					})
				}
			}
			return nil
		},
		func(output AssistantOutput) {
			if output.ReasoningContent != "" {
				s.writeEvent(w, "reasoning", map[string]string{"delta": output.ReasoningContent})
			}
			if !output.IsFinal && output.Delta != "" {
				s.writeEvent(w, "intermediate_answer", map[string]string{"delta": output.Delta})
			}
		},
	)
	if err != nil {
		execErr = err
		s.writeEvent(w, "error", map[string]string{"message": err.Error()})
		s.writeEvent(w, "done", nil)
		return nil
	}

	// 累计 Function Calling 循环的 token 用量
	totalUsage = addUsage(totalUsage, result.Usage)

	// 将工具调用记录持久化到 Session，便于历史回看
	if len(result.Records) > 0 {
		if b, err := json.Marshal(result.Records); err == nil {
			toolChainJSON = string(b)
		}
	}
	stepCount = len(result.Records) // AR-07：工具步数供用量展示

	// 11. 输出最终回答
	if result.LoopDetected {
		// 检测到同工具同参数循环调用，强制进入最终回答
		s.writeEvent(w, "warning", map[string]string{"message": "检测到工具循环调用，将基于已有结果生成回答"})
	} else if result.ReachMaxSteps {
		// 使用 warning 事件而非 error，避免前端把“已达上限”误判为失败并丢弃后续最终回答
		s.writeEvent(w, "warning", map[string]string{"message": "工具调用次数已达上限，将基于已有结果生成回答"})
	} else if result.ReachTokenBudget {
		// AR-03：达到 token 预算上限
		s.writeEvent(w, "warning", map[string]string{"message": "本次对话已达到 token 用量上限，将基于已有结果生成回答"})
	} else if result.BudgetExceeded {
		// AR-03：执行中余额不足
		s.writeEvent(w, "warning", map[string]string{"message": "弹丸余额不足，已基于已有结果生成回答，请充值后继续"})
	} else if result.ReachCostBudget {
		// AR-03：子任务成本超限（max_cost_per_task）
		s.writeEvent(w, "warning", map[string]string{"message": "子任务成本已达上限，将基于已有结果生成回答"})
	}

	// AR-02：客户端取消（断连）。连接已断，不再生成最终回答，仅持久化已产出的工具记录。
	// session 状态由 defer 置为 failed；前端通过 aborted 状态区分「完成」与「已取消」。
	if result.Cancelled {
		execErr = fmt.Errorf("用户取消执行")
		// writeEvent 在连接已断时会失败，忽略错误
		s.writeEvent(w, "cancelled", map[string]string{"session_id": session.ID})
		s.writeEvent(w, "done", map[string]string{"session_id": session.ID, "cancelled": "true"})
		return nil
	}

	// 如果 Function Calling 循环已经得到了最终回答（例如模型在工具后直接给出文本，
	// 或不需要工具的直连场景），直接下发该回答，避免再调一次流式接口导致空回复或长时间等待。
	if result.FinalContent != "" {
		s.writeEvent(w, "final_answer", map[string]string{"delta": result.FinalContent})
		s.writeToolSummaryEvent(w, result.Records)
		s.saveAgentAssistantMessage(ctx, conv.ID, userID, session.ID, result.Records, result.FinalContent)
		// Agent Team P2：组内对话执行成功后异步提取组共享记忆（失败仅记日志）
		s.extractTeamMemoryAsync(conv, userID, modelName, llmClient, req.Message, result.FinalContent)
		s.writeEvent(w, "done", map[string]interface{}{"session_id": session.ID, "usage": s.buildUsagePayload(totalUsage, stepCount, billingProvider, billingModel)})
		return nil
	}

	// 循环未直接得到回答时，再通过流式接口生成最终回答
	finalReq := llm.ChatRequest{
		Model:    modelName,
		Messages: result.Messages,
		Stream:   true,
	}
	stream, err := llmClient.ChatStream(ctx, finalReq)
	if err != nil {
		execErr = err
		s.writeEvent(w, "error", map[string]string{"message": err.Error()})
		s.writeEvent(w, "done", nil)
		return nil
	}

	emitted := false
	finalAnswer := ""
	for chunk := range stream {
		if chunk.Delta != "" {
			emitted = true
			finalAnswer += chunk.Delta
			s.writeEvent(w, "final_answer", map[string]string{"delta": chunk.Delta})
		}
		if chunk.ReasoningContent != "" {
			s.writeEvent(w, "reasoning", map[string]string{"delta": chunk.ReasoningContent})
		}
		if chunk.Usage != nil {
			totalUsage = addUsage(totalUsage, chunk.Usage)
		}
	}
	if !emitted {
		s.writeEvent(w, "warning", map[string]string{"message": "模型未返回有效回答"})
	}

	s.writeToolSummaryEvent(w, result.Records)
	s.saveAgentAssistantMessage(ctx, conv.ID, userID, session.ID, result.Records, finalAnswer)
	// Agent Team P2：组内对话执行成功后异步提取组共享记忆（失败仅记日志）
	s.extractTeamMemoryAsync(conv, userID, modelName, llmClient, req.Message, finalAnswer)
	s.writeEvent(w, "done", map[string]interface{}{"session_id": session.ID, "usage": s.buildUsagePayload(totalUsage, stepCount, billingProvider, billingModel)})
	return nil
}

// createSession 创建 Agent Session
func (s *AgentService) createSession(ctx context.Context, userID, conversationID, message, cwd string) (*model.AgentSession, error) {
	return s.createSessionWithParent(ctx, userID, conversationID, "", message, cwd)
}

// createSessionWithParent 创建 Agent Session；Agent Team P3：parentSessionID 非空时记录
// 子调用 provenance（编排者触发 CallAssistant 时子 session 关联父 session）
func (s *AgentService) createSessionWithParent(ctx context.Context, userID, conversationID, parentSessionID, message, cwd string) (*model.AgentSession, error) {
	if err := s.ensureSessionQuota(ctx, userID); err != nil {
		return nil, err
	}

	id := generateID("as")
	sessionDir, err := s.sandbox.SessionDir(userID, id)
	if err != nil {
		return nil, err
	}

	title := truncateByRunes(message, 30)

	session := &model.AgentSession{
		ID:              id,
		UserID:          userID,
		ConversationID:  conversationID,
		ParentSessionID: parentSessionID,
		Title:           title,
		Status:          "running",
		Permissions:     "[]",
		DiskPath:        sessionDir,
		Cwd:             cwd, // AR-06：claw 工作目录（子 session 继承父 cwd）
		CreatedAt:       time.Now().Unix(),
		UpdatedAt:       time.Now().Unix(),
	}
	if err := s.sessionRepo.Create(session); err != nil {
		return nil, err
	}
	return session, nil
}

// truncateByRunes 按 Unicode 字符截断字符串，避免截断多字节 UTF-8 字符导致乱码
func truncateByRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// ensureSessionQuota 确保 Session 数量未超限
func (s *AgentService) ensureSessionQuota(ctx context.Context, userID string) error {
	limit, err := s.vipService.GetMaxAgentSessions(userID)
	if err != nil {
		return err
	}
	count, err := s.sessionRepo.CountByUser(userID)
	if err != nil {
		return err
	}
	if count >= int64(limit) {
		oldest, err := s.sessionRepo.FindOldest(userID)
		if err != nil {
			return err
		}
		return s.sessionRepo.Delete(oldest.ID)
	}
	return nil
}

// buildInitialMessages 构建初始消息列表。
// Agent Team P2：teamID 非空且 teamMemorySvc 已装配时，检索组共享记忆并把
// 「组共享记忆」区块拼入 system 消息内容尾部（不新增消息，避免弱模型角色混乱）；
// userID/teamID 传空则跳过注入（如工具关闭的普通对话路径）。
func (s *AgentService) buildInitialMessages(ctx context.Context, req AgentExecuteRequest, attachments []AgentAttachment, userID, teamID, resolvedCwd string, toolsEnabled bool, supportsVision bool, permissionMode model.PermissionMode) []llm.Message {
	messages := make([]llm.Message, 0, len(req.History)+2)
	systemContent := "你是一个有用的 AI 助手。\n" +
		"规则：\n" +
		"1. 当用户问题涉及实时信息、搜索网络、读取文件、处理图片/OCR 或生成视频时，你必须调用对应工具获取结果，禁止只回复“我要查询/请稍等”而不调用工具。\n" +
		"2. 请直接输出工具调用，拿到工具结果后再给出最终回答。\n" +
		"3. 如果工具返回失败或没有有效结果，如实告知用户，不要编造。\n" +
		"4. 必须使用工具列表中的准确工具名（区分大小写），不得自创或变形；若返回未知工具，按其列出的可用工具名重新调用。\n" +
		"5. 调用工具前，先查看可用工具列表及其描述，确认工具能力与用户需求匹配后再调用；不确定时查看工具描述而非猜测。\n"
	if toolsEnabled {
		// AR-26：优先 function calling；不支持时引导模型用内嵌标记调 FunctionGet 主动拉取工具列表，
		// 拿到后再用内嵌标记调具体工具。不预设工具列表（按需拉取，对应 assistant 当下工具能力）。
		systemContent += "6. 优先使用结构化工具调用（function calling / tool_calls）调用工具。若你的环境不支持 function calling，可发送内联标记 <|FunctionCallBegin|>[{\"name\":\"FunctionGet\",\"parameters\":{}}]<|FunctionCallEnd|> 获取可用工具列表及用法，拿到后用同样的内联标记 <|FunctionCallBegin|>[{\"name\":\"工具名\",\"parameters\":{...}}]<|FunctionCallEnd|> 调用具体工具。禁止用 [工具名]参数[/工具名] 方括号标签或裸 JSON 形式调用工具。"
	} else {
		systemContent += "6. 必须使用结构化工具调用（function calling / tool_calls）调用工具，禁止在回复正文里用 [工具名]参数[/工具名] 等文本标签或 {\"name\":\"...\",\"parameters\":{...}} 形式的 JSON 文本来描述或发起工具调用；工具参数以 JSON 对象经 tool_calls 字段提供。"
	}
	// Agent Team P2：组共享记忆注入（区块预算 4000 字符 ≈ 2000 tokens，超出按相关度截断）
	if teamID != "" && s.teamMemorySvc != nil {
		memories := s.teamMemorySvc.SearchForInjection(ctx, userID, teamID, req.Message, 8)
		if block := s.teamMemorySvc.FormatInjectionBlock(memories, TeamMemoryInjectMaxChars); block != "" {
			systemContent += "\n\n" + block
		}
	}
	// AR-06：claw 工作目录注入 system prompt（resolvedCwd 非空即 claw 启用且有有效 cwd）。
	// 文件工具 path 基于此目录解析；LLM 需知晓工作目录才能正确传 path，避免漏传或瞎传。
	if resolvedCwd != "" {
		systemContent += "\n\n当前工作目录：" + resolvedCwd +
			"\n文件工具（ReadFile/WriteFile/StrReplaceFile/Grep/OCR）的 path 参数请使用相对于此目录的路径，也可使用绝对路径。"
	}
	// C3 plan 模式：指示只读研究 + 调 ExitPlanMode 提交 plan，禁止直接改文件（写工具已被权限闸拦截，
	// 此处再次明示避免模型反复尝试写操作浪费轮次）。
	if permissionMode == model.PermissionModePlan {
		systemContent += "\n\n【当前为 plan（计划）模式】你只能使用只读工具（ReadFile/Grep/FetchURL/OCR/Shell 只读命令等）进行调研，禁止直接修改文件或执行写操作。完成调研后，必须调用 ExitPlanMode 工具提交结构化计划（含步骤、涉及文件/命令、风险、验收标准）供用户审批。用户接受后会话切到 acceptEdits 模式再执行；用户要求细化时按反馈修订后重新提交。"
	}
	messages = append(messages, llm.Message{
		Role:    "system",
		Content: systemContent,
	})
	if len(req.History) > 0 {
		messages = append(messages, req.History...)
	}

	content := req.Message
	if len(attachments) > 0 {
		// C7：附件构建为 OpenAI 兼容 content parts（[]interface{} map），兼容 OpenAI/Anthropic/Gemini 客户端
		parts := s.buildAttachmentContentParts(ctx, attachments, supportsVision)
		if len(parts) > 0 {
			if text := strings.TrimSpace(content); text != "" {
				parts = append(parts, map[string]interface{}{"type": "text", "text": content})
			}
			messages = append(messages, llm.Message{Role: "user", Content: parts})
			return messages
		}
	}
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: content,
	})
	return messages
}

// supportsVision 判断当前 provider/model 是否支持图片理解。
// 复用 chat_proxy 的 modelSupportsVision 命名规则兜底 + Ele Agent 管理员配置（supports_vision）。
// 未知自定义模型保守返回 false，触发图片 OCR 降级而非 image_url（避免非视觉模型空响应）。
func (s *AgentService) supportsVision(provider, modelName string) bool {
	if llm.Provider(provider) == llm.ProviderEleAgent {
		subProvider, subModel, err := parseEleAgentModel(modelName)
		if err != nil || s.eleAgentModelService == nil {
			return false
		}
		return s.eleAgentModelService.GetModelCapability(subProvider, subModel)
	}
	supported, known := modelSupportsVision(provider, modelName)
	if known {
		return supported
	}
	return false
}

// buildAttachmentContentParts C7：把附件构建为 OpenAI 兼容 content parts。
// - image：视觉模型 -> image_url（data URI）；非视觉模型 -> OCR 降级为文本 part（复用 OCRDataURI/tesseract）；
//   OCR 不可用时降级为占位说明，避免图片被静默丢弃。
// - file：文本内容直接拼为 text part（带文件名前缀）；二进制文件（无 text）给占位说明。
// 附件在前、用户文本在后（OpenAI/Kimi 多模态惯例），用户文本由调用方追加。
func (s *AgentService) buildAttachmentContentParts(ctx context.Context, attachments []AgentAttachment, supportsVision bool) []interface{} {
	var parts []interface{}
	for _, att := range attachments {
		switch att.Type {
		case "image":
			if supportsVision && att.DataURL != "" {
				parts = append(parts, map[string]interface{}{
					"type":      "image_url",
					"image_url": map[string]interface{}{"url": att.DataURL},
				})
			} else if att.DataURL != "" {
				text, err := s.registry.OCRDataURI(ctx, att.DataURL)
				if err == nil && strings.TrimSpace(text) != "" {
					parts = append(parts, map[string]interface{}{
						"type": "text",
						"text": fmt.Sprintf("【图片：%s】\n%s", att.Name, text),
					})
				} else {
					parts = append(parts, map[string]interface{}{
						"type": "text",
						"text": fmt.Sprintf("【图片：%s（当前模型不支持图片理解，且 OCR 不可用，请切换视觉模型）】", att.Name),
					})
				}
			}
		case "file":
			if strings.TrimSpace(att.Text) != "" {
				parts = append(parts, map[string]interface{}{
					"type": "text",
					"text": fmt.Sprintf("【文件：%s】\n%s", att.Name, att.Text),
				})
			} else if att.DataURL != "" {
				parts = append(parts, map[string]interface{}{
					"type": "text",
					"text": fmt.Sprintf("【文件：%s（二进制文件，当前模型无法直接解析，可用对应工具处理）】", att.Name),
				})
			}
		}
	}
	return parts
}

// resolveClient 根据请求中的模型配置解析 LLM 客户端
func (s *AgentService) resolveClient(ctx context.Context, req AgentExecuteRequest, modelName string) (AgentLLMClient, error) {
	if s.clientResolver == nil {
		return nil, fmt.Errorf("Agent LLM 客户端未初始化，请配置 API Key")
	}
	return s.clientResolver(ctx, req.Provider, modelName, req.BaseURL, req.APIKey)
}

// extractTeamMemoryAsync Agent Team P2：执行成功后异步提取组共享记忆。
// 仅当对话归属分组（conv.TeamID 非空）且 teamMemorySvc 已装配时触发；
// goroutine 内使用 context.Background()+WithTimeout(60s)——请求 ctx 随 SSE 结束即取消，
// 不能复用；复用本次执行的 llmClient 与 modelName；失败仅记日志，不影响主流程。
func (s *AgentService) extractTeamMemoryAsync(conv *model.ChatConversation, userID, modelName string, client AgentLLMClient, userMessage, finalAnswer string) {
	if s.teamMemorySvc == nil || conv == nil || conv.TeamID == "" {
		return
	}
	teamID, conversationID := conv.TeamID, conv.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := s.teamMemorySvc.ExtractAndStore(ctx, client, modelName, teamID, userID, conversationID, userMessage, finalAnswer); err != nil && s.logger != nil {
			s.logger.Warn("组共享记忆提取失败", zap.String("team_id", teamID), zap.String("conversation_id", conversationID), zap.Error(err))
		}
	}()
}

// chatStream 普通对话流，返回累计的 token 用量用于计费
func (s *AgentService) chatStream(ctx context.Context, req AgentExecuteRequest, w io.Writer) (*llm.Usage, error) {
	modelName := s.model
	if req.Model != "" {
		modelName = req.Model
	}

	llmClient, err := s.resolveClient(ctx, req, modelName)
	if err != nil {
		s.writeEvent(w, "error", map[string]string{"message": err.Error()})
		s.writeEvent(w, "done", nil)
		return nil, nil
	}

	// Ele Agent 模型名为 subProvider/subModel，上游真实请求只使用 subModel
	modelName = normalizeAgentModelName(req.Provider, modelName)

	// 工具关闭的普通对话路径不注入组共享记忆（userID/teamID 传空跳过；保持与历史行为一致）
	messages := s.buildInitialMessages(ctx, req, req.Attachments, "", "", "", false, s.supportsVision(req.Provider, modelName), model.PermissionModeDefault)
	stream, err := llmClient.ChatStream(ctx, llm.ChatRequest{
		Model:    modelName,
		Messages: messages,
		Stream:   true,
	})
	if err != nil {
		s.writeEvent(w, "error", map[string]string{"message": err.Error()})
		s.writeEvent(w, "done", nil)
		return nil, nil
	}

	var usage *llm.Usage
	finalAnswer := ""
	for chunk := range stream {
		if chunk.Delta != "" {
			finalAnswer += chunk.Delta
			s.writeEvent(w, "final_answer", map[string]string{"delta": chunk.Delta})
		}
		if chunk.ReasoningContent != "" {
			s.writeEvent(w, "reasoning", map[string]string{"delta": chunk.ReasoningContent})
		}
		if chunk.Usage != nil {
			usage = addUsage(usage, chunk.Usage)
		}
	}

	// 普通对话也保存 assistant 回复到 ChatMessage，统一历史数据源
	s.saveAssistantMessage(ctx, req.ConversationID, finalAnswer)

	s.writeEvent(w, "done", nil)
	return usage, nil
}

// saveAssistantMessage 保存普通 assistant 回复到 ChatMessage 表
func (s *AgentService) saveAssistantMessage(ctx context.Context, conversationID, answer string) {
	userID, ok := ctx.Value("user_id").(string)
	if !ok || userID == "" || conversationID == "" {
		return
	}
	if answer == "" {
		answer = "（无回答）"
	}
	msg := &model.ChatMessage{
		ID:              generateID("msg"),
		Role:            "assistant",
		Content:         answer,
		ClientMessageID: "",
		CreatedAt:       time.Now().Unix(),
	}
	if _, err := s.conversationSvc.SaveMessage(ctx, conversationID, userID, msg); err != nil && s.logger != nil {
		s.logger.Warn("保存普通 assistant 消息失败", zap.String("conversation_id", conversationID), zap.Error(err))
	}
}

// normalizeAgentModelName 将 Ele Agent 内部模型名 subProvider/subModel 转换为上游真实模型名 subModel
func normalizeAgentModelName(provider, modelName string) string {
	if llm.Provider(provider) == llm.ProviderEleAgent {
		if idx := strings.Index(modelName, "/"); idx >= 0 {
			return modelName[idx+1:]
		}
	}
	return modelName
}

// billingProviderAndModel 返回计费所需的 provider 与 model 标识。
// 前端未传 provider 时默认按 Ele Agent 计费；BYOK 模型由 BillingService 自行判定为免费。
func (s *AgentService) billingProviderAndModel(req AgentExecuteRequest) (string, string) {
	provider := req.Provider
	if provider == "" {
		provider = "eleagent"
	}
	modelName := req.Model
	if modelName == "" {
		modelName = s.model
	}
	return provider, modelName
}

// writeEvent 写入 SSE 事件
func (s *AgentService) writeEvent(w io.Writer, event string, data interface{}) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(b))
	if flusher, ok := w.(interface{ Flush() }); ok {
		flusher.Flush()
	}
}

// DeliverApproval C1 投递工具审批决策（/agent/approve 调用，跨 HTTP 请求解锁阻塞的工具循环）。
// 命中待审批请求则解锁，返回 true；未命中（已超时/已决策/不存在）返回 false（幂等）。
func (s *AgentService) DeliverApproval(sessionID, toolCallID string, dec ApprovalDecision) bool {
	key := sessionID + ":" + toolCallID
	return s.approvals.deliver(key, dec)
}

// ListPermissionRules C1 返回用户已持久化的权限规则（供 /claw-console 展示与管理）。
func (s *AgentService) ListPermissionRules() []model.PermissionRule {
	if s.permissionSvc == nil {
		return nil
	}
	return s.permissionSvc.ListRules()
}

// AddPermissionRule C1 新增一条权限规则（allow/deny/ask）。
func (s *AgentService) AddPermissionRule(spec string, decision model.PermissionDecision) {
	if s.permissionSvc != nil {
		s.permissionSvc.AddAlwaysAllow(spec, decision)
	}
}

// RemovePermissionRule C1 按 spec 删除一条权限规则。
func (s *AgentService) RemovePermissionRule(spec string) {
	if s.permissionSvc != nil {
		s.permissionSvc.RemoveRule(spec)
	}
}

// buildUsagePayload AR-07：构建 SSE done 事件 usage 负载（tokens/cost/步数/上下文规模）。
// claw 无 billing 时省略 cost 字段（前端裁剪成本展示）；prompt_tokens 反映最近上下文规模供 contextUsage 展示。
func (s *AgentService) buildUsagePayload(totalUsage *llm.Usage, stepCount int, billingProvider, billingModel string) map[string]interface{} {
	payload := map[string]interface{}{
		"step_count": stepCount,
	}
	if totalUsage != nil {
		total := totalUsage.TotalTokens
		if total == 0 {
			total = totalUsage.PromptTokens + totalUsage.CompletionTokens
		}
		payload["total_tokens"] = total
		payload["prompt_tokens"] = totalUsage.PromptTokens
		payload["completion_tokens"] = totalUsage.CompletionTokens
		if s.billingService != nil {
			payload["cost_amount"] = s.billingService.EstimateCost(billingProvider, billingModel, CurrencyDanwan, totalUsage)
			payload["currency"] = CurrencyDanwan
		}
	}
	return payload
}

// writeToolSummaryEvent 下发工具摘要事件，供前端拼入 assistant content 后持久化
func (s *AgentService) writeToolSummaryEvent(w io.Writer, records []ToolCallRecord) {
	toolSummary := buildToolSummary(records)
	if toolSummary != "" {
		s.writeEvent(w, "tool_summary", map[string]string{"content": toolSummary})
	}
}

// saveAgentAssistantMessage 将 Agent 最终回答与工具摘要持久化到 ChatMessage 表。
// 工具摘要拼接在正文前作为 LLM 上下文，并以 JSON 形式存入 ToolResults 供前端展示。
func (s *AgentService) saveAgentAssistantMessage(ctx context.Context, conversationID, userID, sessionID string, records []ToolCallRecord, answer string) {
	if answer == "" {
		answer = "（无回答）"
	}
	content := answer
	toolSummary := buildToolSummary(records)
	if toolSummary != "" {
		content = toolSummary + "\n\n" + answer
	}

	toolResultsJSON := ""
	if toolSummary != "" {
		if b, err := json.Marshal(map[string]string{"summary": toolSummary}); err == nil {
			toolResultsJSON = string(b)
		}
	}

	msg := &model.ChatMessage{
		ID:              generateID("msg"),
		Role:            "assistant",
		Content:         content,
		ToolResults:     toolResultsJSON,
		ClientMessageID: "agent_assistant_" + sessionID,
		CreatedAt:       time.Now().Unix(),
	}
	if _, err := s.conversationSvc.SaveMessage(ctx, conversationID, userID, msg); err != nil && s.logger != nil {
		s.logger.Warn("保存 Agent assistant 消息失败", zap.String("conversation_id", conversationID), zap.String("session_id", sessionID), zap.Error(err))
	}
}

// AgentSessionItem Session 列表项（脱敏，不暴露磁盘路径）
type AgentSessionItem struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id,omitempty"`
	// Agent Team P3：子调用 provenance
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// AR-12：会话分叉 provenance
	ParentEntryID       string `json:"parent_entry_id,omitempty"`
	ForkedFromSessionID string `json:"forked_from_session_id,omitempty"`
	Title               string `json:"title"`
	Status              string `json:"status"`
	ToolChain           string `json:"tool_chain,omitempty"`
	// AR-07：用量统计（供前端用量可见性状态条展示）
	TotalTokens int64  `json:"total_tokens,omitempty"`
	StepCount   int    `json:"step_count,omitempty"`
	CostAmount  int64  `json:"cost_amount,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	CompletedAt *int64 `json:"completed_at,omitempty"`
}

// ListSessions 查询用户的 Agent Session 列表
func (s *AgentService) ListSessions(ctx context.Context, userID string, page, pageSize int) ([]AgentSessionItem, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	sessions, total, err := s.sessionRepo.ListByUser(userID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	items := make([]AgentSessionItem, 0, len(sessions))
	for _, sess := range sessions {
		items = append(items, AgentSessionItem{
			ID:                  sess.ID,
			ConversationID:      sess.ConversationID,
			ParentSessionID:     sess.ParentSessionID,
			ParentEntryID:       sess.ParentEntryID,
			ForkedFromSessionID: sess.ForkedFromSessionID,
			Title:               sess.Title,
			Status:              sess.Status,
			ToolChain:           sess.ToolChain,
			TotalTokens:         sess.TotalTokens,
			StepCount:           sess.StepCount,
			CostAmount:          sess.CostAmount,
			CreatedAt:           sess.CreatedAt,
			UpdatedAt:           sess.UpdatedAt,
			CompletedAt:         sess.CompletedAt,
		})
	}
	return items, total, nil
}

// GetSession 查询 Session 详情（仅允许所有者访问）
func (s *AgentService) GetSession(ctx context.Context, id, userID string) (*AgentSessionItem, error) {
	sess, err := s.sessionRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if sess.UserID != userID {
		return nil, fmt.Errorf("无权访问该 Session")
	}
	return &AgentSessionItem{
		ID:                  sess.ID,
		ConversationID:      sess.ConversationID,
		ParentSessionID:     sess.ParentSessionID,
		ParentEntryID:       sess.ParentEntryID,
		ForkedFromSessionID: sess.ForkedFromSessionID,
		Title:               sess.Title,
		Status:              sess.Status,
		ToolChain:           sess.ToolChain,
		TotalTokens:         sess.TotalTokens,
		StepCount:           sess.StepCount,
		CostAmount:          sess.CostAmount,
		CreatedAt:           sess.CreatedAt,
		UpdatedAt:           sess.UpdatedAt,
		CompletedAt:         sess.CompletedAt,
	}, nil
}

// ForkSession 会话分叉（AR-12）：从父 session 的分叉点消息 entryID 处复制对话历史到新 session，
// 继承父 cwd/project_root/permissions，记录 parent_entry_id / forked_from_session_id。
// 返回新 session（关联新对话），供前端切换到分叉对话继续探索。
func (s *AgentService) ForkSession(ctx context.Context, id, userID, entryID string) (*AgentSessionItem, error) {
	parent, err := s.sessionRepo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("Session 不存在")
	}
	if parent.UserID != userID {
		return nil, fmt.Errorf("无权访问该 Session")
	}
	if parent.ConversationID == "" {
		return nil, fmt.Errorf("该 Session 无关联对话，无法分叉")
	}
	if entryID == "" {
		return nil, fmt.Errorf("缺少分叉点 entry_id")
	}

	// 复制父对话到分叉点为止的消息历史到新对话
	conv, err := s.conversationSvc.ForkConversation(ctx, userID, parent.ConversationID, entryID)
	if err != nil {
		return nil, err
	}

	if err := s.ensureSessionQuota(ctx, userID); err != nil {
		return nil, err
	}

	newID := generateID("as")
	sessionDir, err := s.sandbox.SessionDir(userID, newID)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	session := &model.AgentSession{
		ID:                  newID,
		UserID:              userID,
		ConversationID:      conv.ID,
		ParentEntryID:       entryID,
		ForkedFromSessionID: parent.ID,
		Title:               parent.Title,
		Status:              "succeeded",
		Permissions:         parent.Permissions,
		ToolChain:           "[]",
		DiskPath:            sessionDir,
		Cwd:                 parent.Cwd,
		ProjectRoot:         parent.ProjectRoot,
		CreatedAt:           now,
		UpdatedAt:           now,
		CompletedAt:         &now,
	}
	if err := s.sessionRepo.Create(session); err != nil {
		return nil, err
	}

	return &AgentSessionItem{
		ID:                  session.ID,
		ConversationID:      session.ConversationID,
		ParentEntryID:       session.ParentEntryID,
		ForkedFromSessionID: session.ForkedFromSessionID,
		Title:               session.Title,
		Status:              session.Status,
		CreatedAt:           session.CreatedAt,
		UpdatedAt:           session.UpdatedAt,
		CompletedAt:         session.CompletedAt,
	}, nil
}

// GetSessionAudit 读取会话统一审计视图（AR-08）。
// 解析 Session.ToolChain（JSON 持久化的 []ToolCallRecord）并读 metadata.json 写审计，
// 返回 SessionAudit 供 claw-console/admin 展示。仅校验所有权，不泄露跨用户数据。
func (s *AgentService) GetSessionAudit(ctx context.Context, id, userID string) (SessionAudit, error) {
	sess, err := s.sessionRepo.GetByID(id)
	if err != nil {
		return SessionAudit{}, err
	}
	if sess.UserID != userID {
		return SessionAudit{}, fmt.Errorf("无权访问该 Session")
	}
	var records []ToolCallRecord
	if sess.ToolChain != "" {
		_ = json.Unmarshal([]byte(sess.ToolChain), &records)
	}
	return s.sandbox.ReadSessionAudit(userID, id, records)
}

// updateSessionStatus 更新 Session 状态、完成时间与用量统计（AR-07 持久化 totalTokens/stepCount/costAmount）
func (s *AgentService) updateSessionStatus(sessionID, status, toolChainJSON string, totalTokens int64, stepCount int, costAmount int64) error {
	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		return err
	}
	session.Status = status
	if toolChainJSON != "" {
		session.ToolChain = toolChainJSON
	}
	// AR-07：用量统计持久化（每次执行覆盖，反映最新一次执行的用量）
	session.TotalTokens = totalTokens
	session.StepCount = stepCount
	session.CostAmount = costAmount
	now := time.Now().Unix()
	session.UpdatedAt = now
	if status == "succeeded" || status == "failed" {
		session.CompletedAt = &now
	}
	return s.sessionRepo.Update(session)
}

// deleteSessionResources 删除 Session 的磁盘目录与输出资源元数据
func (s *AgentService) deleteSessionResources(session *model.AgentSession) {
	if session.DiskPath != "" {
		_ = s.sandbox.RemoveSessionDir(session.DiskPath)
	}
	_ = s.sessionRepo.DeleteOutputsBySessionID(session.ID)
}

// DeleteSession 删除 Session 及其磁盘资源
func (s *AgentService) DeleteSession(ctx context.Context, id, userID string) error {
	sess, err := s.sessionRepo.GetByID(id)
	if err != nil {
		return err
	}
	if sess.UserID != userID {
		return fmt.Errorf("无权访问该 Session")
	}
	s.deleteSessionResources(sess)
	return s.sessionRepo.Delete(id)
}

// DeleteAllSessions 删除当前用户的所有 Agent Session 及其资源
func (s *AgentService) DeleteAllSessions(ctx context.Context, userID string) error {
	sessions, _, err := s.sessionRepo.ListByUser(userID, 1, 10000)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(sessions))
	for i := range sessions {
		s.deleteSessionResources(&sessions[i])
		ids = append(ids, sessions[i].ID)
	}
	return s.sessionRepo.DeleteByIDs(ids)
}

// DeleteSessionsByConversation 删除某个对话下的所有 Agent Session 及其资源
func (s *AgentService) DeleteSessionsByConversation(ctx context.Context, userID, conversationID string) error {
	sessions, err := s.sessionRepo.ListByConversation(userID, conversationID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(sessions))
	for i := range sessions {
		s.deleteSessionResources(&sessions[i])
		ids = append(ids, sessions[i].ID)
	}
	return s.sessionRepo.DeleteByIDs(ids)
}

// GetResource 根据匿名 resource_id 获取资源文件信息
func (s *AgentService) GetResource(ctx context.Context, resourceID string) (data []byte, mimeType, fileName string, err error) {
	output, err := s.sessionRepo.GetOutputByResourceID(resourceID)
	if err != nil {
		return nil, "", "", err
	}
	// AR-27：claw（unrestricted）下 WriteFile 把文件写到 cwd 内，DiskPath 是 cwd 下绝对路径。
	// 共享沙箱单例 projectRoot 为空会拒绝读取，导致下载返回 JSON 404（浏览器存成 ar-xxxxx.json）。
	// 按所属 session 的 cwd 克隆沙箱再读；取不到 session/cwd 时回退共享沙箱（云端 basePath 内）。
	reader := s.sandbox
	if s.unrestricted && output.SessionID != "" {
		if sess, serr := s.sessionRepo.GetByID(output.SessionID); serr == nil && sess != nil && sess.Cwd != "" {
			reader = s.sandbox.WithProjectRoot(sess.Cwd)
		}
	}
	data, err = reader.ReadFile(output.DiskPath)
	if err != nil {
		return nil, "", "", err
	}
	return data, output.MimeType, output.FileName, nil
}
