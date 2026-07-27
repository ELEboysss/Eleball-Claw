package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/pkg/llm"
	"go.uber.org/zap"
)

// Agent Team P3：跨 Agent 编排协作（设计基准 docs/agent-team-collaboration-plan.md §4.2/§4.3/§9）。
// 编排者（当前对话绑定的助手/主 agent）通过 CallAssistant 工具把子任务委派给
// 能力目录内的协作助手，以独立子 ToolCallingLoop 执行，限深 1 层。

const (
	// capabilityCatalogMaxSize 能力目录上限（文档 §4.2）
	capabilityCatalogMaxSize = 20
	// callAssistantMaxDelegates 每次 execute 委派次数上限（文档 §4.3）
	callAssistantMaxDelegates = 5
	// callAssistantTimeout 子调用超时（文档 §4.3）
	callAssistantTimeout = 5 * time.Minute
	// callAssistantSummaryMaxRunes 返回给编排者的结果摘要截断长度（文档 §4.3）
	callAssistantSummaryMaxRunes = 4000
)

// AssistantCapability 协作助手能力目录项（Agent Team P3）
type AssistantCapability struct {
	ID          string
	Name        string
	Description string
	Skills      []string // 技能摘要：assistant_items → AgentItem.Name 拼接
}

// BuildCapabilityCatalog 构建当前用户可委派的协作助手目录（Agent Team P3，文档 §4.2）：
// shared=true 且（team_id 为空=全局可见 或 team_id == 当前对话组）的助手，
// 排除当前对话自身绑定的助手（它是编排者自己），上限 20 个。
// ctx 预留给后续检索实现（如 embedding 排序），当前未使用。
func (s *AssistantService) BuildCapabilityCatalog(ctx context.Context, userID, currentAssistantID, teamID string) []AssistantCapability {
	_ = ctx
	assistants, err := s.repo.ListByUser(userID)
	if err != nil {
		return nil
	}
	catalog := make([]AssistantCapability, 0, len(assistants))
	for _, a := range assistants {
		if !a.Shared {
			continue
		}
		if a.ID == currentAssistantID {
			continue
		}
		if a.TeamID != "" && a.TeamID != teamID {
			continue
		}
		skills := make([]string, 0)
		for _, item := range s.expandItems(a.ID) {
			if item.Name != "" {
				skills = append(skills, item.Name)
			}
		}
		catalog = append(catalog, AssistantCapability{
			ID:          a.ID,
			Name:        a.Name,
			Description: a.Description,
			Skills:      skills,
		})
		if len(catalog) >= capabilityCatalogMaxSize {
			break
		}
	}
	return catalog
}

// FormatCatalogBlock 生成可委派助手清单（每行：ID / 名称 / 描述 / 技能），嵌入 CallAssistant 工具
// description 供 LLM 决策；不再注入 system prompt，避免每轮把「可委派」推到 LLM 眼前而挤占既有工具。
func FormatCatalogBlock(catalog []AssistantCapability) string {
	if len(catalog) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range catalog {
		skills := "无"
		if len(c.Skills) > 0 {
			skills = strings.Join(c.Skills, "、")
		}
		fmt.Fprintf(&b, "\n- %s: %s — %s（技能：%s）", c.ID, c.Name, c.Description, skills)
	}
	return b.String()
}

// callAssistantDescription 构造 CallAssistant 工具描述：明确「仅当子任务明显更适合交给专门助手时才用」，
// 常规搜索/抓取/文件/问答请直接用既有工具或直接回答；附可委派助手清单，与 SearchWeb/FetchURL 平级可选。
func callAssistantDescription(catalog []AssistantCapability) string {
	desc := "将子任务委派给协作助手执行，返回其结果摘要。仅当某子任务明显更适合交给专门助手时才调用；常规搜索、抓取、文件处理、问答请直接使用 SearchWeb、FetchURL 等既有工具或直接回答，多数情况下无需委派。assistant_id 与 task 均必填、不能为空。"
	if list := FormatCatalogBlock(catalog); list != "" {
		desc += "可委派助手（assistant_id 取下列之一）：" + list
	}
	return desc
}

// formatCatalogIDsForError 错误提示中列出可委派助手 ID 与名称，便于 LLM 在空参/误调后自我纠正重试
func formatCatalogIDsForError(catalog []AssistantCapability) string {
	if len(catalog) == 0 {
		return "（当前无可委派助手）"
	}
	parts := make([]string, 0, len(catalog))
	for _, c := range catalog {
		parts = append(parts, fmt.Sprintf("%s(%s)", c.ID, c.Name))
	}
	return strings.Join(parts, ", ")
}

