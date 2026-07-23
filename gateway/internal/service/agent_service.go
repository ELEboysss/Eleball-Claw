package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	clientResolver       AgentLLMClientResolver
	model                string
	maxSteps             int
	logger               *zap.Logger
	// unrestricted=true 时跳过 VIP 门控（Agent 模式/文件工具/试用次数），并容忍本地 user 未命中。
	// claw 本地不限 Agent 模式（云端账户统一后，本地无 users 行），置 true；云端 cmd/server 保持 false。
	unrestricted bool
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
	return &AgentService{
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
	}
}

// SetMaxRetries 设置 Agent 工具循环上游可重试错误的最大尝试次数（对应 llm.max_retries 配置）
func (s *AgentService) SetMaxRetries(n int) {
	if s.toolLoop != nil {
		s.toolLoop.SetMaxRetries(n)
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
	// 同时持久化到 conversation
	if req.EnableTools != nil || req.EnableWebSearch != nil || req.SearchProvider != nil {
		updateReq := UpdateConversationReq{
			EnableTools:     req.EnableTools,
			EnableWebSearch: req.EnableWebSearch,
			SearchProvider:  req.SearchProvider,
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
			if err := s.billingService.Deduct(userID, billingProvider, billingModel, CurrencyDanwan, totalUsage); err != nil && s.logger != nil {
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

	// 8. 创建 Agent Session
	session, err := s.createSession(ctx, userID, conv.ID, req.Message)
	if err != nil {
		s.writeEvent(w, "error", map[string]string{"message": err.Error()})
		s.writeEvent(w, "done", nil)
		return nil
	}

	// 执行结束后更新 Session 状态；执行成功为 succeeded，任何错误事件发出后为 failed
	execErr := error(nil)
	toolChainJSON := ""
	defer func() {
		status := "succeeded"
		if execErr != nil {
			status = "failed"
		}
		if err := s.updateSessionStatus(session.ID, status, toolChainJSON); err != nil && s.logger != nil {
			s.logger.Warn("更新 Agent Session 状态失败", zap.String("session_id", session.ID), zap.String("status", status), zap.Error(err))
		}
	}()

	// 9. 构建可用工具列表（根据联网开关决定是否暴露 SearchWeb / FetchURL）
	// 克隆注册表并注入用户购买的动态工具，实现集市 SKU 的动态加载
	var dynamicTools []*Tool
	if s.agentToolLoader != nil {
		loaded, loadErr := s.agentToolLoader.LoadToolsForUser(ctx, userID)
		if loadErr != nil && s.logger != nil {
			s.logger.Warn("加载用户动态工具失败", zap.String("user_id", userID), zap.Error(loadErr))
		}
		dynamicTools = loaded
	}
	registry := s.registry.Clone()
	for _, t := range dynamicTools {
		registry.Register(t)
	}
	availableTools := s.schemaBuilder.BuildWithOptionsAndDynamic(hasFileTools, enableWebSearch, dynamicTools)

	// 10. 构建初始消息
	messages := s.buildInitialMessages(req, preprocessed)

	// 11. Function Calling 循环
	env := &ToolEnv{
		UserID:         userID,
		ConversationID: conv.ID,
		SessionID:      session.ID,
		Sandbox:        s.sandbox,
		SessionRepo:    s.sessionRepo,
		SearchProvider: searchProvider,
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

	// 11. 输出最终回答
	if result.LoopDetected {
		// 检测到同工具同参数循环调用，强制进入最终回答
		s.writeEvent(w, "warning", map[string]string{"message": "检测到工具循环调用，将基于已有结果生成回答"})
	} else if result.ReachMaxSteps {
		// 使用 warning 事件而非 error，避免前端把“已达上限”误判为失败并丢弃后续最终回答
		s.writeEvent(w, "warning", map[string]string{"message": "工具调用次数已达上限，将基于已有结果生成回答"})
	}

	// 如果 Function Calling 循环已经得到了最终回答（例如模型在工具后直接给出文本，
	// 或不需要工具的直连场景），直接下发该回答，避免再调一次流式接口导致空回复或长时间等待。
	if result.FinalContent != "" {
		s.writeEvent(w, "final_answer", map[string]string{"delta": result.FinalContent})
		s.writeToolSummaryEvent(w, result.Records)
		s.saveAgentAssistantMessage(ctx, conv.ID, userID, session.ID, result.Records, result.FinalContent)
		s.writeEvent(w, "done", map[string]string{"session_id": session.ID})
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
	s.writeEvent(w, "done", map[string]string{"session_id": session.ID})
	return nil
}

// createSession 创建 Agent Session
func (s *AgentService) createSession(ctx context.Context, userID, conversationID, message string) (*model.AgentSession, error) {
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
		ID:             id,
		UserID:         userID,
		ConversationID: conversationID,
		Title:          title,
		Status:         "running",
		Permissions:    "[]",
		DiskPath:       sessionDir,
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
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

// buildInitialMessages 构建初始消息列表
func (s *AgentService) buildInitialMessages(req AgentExecuteRequest, attachments []AgentAttachment) []llm.Message {
	messages := make([]llm.Message, 0, len(req.History)+2)
	messages = append(messages, llm.Message{
		Role: "system",
		Content: "你是一个有用的 AI 助手。\n" +
			"规则：\n" +
			"1. 当用户问题涉及实时信息、搜索网络、读取文件、处理图片/OCR 或生成视频时，你必须调用对应工具获取结果，禁止只回复“我要查询/请稍等”而不调用工具。\n" +
			"2. 请直接输出工具调用，拿到工具结果后再给出最终回答。\n" +
			"3. 如果工具返回失败或没有有效结果，如实告知用户，不要编造。",
	})
	if len(req.History) > 0 {
		messages = append(messages, req.History...)
	}

	content := req.Message
	if len(attachments) > 0 {
		// TODO: 将附件构建为 content parts
		content = content + "\n[附件信息待扩展]"
	}
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: content,
	})
	return messages
}

// resolveClient 根据请求中的模型配置解析 LLM 客户端
func (s *AgentService) resolveClient(ctx context.Context, req AgentExecuteRequest, modelName string) (AgentLLMClient, error) {
	if s.clientResolver == nil {
		return nil, fmt.Errorf("Agent LLM 客户端未初始化，请配置 API Key")
	}
	return s.clientResolver(ctx, req.Provider, modelName, req.BaseURL, req.APIKey)
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

	messages := s.buildInitialMessages(req, req.Attachments)
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
	ID             string  `json:"id"`
	ConversationID string  `json:"conversation_id,omitempty"`
	Title          string  `json:"title"`
	Status         string  `json:"status"`
	ToolChain      string  `json:"tool_chain,omitempty"`
	CreatedAt      int64   `json:"created_at"`
	UpdatedAt      int64   `json:"updated_at"`
	CompletedAt    *int64  `json:"completed_at,omitempty"`
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
			ID:             sess.ID,
			ConversationID: sess.ConversationID,
			Title:          sess.Title,
			Status:         sess.Status,
			ToolChain:      sess.ToolChain,
			CreatedAt:      sess.CreatedAt,
			UpdatedAt:      sess.UpdatedAt,
			CompletedAt:    sess.CompletedAt,
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
		ID:             sess.ID,
		ConversationID: sess.ConversationID,
		Title:          sess.Title,
		Status:         sess.Status,
		ToolChain:      sess.ToolChain,
		CreatedAt:      sess.CreatedAt,
		UpdatedAt:      sess.UpdatedAt,
		CompletedAt:    sess.CompletedAt,
	}, nil
}

// updateSessionStatus 更新 Session 状态与完成时间
func (s *AgentService) updateSessionStatus(sessionID, status, toolChainJSON string) error {
	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		return err
	}
	session.Status = status
	if toolChainJSON != "" {
		session.ToolChain = toolChainJSON
	}
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
	data, err = s.sandbox.ReadFile(output.DiskPath)
	if err != nil {
		return nil, "", "", err
	}
	return data, output.MimeType, output.FileName, nil
}
