package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// AgentWorkflowHandler Agent 工作流处理器
type AgentWorkflowHandler struct {
	agentService *service.AgentService
	// claw：搜索能力下沉到 search-web 模块，注入后 ListSearchProviders 优先转发模块 list_sources
	skillRuntimeRegistry *service.SkillRuntimeRegistry
	// C5：slash 命令服务（输入栏命令中心）
	slashService *service.SlashCommandService
	// C8：项目记忆文件加载服务（/agent/memory）
	contextFileService *service.ContextFileService
}

// NewAgentWorkflowHandler 创建处理器
func NewAgentWorkflowHandler(agentService *service.AgentService) *AgentWorkflowHandler {
	return &AgentWorkflowHandler{agentService: agentService}
}

// SetSkillRuntimeRegistry 注入统一运行时注册表（claw 用：search-providers 转发 search-web 模块）。
// 不改构造签名以保持向后兼容；未注入时 ListSearchProviders 回退环境变量。
func (h *AgentWorkflowHandler) SetSkillRuntimeRegistry(r *service.SkillRuntimeRegistry) {
	h.skillRuntimeRegistry = r
}

// SetSlashCommandService 注入 slash 命令服务（C5）
func (h *AgentWorkflowHandler) SetSlashCommandService(s *service.SlashCommandService) {
	h.slashService = s
}

// SetContextFileService 注入项目记忆文件加载服务（C8）
func (h *AgentWorkflowHandler) SetContextFileService(s *service.ContextFileService) {
	h.contextFileService = s
}

// getUserID 从 gin context 获取当前用户 ID
func (h *AgentWorkflowHandler) getUserID(c *gin.Context) (string, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return "", false
	}
	s, ok := userID.(string)
	return s, ok
}

// Execute 执行 Agent 工作流（SSE）
func (h *AgentWorkflowHandler) Execute(c *gin.Context) {
	var req service.AgentExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	ctx := context.WithValue(c.Request.Context(), "user_id", userID)

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	// 调用 service 执行，使用 gin.ResponseWriter 作为 SSE writer
	_ = h.agentService.Execute(ctx, req, c.Writer)
}

// Approve C1 工具审批决策（POST /v1/agent/approve）。
// 跨 HTTP 请求解锁阻塞在审批闸的工具循环：决策经共享 registry 投递到等待中的 channel。
func (h *AgentWorkflowHandler) Approve(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	var req struct {
		SessionID   string `json:"session_id"`
		ToolCallID  string `json:"tool_call_id"`
		Decision    string `json:"decision"`     // allow / deny
		AlwaysAllow string `json:"always_allow"`  // Tool(spec)，allow 时可选「总是允许」
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.SessionID == "" || req.ToolCallID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "session_id 与 tool_call_id 必填"})
		return
	}
	// 归一化决策值
	decision := "deny"
	if req.Decision == "allow" {
		decision = "allow"
	}
	// 所有权校验：会话必须属于当前用户，防越权审批他人会话
	if _, err := h.agentService.GetSession(c.Request.Context(), req.SessionID, userID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 3001, "message": "会话不存在或无权访问"})
		return
	}
	dec := service.ApprovalDecision{
		Decision:    decision,
		AlwaysAllow:  req.AlwaysAllow,
	}
	// 未命中（已超时/已决策）幂等返回成功，前端据此收起卡片
	h.agentService.DeliverApproval(req.SessionID, req.ToolCallID, dec)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// PlanReview C3 plan 审批决策（POST /v1/agent/plan-review）。