// skillItemsFor 展开助手条目的秘技完整记录（Agent Team P3：构建子 agent system prompt 用）
func (s *AssistantService) skillItemsFor(assistantID string) []*model.AgentItem {
	agentIDs, err := s.repo.ListAgentIDs(assistantID)
	if err != nil || len(agentIDs) == 0 {
		return nil
	}
	items := make([]*model.AgentItem, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		if agent, err := s.agentRepo.GetByID(agentID); err == nil && agent != nil {
			items = append(items, agent)
		}
	}
	return items
}

// callAssistantRuntime 编排执行期依赖：Execute 在解析 LLM 客户端后填充，
// CallAssistant 闭包在实际被工具循环调用时读取（工具装配早于客户端解析）。
type callAssistantRuntime struct {
	client          AgentLLMClient
	model           string
	writer          io.Writer // Agent Team P5：子任务进度流式下发（= Execute 的 SSE writer）
	billingProvider string    // Agent Team P5：主对话计费口径（follow 模式复用）
	billingModel    string    //   未归一化的 subProvider/subModel（供 P5-3 逐次计费）
}

// resolveSubClient 按助手 LLM 配置解析子 agent 客户端（Agent Team P5）：
// follow/空 -> 回落主对话 client/model；eleagent -> 复用服务端凭据解析指定 Ele Agent 模型；
// byok -> 解密 api_key 后按 provider/model/base_url 直连。
// 返回 (client, model, billingProvider, billingModel)；billing 口径供 P5-3 逐次计费用。
func (s *AgentService) resolveSubClient(ctx context.Context, a *model.Assistant, rt *callAssistantRuntime) (AgentLLMClient, string, string, string, error) {
	mode := a.LLMMode
	if mode == "" {
		mode = "follow"
	}
	switch mode {
	case "follow":
		return rt.client, rt.model, rt.billingProvider, rt.billingModel, nil
	case "eleagent":
		if a.LLMModel == "" {
			return nil, "", "", "", errors.New("助手未配置 Ele Agent 模型")
		}
		subReq := AgentExecuteRequest{Provider: "eleagent", Model: a.LLMModel}
		c, err := s.resolveClient(ctx, subReq, a.LLMModel)
		if err != nil {
			return nil, "", "", "", err
		}
		return c, normalizeAgentModelName("eleagent", a.LLMModel), "eleagent", a.LLMModel, nil
	case "byok":
		if a.LLMProvider == "" || a.LLMModel == "" || a.LLMBaseURL == "" {
			return nil, "", "", "", errors.New("助手 BYOK 配置不完整（需 provider/model/base_url）")
		}
		apiKey, err := s.assistantSvc.DecryptAPIKey(a)
		if err != nil {
			return nil, "", "", "", err
		}
		subReq := AgentExecuteRequest{Provider: a.LLMProvider, Model: a.LLMModel, BaseURL: a.LLMBaseURL, APIKey: apiKey}
		c, err := s.resolveClient(ctx, subReq, a.LLMModel)
		if err != nil {
			return nil, "", "", "", err
		}
		return c, a.LLMModel, a.LLMProvider, a.LLMModel, nil
	}
	return rt.client, rt.model, rt.billingProvider, rt.billingModel, nil
}

// buildCallAssistantTool 构造 CallAssistant 编排工具（Agent Team P3，文档 §4.3）。
// 目录非空时由 Execute 加入 dynamicTools（随之进 schema 与注册表）。
func (s *AgentService) buildCallAssistantTool(catalog []AssistantCapability, parentSession *model.AgentSession, rt *callAssistantRuntime) *Tool {
	ids := make([]string, 0, len(catalog))
	for _, c := range catalog {
		ids = append(ids, c.ID)
	}
	return &Tool{
		Name:        "CallAssistant",
		Description: callAssistantDescription(catalog),
		ServerSide:  false,
		Driver:      string(model.ToolDriverBuiltin),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"assistant_id": map[string]interface{}{
					"type":        "string",
					"enum":        ids,
					"description": "目标协作助手 ID（取自本工具说明中列出的可委派助手，不能为空）",
				},
				"task": map[string]interface{}{
					"type":        "string",
					"description": "委派给助手的具体子任务（用一句话写清要助手完成什么，不能为空）",
				},
				"context": map[string]interface{}{
					"type":        "string",
					"description": "完成子任务必需的背景信息（可选）",
				},
			},
			"required": []string{"assistant_id", "task"},
		},
		Func: func(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
			return s.executeCallAssistant(ctx, input, env, catalog, parentSession, rt)
		},
	}
}

// executeCallAssistant CallAssistant 编排执行（Agent Team P3，文档 §4.3）：
// 限深 1 层（子 registry 不含 CallAssistant + ToolEnv.Depth 兜底）、每 execute 委派 ≤5、
// 子 session 记 parent_session_id、5min 超时、摘要截断 4000 字符、用量经 accumulator 并入主循环计费。
func (s *AgentService) executeCallAssistant(ctx context.Context, input map[string]interface{}, env *ToolEnv, catalog []AssistantCapability, parentSession *model.AgentSession, rt *callAssistantRuntime) (map[string]interface{}, error) {
	if env == nil {
		return nil, errors.New("工具执行环境缺失")
	}
	// 防御性兜底：子 agent 环境禁止嵌套委派（结构性限深由子 registry 不含 CallAssistant 保证）
	if env.Depth > 0 {
		return nil, errors.New("CallAssistant 不允许在子任务中嵌套调用（限深 1 层）")
	}
	// 委派计数上限（计数器由 Execute 装配，每次 execute 独立）
	if env.DelegateCalls == nil {
		env.DelegateCalls = new(int)
	}
	if *env.DelegateCalls >= callAssistantMaxDelegates {
		return nil, fmt.Errorf("本次执行委派次数已达上限（%d 次）", callAssistantMaxDelegates)
	}

	assistantID, _ := input["assistant_id"].(string)
	task, _ := input["task"].(string)
	bgCtx, _ := input["context"].(string)
	if assistantID == "" || task == "" {
		return nil, fmt.Errorf("CallAssistant 参数不完整：assistant_id 与 task 均不能为空。请从能力目录照抄一个助手 ID 填入 assistant_id，并用一句话写清 task。可委派助手：%s", formatCatalogIDsForError(catalog))
	}
	// 仅允许委派给能力目录内的助手（schema enum 约束的服务端兜底）
	inCatalog := false
	for _, c := range catalog {
		if c.ID == assistantID {
			inCatalog = true
			break
		}
	}
	if !inCatalog {
		return nil, fmt.Errorf("助手 %s 不在可委派的能力目录内", assistantID)
	}
	if s.assistantSvc == nil || s.agentToolLoader == nil {
		return nil, errors.New("编排服务未装配")
	}
	if rt == nil || rt.client == nil {
		return nil, errors.New("LLM 客户端未就绪")
	}
	*env.DelegateCalls++

	// 解析助手与其子工具集（模块离线等既有规则不变）
	assistant, err := s.assistantSvc.getOwned(env.UserID, assistantID)
	if err != nil {
		return nil, err
	}
	// Agent Team P5：按助手 LLM 配置解析子 agent 客户端（follow/eleagent/byok）+ 子计费口径
	subClient, subModel, billProvider, billModel, err := s.resolveSubClient(ctx, assistant, rt)
	if err != nil {
		return nil, err
	}
	// Agent Team P5：逐次委派计费闸门——余额不足则拒绝委派（工具错误返回，由主 agent 决定是否换路）
	if s.billingService != nil {
		if err := s.billingService.CheckBalance(env.UserID, billProvider, billModel, CurrencyDanwan); err != nil {
			return nil, err
		}
	}
	agentIDs, err := s.assistantSvc.AgentIDsFor(env.UserID, assistantID)
	if err != nil {
		return nil, err
	}
	subTools, err := s.agentToolLoader.LoadToolsForUserFiltered(ctx, env.UserID, agentIDs)
	if err != nil {
		return nil, err
	}

	// 子 registry = 基础注册表克隆 + 子工具集，不注册 CallAssistant（结构性限深 1 层）
	subRegistry := s.registry.Clone()
	for _, t := range subTools {
		subRegistry.Register(t)
	}

	// 子 AgentSession：ParentSessionID = 主 session，独立 DiskPath
	parentSessionID := ""
	if parentSession != nil {
		parentSessionID = parentSession.ID
	}
	childSession, err := s.createSessionWithParent(ctx, env.UserID, env.ConversationID, parentSessionID, task)
	if err != nil {
		return nil, err
	}

	// 子 ToolEnv：UserID 相同、ConversationID 相同（同一对话沙箱，文件产物互相可见）、
	// SessionID = 子 session、Depth+1；委派计数器不共享（子环境无 CallAssistant）
	childEnv := &ToolEnv{
		UserID:           env.UserID,
		ConversationID:   env.ConversationID,
		SessionID:        childSession.ID,
		Sandbox:          env.Sandbox,
		SessionRepo:      env.SessionRepo,
		SearchProvider:   env.SearchProvider,
		Depth:            env.Depth + 1,
		UsageAccumulator: nil, // Agent Team P5：子用量即时扣费，不再经 accumulator（防双重计费）
	}

	// 子消息 = system（助手人格/默认专家模板）+ user(task + 可选背景)；不转发主对话历史
	systemPrompt := buildChildSystemPrompt(assistant, s.assistantSvc.skillItemsFor(assistantID))
	userContent := task
	if bgCtx != "" {
		userContent += "\n\n背景：\n" + bgCtx
	}
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}

	subCtx, cancel := context.WithTimeout(ctx, callAssistantTimeout)
	defer cancel()
	result, runErr := s.toolLoop.RunWithRegistry(subCtx, subRegistry, subClient, subModel, buildSubToolSchemas(subTools), messages, childEnv,
		// Agent Team P5：子任务进度流式下发，事件带 session_id/parent_session_id/sub 标记，前端据此嵌套分组
		func(record ToolCallRecord) error {
			if rt.writer != nil {
				s.writeEvent(rt.writer, "tool_call", map[string]interface{}{
					"step": record.Step, "tool": record.Tool, "arguments": json.RawMessage(record.Arguments),
					"session_id": childSession.ID, "parent_session_id": parentSessionID, "sub": true,
				})
				s.writeEvent(rt.writer, "tool_result", map[string]interface{}{
					"step":              record.Step,
					"tool":              record.Tool,
					"status":            map[bool]string{true: "succeeded", false: "failed"}[record.Error == ""],
					"output":            record.Output,
					"error_message":     record.Error,
					"session_id":        childSession.ID,
					"parent_session_id": parentSessionID,
					"sub":               true,
				})
			}
			return nil
		},
		func(output AssistantOutput) {
			if rt.writer == nil {
				return
			}
			if output.ReasoningContent != "" {
				s.writeEvent(rt.writer, "reasoning", map[string]interface{}{"delta": output.ReasoningContent, "session_id": childSession.ID, "sub": true})
			}
			if !output.IsFinal && output.Delta != "" {
				s.writeEvent(rt.writer, "intermediate_answer", map[string]interface{}{"delta": output.Delta, "session_id": childSession.ID, "sub": true})
			}
		},
	)

	// 子 session 状态收尾（失败仅记日志，不影响错误返回语义）
	status := "succeeded"
	if runErr != nil {
		status = "failed"
	}
	toolChainJSON := ""
	if result != nil && len(result.Records) > 0 {
		if b, merr := json.Marshal(result.Records); merr == nil {
			toolChainJSON = string(b)
		}
	}
	if uerr := s.updateSessionStatus(childSession.ID, status, toolChainJSON); uerr != nil && s.logger != nil {
		s.logger.Warn("更新子 Agent Session 状态失败", zap.String("session_id", childSession.ID), zap.Error(uerr))
	}
	if runErr != nil {
		return nil, runErr
	}

	// Agent Team P5：子用量即时扣费（标注 call_assistant:子session），不再经 accumulator 进主 totalUsage（防双重计费）
	if s.billingService != nil && result.Usage != nil {
		if err := s.billingService.DeductWithSource(env.UserID, billProvider, billModel, CurrencyDanwan, "call_assistant:"+childSession.ID, result.Usage); err != nil && s.logger != nil {
			s.logger.Warn("CallAssistant 子任务扣费失败", zap.String("user_id", env.UserID), zap.String("session_id", childSession.ID), zap.Error(err))
		}
	}

	summary := result.FinalContent
	if summary == "" {
		summary = "（子任务未给出最终回答）"
	}
	return map[string]interface{}{
		"summary":    truncateByRunes(summary, callAssistantSummaryMaxRunes),
		"session_id": childSession.ID,
		"tool_calls": len(result.Records),
	}, nil
}