// 复用 C1 的共享 registry 投递：ExitPlanMode 工具阻塞在 plan_review，此处投递 accepted/rejected/refined。
// refined 时 feedback 作为细化反馈回灌 LLM 供其修订 plan。
func (h *AgentWorkflowHandler) PlanReview(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	var req struct {
		SessionID  string `json:"session_id"`
		ToolCallID string `json:"tool_call_id"`
		Decision   string `json:"decision"`  // accepted / rejected / refined
		Feedback   string `json:"feedback"`   // refined 时的细化反馈
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.SessionID == "" || req.ToolCallID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "session_id 与 tool_call_id 必填"})
		return
	}
	decision := normalizePlanDecision(req.Decision)
	// 所有权校验：会话必须属于当前用户
	if _, err := h.agentService.GetSession(c.Request.Context(), req.SessionID, userID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 3001, "message": "会话不存在或无权访问"})
		return
	}
	dec := service.ApprovalDecision{
		Decision: decision,
		Reason:   req.Feedback, // refined 时为细化反馈；accepted/rejected 时为空
	}
	h.agentService.DeliverApproval(req.SessionID, req.ToolCallID, dec)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// normalizePlanDecision 归一化 plan 审批决策值，非法值回落 rejected。
func normalizePlanDecision(d string) string {
	switch d {
	case "accepted", "rejected", "refined":
		return d
	}
	return "rejected"
}

// ListPermissionRules C1 列出权限规则（GET /v1/agent/permission-rules）。
func (h *AgentWorkflowHandler) ListPermissionRules(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"code": 0, "message": "success",
		"data": gin.H{"rules": h.agentService.ListPermissionRules()},
	})
}