// buildSubToolSchemas 将子工具集转换为 OpenAI 兼容的 tools schema 列表（Agent Team P3）
func buildSubToolSchemas(tools []*Tool) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			},
		})
	}
	return result
}

// buildChildSystemPrompt 子 agent system prompt（Agent Team P3，文档 §4.3/§9）：
// 助手自定义 system_prompt 非空时原样使用；空则使用默认专家模板。
// 默认模板附带可用技能名列表，并把各秘技（AgentItem）非空的 SystemPrompt 以「技能提示」附后
// （修复秘技人格此前完全不注入执行链路的现状）。
func buildChildSystemPrompt(assistant *model.Assistant, skills []*model.AgentItem) string {
	if assistant.SystemPrompt != "" {
		return assistant.SystemPrompt
	}
	desc := assistant.Description
	if desc == "" {
		desc = "综合任务"
	}
	names := make([]string, 0, len(skills))
	for _, sk := range skills {
		if sk.Name != "" {
			names = append(names, sk.Name)
		}
	}
	skillList := "无"
	if len(names) > 0 {
		skillList = strings.Join(names, "、")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "你是专家助手 %s，擅长 %s。可用技能：%s。独立完成任务，直接给出结果。", assistant.Name, desc, skillList)
	for _, sk := range skills {
		if sk.SystemPrompt == "" {
			continue
		}
		fmt.Fprintf(&b, "\n技能提示（%s）：%s", sk.Name, sk.SystemPrompt)
	}
	return b.String()
}