// AddPermissionRule C1 新增权限规则（POST /v1/agent/permission-rules）。
func (h *AgentWorkflowHandler) AddPermissionRule(c *gin.Context) {
	var req struct {
		Spec     string `json:"spec"`
		Decision string `json:"decision"` // allow / deny / ask
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	h.agentService.AddPermissionRule(req.Spec, normalizeDecision(req.Decision))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// DeletePermissionRule C1 删除权限规则（DELETE /v1/agent/permission-rules?spec=）。
func (h *AgentWorkflowHandler) DeletePermissionRule(c *gin.Context) {
	spec := c.Query("spec")
	h.agentService.RemovePermissionRule(spec)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// normalizeDecision 归一化决策文本到 model.PermissionDecision。
func normalizeDecision(s string) model.PermissionDecision {
	switch s {
	case "allow":
		return model.PermissionDecisionAllow
	case "deny":
		return model.PermissionDecisionDeny
	case "ask":
		return model.PermissionDecisionAsk
	default:
		return model.PermissionDecisionAsk
	}
}

// ListSessions 查询当前用户的 Agent Session 列表
func (h *AgentWorkflowHandler) ListSessions(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	items, total, err := h.agentService.ListSessions(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"total": total, "items": items},
	})
}

// GetSession 查询 Agent Session 详情
func (h *AgentWorkflowHandler) GetSession(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	id := c.Param("id")
	item, err := h.agentService.GetSession(c.Request.Context(), id, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// ForkSession 会话分叉（AR-12）：从分叉点消息 entry_id 复制父 session 对话历史到新 session。
func (h *AgentWorkflowHandler) ForkSession(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	var req struct {
		EntryID string `json:"entry_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	id := c.Param("id")
	session, err := h.agentService.ForkSession(c.Request.Context(), id, userID, req.EntryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": session})
}

// GetSessionAudit 查询 Agent Session 统一审计视图（AR-08）。
// 聚合工具调用记录（latency/output_size）与文件写审计（unified diff），供 claw-console/admin 展示。
func (h *AgentWorkflowHandler) GetSessionAudit(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	id := c.Param("id")
	audit, err := h.agentService.GetSessionAudit(c.Request.Context(), id, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": audit})
}

// DeleteSession 删除 Agent Session 及其磁盘资源
func (h *AgentWorkflowHandler) DeleteSession(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	id := c.Param("id")
	if err := h.agentService.DeleteSession(c.Request.Context(), id, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// DeleteSessions 批量删除 Agent Session 及其资源
// - 带 conversation_id 查询参数：删除该对话下的所有 Session
// - 不带查询参数：删除当前用户的全部 Session
func (h *AgentWorkflowHandler) DeleteSessions(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	conversationID := c.Query("conversation_id")
	var err error
	if conversationID != "" {
		err = h.agentService.DeleteSessionsByConversation(c.Request.Context(), userID, conversationID)
	} else {
		err = h.agentService.DeleteAllSessions(c.Request.Context(), userID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// Steer C6：向运行中的 Agent Session 注入一条 steer 消息。
// steer 在当前工具调用完成后、下一轮 LLM 调用前作为 user message 注入。
func (h *AgentWorkflowHandler) Steer(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	id := c.Param("id")
	var req struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "text 不能为空"})
		return
	}
	// 所有权校验：会话必须属于当前用户
	if _, err := h.agentService.GetSession(c.Request.Context(), id, userID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 3001, "message": "会话不存在或无权访问"})
		return
	}
	h.agentService.PushSteer(id, strings.TrimSpace(req.Text))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// Followup C6：向 Agent Session 排队一条 follow-up 消息。
// 当前 Agent 回合结束后自动作为新用户输入继续执行；Agent 已结束时行为等同于新发起执行。
func (h *AgentWorkflowHandler) Followup(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	id := c.Param("id")
	var req struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "text 不能为空"})
		return
	}
	// 所有权校验：会话必须属于当前用户
	if _, err := h.agentService.GetSession(c.Request.Context(), id, userID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 3001, "message": "会话不存在或无权访问"})
		return
	}
	h.agentService.PushFollowup(id, strings.TrimSpace(req.Text))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// Compact C4：手动触发对话级上下文压缩。
// 请求可指定 conversation_id 与 focus；模型缺省时使用会话绑定的模型/默认模型。
func (h *AgentWorkflowHandler) Compact(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	var req service.AgentCompactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}
	if req.ConversationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "conversation_id 不能为空"})
		return
	}
	ctx := context.WithValue(c.Request.Context(), "user_id", userID)
	res, err := h.agentService.Compact(ctx, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": res})
}

// ListSearchProviders 返回当前已配置的可用搜索源列表
// 前端据此动态渲染搜索源下拉框，未配置 key 的源不展示。
//
// claw：搜索能力下沉到 search-web 模块（源配置在模块容器侧），优先转发模块的
// list_sources 取真实可用源；模块离线或未注入 registry 时回退读 gateway 环境变量。
func (h *AgentWorkflowHandler) ListSearchProviders(c *gin.Context) {
	if h.skillRuntimeRegistry != nil {
		if providers, err := h.listSearchWebSources(c); err == nil && len(providers) > 0 {
			c.JSON(http.StatusOK, gin.H{
				"code":    0,
				"message": "success",
				"data":    providers,
				"source":  "search-web",
			})
			return
		}
	}
	// 回退：模块离线或未配置时，读 gateway 环境变量（云端行为）
	providers := service.ListAvailableSearchProviders()
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    providers,
		"source":  "env-fallback",
	})
}

// listSearchWebSources 调 search-web 模块的 list_sources，过滤可用源并映射为
// [{name,label}]（对齐 ListAvailableSearchProviders 契约，前端无需改动）。
func (h *AgentWorkflowHandler) listSearchWebSources(c *gin.Context) ([]gin.H, error) {
	userID, _ := h.getUserID(c)
	result, err := h.skillRuntimeRegistry.Execute("search-web", "list_sources", map[string]interface{}{}, userID)
	if err != nil {
		return nil, err
	}
	sourcesRaw, ok := result["sources"]
	if !ok {
		return nil, errors.New("search-web 未返回 sources")
	}
	arr, ok := sourcesRaw.([]interface{})
	if !ok {
		return nil, errors.New("search-web sources 格式异常")
	}
	out := make([]gin.H, 0, len(arr))
	for _, s := range arr {
		m, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		// 仅返回 available=true 的源（未配置凭据的不展示）
		if avail, _ := m["available"].(bool); !avail {
			continue
		}
		name, _ := m["name"].(string)
		label, _ := m["label"].(string)
		if name == "" {
			continue
		}
		out = append(out, gin.H{"name": name, "label": label})
	}
	return out, nil
}

// GetResource 匿名代理下载 Agent 输出资源
func (h *AgentWorkflowHandler) GetResource(c *gin.Context) {
	id := c.Param("id")
	data, mimeType, fileName, err := h.agentService.GetResource(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 4004, "message": err.Error()})
		return
	}

	if mimeType != "" {
		c.Header("Content-Type", mimeType)
	}
	if fileName != "" {
		c.Header("Content-Disposition", "attachment; filename=\""+fileName+"\"")
	}
	c.Data(http.StatusOK, mimeType, data)
}

// SlashCommands C5：返回当前用户可见的 slash 命令列表（输入栏命令中心）
func (h *AgentWorkflowHandler) SlashCommands(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	if h.slashService == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"categories": []interface{}{}}})
		return
	}
	resp, err := h.slashService.ListCommands(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": resp})
}

// FilesFuzzy C5：返回工作区文件 fuzzy 补全结果（@ mention）
func (h *AgentWorkflowHandler) FilesFuzzy(c *gin.Context) {
	if _, ok := h.getUserID(c); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	if h.slashService == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"files": []interface{}{}}})
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "q 参数不能为空"})
		return
	}
	cwd := c.Query("cwd")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	resp, err := h.slashService.FuzzyFiles(cwd, q, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": resp})
}

// Memory C8：查询当前工作目录下已加载的项目记忆文件（CLAUDE.md / AGENTS.md）。
func (h *AgentWorkflowHandler) Memory(c *gin.Context) {
	if _, ok := h.getUserID(c); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	if h.contextFileService == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"files": []interface{}{}}})
		return
	}
	cwd := c.Query("cwd")
	if cwd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "cwd 参数不能为空"})
		return
	}
	files, err := h.contextFileService.ListLoadedFiles(cwd)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"files": files}})
}

// GetSessionState C10：查询指定 Agent Session 的运行时状态（是否运行中、队列消息等）。
func (h *AgentWorkflowHandler) GetSessionState(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	id := c.Param("id")
	session, err := h.agentService.GetSession(c.Request.Context(), id, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 3001, "message": "会话不存在或无权访问"})
		return
	}

	running := session.Status == "running"
	steering, followUp := []service.SteerMessage{}, []service.FollowupMessage{}
	if q := h.agentService.GetSteerQueue(id); q != nil {
		steering, followUp = q.Snapshot()
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"running":           running,
			"is_streaming":      running,
			"is_prompt_running": running,
			"is_bash_running":   false,
			"queued_messages": gin.H{
				"steering": steering,
				"followUp": followUp,
			},
		},
	})
}

// RunningSessionsEvents C10：SSE 流式推送当前用户运行中 Session 集合的变化。
// 客户端应先订阅此接口，再读取初始快照；每次状态变化时收到 type=running 事件，
// 30 秒一次心跳（: \n\n）。
func (h *AgentWorkflowHandler) RunningSessionsEvents(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	writeEvent := func(event string, data interface{}) bool {
		b, _ := json.Marshal(data)
		_, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(b))
		if err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}

	writeHeartbeat := func() bool {
		_, err := fmt.Fprint(c.Writer, ":\n\n")
		if err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}

	sendSnapshot := func() {
		ids := h.agentService.GetRunningSessionIDs(userID)
		writeEvent("running", map[string]interface{}{
			"type":                "running",
			"running_session_ids": ids,
		})
	}

	sub := h.agentService.SubscribeRunningSessions(userID)
	if sub == nil {
		// 无订阅能力时只发送一次当前快照后保持心跳。
		sendSnapshot()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-c.Request.Context().Done():
				return
			case <-ticker.C:
				if !writeHeartbeat() {
					return
				}
			}
		}
	}

	// 发送初始快照
	sendSnapshot()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer h.agentService.UnsubscribeRunningSessions(userID, sub)

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-sub:
			// 收到变化信号，先订阅新的 channel 再释放旧的，避免丢失中间状态变更。
			newSub := h.agentService.SubscribeRunningSessions(userID)
			h.agentService.UnsubscribeRunningSessions(userID, sub)
			sub = newSub
			if sub == nil {
				return
			}
			sendSnapshot()
		case <-ticker.C:
			if !writeHeartbeat() {
				return
			}
		}
	}
}
