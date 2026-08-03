package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	"go.uber.org/zap"
)

// CompactionSummary C4：LLM 生成的结构化对话摘要。
// 字段与前端展示、后续压缩累计文件列表强相关，修改时需同步前端 banner。
type CompactionSummary struct {
	Goal                 string   `json:"goal"`
	Constraints          []string `json:"constraints"`
	ProgressDone         []string `json:"progress_done"`
	ProgressInProgress   []string `json:"progress_in_progress"`
	ProgressBlocked      []string `json:"progress_blocked"`
	Decisions            []string `json:"decisions"`
	NextSteps            []string `json:"next_steps"`
	CriticalContext      string   `json:"critical_context"`
	ReadFiles            []string `json:"read_files"`
	ModifiedFiles        []string `json:"modified_files"`
	// C4 plan 模式：压缩时保留 plan 状态，确保摘要和后端元数据携带该上下文。
	PlanMode bool   `json:"plan_mode,omitempty"`
	PlanPath string `json:"plan_path,omitempty"`
}

// CompactionResult C4：一次压缩的完整结果，用于 SSE 事件与持久化。
type CompactionResult struct {
	BeforeTokens      int      `json:"before_tokens"`
	AfterTokens       int      `json:"after_tokens"`
	SummaryMarkdown   string   `json:"summary_markdown"`
	FirstKeptEntryID  string   `json:"first_kept_entry_id"`
	RetainedTailCount int      `json:"retained_tail_count"`
	Reason            string   `json:"reason"`
	Focus             string   `json:"focus"`
	ReadFiles         []string `json:"read_files"`
	ModifiedFiles     []string `json:"modified_files"`
	// C4 plan 模式：标记本次压缩处于 plan 模式及对应的计划文件路径，用于跨压缩恢复上下文。
	PlanMode bool   `json:"plan_mode,omitempty"`
	PlanPath string `json:"plan_path,omitempty"`
}

// compactCircuitBreaker C4：单会话熔断状态。
type compactCircuitBreaker struct {
	consecutiveFailures int
	immediateRehits     int
	lastAfterTokens     int
	paused              bool
}

// ContextCompactor C4：对话级上下文压缩器。
// 负责切点计算、摘要生成、持久化（仅插入摘要条目，不删除旧消息）、熔断与累计文件列表。
type ContextCompactor struct {
	repo   *repository.ChatConversationRepo
	cfg    config.CompactionConfig
	logger *zap.Logger
	// C2：PreCompact 生命周期钩子服务（claw-only）；nil 时跳过。
	hookSvc *HookService

	cbMu sync.Mutex
	cb   map[string]*compactCircuitBreaker

	perConvMu sync.Mutex
	convLocks map[string]*sync.Mutex
}

// NewContextCompactor 创建对话级压缩器。
func NewContextCompactor(repo *repository.ChatConversationRepo, cfg config.CompactionConfig, logger *zap.Logger) *ContextCompactor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ContextCompactor{
		repo:      repo,
		cfg:       cfg,
		logger:    logger,
		cb:        make(map[string]*compactCircuitBreaker),
		convLocks: make(map[string]*sync.Mutex),
	}
}

// SetHookService 注入 C2 PreCompact 钩子服务（claw-only）。
func (c *ContextCompactor) SetHookService(svc *HookService) {
	c.hookSvc = svc
}

// CompactDuringLoop 在工具循环内执行压缩。传入已解析的 client/model 与平行 msgIDs，
// 返回压缩结果、新的消息列表与新的平行 ID 列表。
// permissionMode/planFilePath/cwd 用于 C4 plan 模式上下文保留与 C2 PreCompact hook。
func (c *ContextCompactor) CompactDuringLoop(
	ctx context.Context,
	client AgentLLMClient,
	modelName, conversationID, userID, sessionID string,
	messages []llm.Message,
	msgIDs []string,
	focus string,
	permissionMode model.PermissionMode,
	planFilePath string,
	cwd string,
) (*CompactionResult, []llm.Message, []string, error) {
	lock := c.convLock(conversationID)
	lock.Lock()
	defer lock.Unlock()

	reason := "threshold"
	if focus != "" {
		reason = "manual"
	}
	return c.compact(ctx, client, modelName, conversationID, userID, sessionID, messages, msgIDs, focus, reason, true, permissionMode, planFilePath, cwd)
}

// CompactConversation 手动压缩整个对话历史。client 由调用方按当前会话模型解析。
func (c *ContextCompactor) CompactConversation(
	ctx context.Context,
	client AgentLLMClient,
	modelName, conversationID, userID string,
	focus string,
) (*CompactionResult, error) {
	lock := c.convLock(conversationID)
	lock.Lock()
	defer lock.Unlock()

	msgs, err := c.loadConversationMessages(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	msgIDs := make([]string, len(msgs))
	res, _, _, err := c.compact(ctx, client, modelName, conversationID, userID, "", msgs, msgIDs, focus, "manual", true, model.PermissionModeDefault, "", "")
	if err != nil {
		return nil, err
	}
	return res, nil
}

// convLock 返回并复用某个会话的排他锁。
func (c *ContextCompactor) convLock(conversationID string) *sync.Mutex {
	c.perConvMu.Lock()
	defer c.perConvMu.Unlock()
	if c.convLocks[conversationID] == nil {
		c.convLocks[conversationID] = &sync.Mutex{}
	}
	return c.convLocks[conversationID]
}

// compact 核心压缩逻辑。persist=true 时把摘要条目写入数据库（不删除旧消息）。
func (c *ContextCompactor) compact(
	ctx context.Context,
	client AgentLLMClient,
	modelName, conversationID, userID, sessionID string,
	messages []llm.Message,
	msgIDs []string,
	focus, reason string,
	persist bool,
	permissionMode model.PermissionMode,
	planFilePath string,
	cwd string,
) (*CompactionResult, []llm.Message, []string, error) {
	cb := c.cbState(conversationID)
	if cb.paused && reason == "threshold" {
		return nil, messages, msgIDs, fmt.Errorf("自动压缩已熔断，请手动触发或重启会话")
	}

	beforeTokens := c.estimateTokens(messages)
	if c.cfg.ThresholdTokens > 0 && beforeTokens < c.cfg.ThresholdTokens && reason == "threshold" {
		return nil, messages, msgIDs, nil
	}

	cut, retainedTail, err := c.findCutPoint(messages)
	if err != nil {
		cb.consecutiveFailures++
		c.checkCircuitBreaker(cb, conversationID)
		return nil, messages, msgIDs, fmt.Errorf("无法找到有效切点: %w", err)
	}
	if cut <= 0 {
		return nil, messages, msgIDs, nil
	}

	prefix := messages[:cut]
	firstKeptID := ""
	if cut < len(msgIDs) {
		firstKeptID = msgIDs[cut]
	}

	readFiles, modifiedFiles, err := c.accumulateFileLists(ctx, conversationID, prefix)
	if err != nil && c.logger != nil {
		c.logger.Warn("累计文件列表失败", zap.Error(err))
	}

	// C2：调用 LLM 摘要前分发 PreCompact 钩子；被阻断时回退硬截断。
	if c.hookSvc != nil {
		preview := fmt.Sprintf("即将对前缀中的 %d 条消息生成结构化摘要（focus=%q, plan_mode=%v, plan_path=%q）", len(prefix), focus, permissionMode == model.PermissionModePlan, planFilePath)
		outcome, hErr := c.hookSvc.DispatchPreCompact(ctx, sessionID, conversationID, cwd, preview)
		if hErr == nil && !outcome.IsAllow() {
			fallback := c.fallbackTruncate(messages, cut)
			if outcome.SystemMessage != "" {
				fallback = append(fallback, llm.Message{Role: "system", Content: outcome.SystemMessage})
			}
			return nil, fallback, msgIDs, fmt.Errorf("PreCompact hook 拒绝压缩: %s", outcome.BlockReason)
		}
		if hErr != nil && c.logger != nil {
			c.logger.Warn("PreCompact hook 分发失败", zap.Error(hErr))
		}
	}

	summary, err := c.summarize(ctx, client, modelName, prefix, focus, readFiles, modifiedFiles, permissionMode == model.PermissionModePlan, planFilePath)
	if err != nil {
		cb.consecutiveFailures++
		c.checkCircuitBreaker(cb, conversationID)
		fallback := c.fallbackTruncate(messages, cut)
		return nil, fallback, msgIDs, fmt.Errorf("摘要生成失败，已回退硬截断: %w", err)
	}

	summary.ReadFiles = mergeUnique(summary.ReadFiles, readFiles)
	summary.ModifiedFiles = mergeUnique(summary.ModifiedFiles, modifiedFiles)
	// C4 plan 模式：把当前 plan 状态写入摘要结构，便于渲染和持久化。
	summary.PlanMode = permissionMode == model.PermissionModePlan
	summary.PlanPath = planFilePath

	summaryMarkdown := renderCompactionMarkdown(summary)
	summaryMsg := llm.Message{
		Role:    "system",
		Content: "[以下为对话历史摘要，替代了更早的详细消息]\n\n" + summaryMarkdown,
	}

	newMessages := make([]llm.Message, 0, 1+len(retainedTail)+1)
	newMsgIDs := make([]string, 0, 1+len(retainedTail)+1)
	if len(messages) > 0 && messages[0].Role == "system" {
		newMessages = append(newMessages, messages[0])
		if len(msgIDs) > 0 {
			newMsgIDs = append(newMsgIDs, msgIDs[0])
		} else {
			newMsgIDs = append(newMsgIDs, "")
		}
	}
	newMessages = append(newMessages, summaryMsg)
	newMsgIDs = append(newMsgIDs, "")
	newMessages = append(newMessages, retainedTail...)
	if cut < len(msgIDs) {
		newMsgIDs = append(newMsgIDs, msgIDs[cut:]...)
	} else {
		for range retainedTail {
			newMsgIDs = append(newMsgIDs, "")
		}
	}

	afterTokens := c.estimateTokens(newMessages)

	res := &CompactionResult{
		BeforeTokens:      beforeTokens,
		AfterTokens:       afterTokens,
		SummaryMarkdown:   summaryMarkdown,
		FirstKeptEntryID:  firstKeptID,
		RetainedTailCount: len(retainedTail),
		Reason:            reason,
		Focus:             focus,
		ReadFiles:         summary.ReadFiles,
		ModifiedFiles:     summary.ModifiedFiles,
		PlanMode:          summary.PlanMode,
		PlanPath:          summary.PlanPath,
	}

	if beforeTokens >= c.cfg.ThresholdTokens && afterTokens >= c.cfg.ThresholdTokens {
		cb.immediateRehits++
	} else {
		cb.immediateRehits = 0
	}
	cb.lastAfterTokens = afterTokens

	if cb.immediateRehits >= 3 && reason == "threshold" {
		cb.paused = true
		if c.logger != nil {
			c.logger.Warn("C4：压缩后上下文仍持续超限，判定为抖动，暂停自动压缩", zap.String("conversation_id", conversationID))
		}
	}

	cb.consecutiveFailures = 0

	if persist && c.repo != nil && conversationID != "" {
		if err := c.persist(ctx, conversationID, userID, sessionID, res); err != nil && c.logger != nil {
			c.logger.Warn("C4：保存压缩条目失败", zap.String("conversation_id", conversationID), zap.Error(err))
		}
	}

	return res, newMessages, newMsgIDs, nil
}

// cbState 返回某会话的熔断状态（懒创建）。
func (c *ContextCompactor) cbState(conversationID string) *compactCircuitBreaker {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	if c.cb[conversationID] == nil {
		c.cb[conversationID] = &compactCircuitBreaker{}
	}
	return c.cb[conversationID]
}

// checkCircuitBreaker 连续失败 3 次后暂停自动压缩。
func (c *ContextCompactor) checkCircuitBreaker(cb *compactCircuitBreaker, conversationID string) {
	if cb.consecutiveFailures >= 3 {
		cb.paused = true
		if c.logger != nil {
			c.logger.Warn("C4：摘要连续失败达到 3 次，暂停自动压缩", zap.String("conversation_id", conversationID))
		}
	}
}

// estimateTokens 粗略估算消息列表的 token 数（1 token ≈ 4 字符，图片按固定成本）。
func (c *ContextCompactor) estimateTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += 4
		total += estimateMessageContentTokens(m)
		for _, tc := range m.ToolCalls {
			total += len([]rune(tc.Function.Arguments)) / 4
			total += 4
		}
		if m.ToolCallID != "" {
			total += len([]rune(m.ToolCallID)) / 4
			total += 2
		}
	}
	return total
}

// estimateMessageContentTokens 估算单条消息 content 的 token 数。
func estimateMessageContentTokens(m llm.Message) int {
	switch v := m.Content.(type) {
	case string:
		return len([]rune(v)) / 4
	case []llm.ContentPart:
		tokens := 0
		for _, p := range v {
			switch p.Type {
			case "text":
				tokens += len([]rune(p.Text)) / 4
			case "image_url":
				tokens += 1000
			case "file":
				if p.File != nil {
					tokens += len([]rune(p.File.Text)) / 4
				}
			}
		}
		return tokens
	case []interface{}:
		tokens := 0
		for _, part := range v {
			if mp, ok := part.(map[string]interface{}); ok {
				pt, _ := mp["type"].(string)
				if pt == "text" {
					if t, ok := mp["text"].(string); ok {
						tokens += len([]rune(t)) / 4
					}
				}
				if pt == "image_url" {
					tokens += 1000
				}
			}
		}
		return tokens
	default:
		return len([]rune(llm.MessageContentToString(m.Content))) / 4
	}
}

// findCutPoint 从最新消息反向累计，找到保留 keep_recent_tokens 预算的切点。
// 返回切点索引（messages[:cut] 为摘要前缀，messages[cut:] 为保留尾部），且切点不落在 tool_call/tool_result 配对中间。
func (c *ContextCompactor) findCutPoint(messages []llm.Message) (int, []llm.Message, error) {
	keep := c.cfg.KeepRecentTokens
	if keep <= 0 {
		keep = 20000
	}

	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		return 0, messages, nil
	}

	total := 0
	cut := lastUserIdx
	for i := len(messages) - 1; i >= 0; i-- {
		if i > lastUserIdx {
			continue
		}
		total += estimateMessageContentTokens(messages[i]) + 4
		if total > keep && i <= lastUserIdx {
			cut = i
			break
		}
		cut = i
	}

	for cut > 0 && messages[cut].Role == "tool" && messages[cut].ToolCallID != "" {
		found := false
		for i := cut - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" {
				for _, tc := range messages[i].ToolCalls {
					if tc.ID == messages[cut].ToolCallID {
						cut = i
						found = true
						break
					}
				}
			}
			if found {
				break
			}
		}
		if !found {
			break
		}
	}

	if cut > lastUserIdx {
		cut = lastUserIdx
	}
	if cut <= 0 {
		return 0, messages, nil
	}
	return cut, messages[cut:], nil
}

// accumulateFileLists 读取历史 compaction 条目中的文件列表，并与本次 prefix 提取结果合并。
func (c *ContextCompactor) accumulateFileLists(ctx context.Context, conversationID string, prefix []llm.Message) ([]string, []string, error) {
	readSet := make(map[string]struct{})
	modifiedSet := make(map[string]struct{})

	if c.repo != nil && conversationID != "" {
		all, _, err := c.repo.ListMessages(conversationID, 1, 1000)
		if err == nil {
			for _, m := range all {
				if m.Role != "compaction" || m.ToolResults == "" {
					continue
				}
				var meta struct {
					ReadFiles     []string `json:"read_files"`
					ModifiedFiles []string `json:"modified_files"`
				}
				if err := json.Unmarshal([]byte(m.ToolResults), &meta); err != nil {
					continue
				}
				for _, f := range meta.ReadFiles {
					readSet[f] = struct{}{}
				}
				for _, f := range meta.ModifiedFiles {
					modifiedSet[f] = struct{}{}
				}
			}
		}
	}

	extractFileLists(prefix, readSet, modifiedSet)
	return mapKeysSorted(readSet), mapKeysSorted(modifiedSet), nil
}

// extractFileLists 从工具调用结果中提取读/写文件路径。
func extractFileLists(messages []llm.Message, readSet, modifiedSet map[string]struct{}) {
	for _, m := range messages {
		content := llm.MessageContentToString(m.Content)
		if content == "" && len(m.ToolCalls) == 0 {
			continue
		}
		if m.Role == "tool" && content != "" {
			var out map[string]interface{}
			if err := json.Unmarshal([]byte(content), &out); err == nil {
				collectPaths(out, readSet)
			}
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if isWriteTool(tc.Function.Name) {
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil {
						if path, ok := args["path"].(string); ok && path != "" {
							modifiedSet[path] = struct{}{}
						}
					}
				}
			}
		}
	}
}

// isWriteTool 判断工具名是否为文件写操作。
func isWriteTool(name string) bool {
	return strings.EqualFold(name, "WriteFile") || strings.EqualFold(name, "StrReplaceFile") ||
		strings.EqualFold(name, "CreateFile") || strings.EqualFold(name, "Move")
}

// collectPaths 递归收集 map 中的 path 字符串。
func collectPaths(m map[string]interface{}, set map[string]struct{}) {
	if path, ok := m["path"].(string); ok && path != "" {
		set[path] = struct{}{}
	}
	if paths, ok := m["paths"].([]interface{}); ok {
		for _, p := range paths {
			if ps, ok := p.(string); ok && ps != "" {
				set[ps] = struct{}{}
			}
		}
	}
	for _, v := range m {
		if child, ok := v.(map[string]interface{}); ok {
			collectPaths(child, set)
		}
	}
}

// summarize 调用 LLM 生成结构化摘要。
func (c *ContextCompactor) summarize(
	ctx context.Context,
	client AgentLLMClient,
	modelName string,
	prefix []llm.Message,
	focus string,
	readFiles, modifiedFiles []string,
	planMode bool,
	planPath string,
) (*CompactionSummary, error) {
	if client == nil {
		return nil, fmt.Errorf("LLM 客户端未提供")
	}
	prompt := buildSummaryPrompt(prefix, focus, readFiles, modifiedFiles, planMode, planPath)
	req := llm.ChatRequest{
		Model:    modelName,
		Messages: []llm.Message{{Role: "system", Content: prompt}},
		Stream:   false,
	}
	resp, err := client.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return parseSummaryJSON(resp.Delta)
}

// buildSummaryPrompt 构造摘要生成提示词。
func buildSummaryPrompt(prefix []llm.Message, focus string, readFiles, modifiedFiles []string, planMode bool, planPath string) string {
	var b strings.Builder
	b.WriteString("请对以下对话历史进行结构化摘要。输出必须是合法 JSON，格式如下：\n")
	b.WriteString(`{
  "goal": "用户原始目标（一句话）",
  "constraints": ["约束1"],
  "progress_done": ["已完成"],
  "progress_in_progress": ["进行中"],
  "progress_blocked": ["失败/阻塞"],
  "decisions": ["已做出的关键决策"],
  "next_steps": ["建议下一步"],
  "critical_context": "必须保留的精确信息",
  "read_files": ["读取过的文件路径"],
  "modified_files": ["修改/创建过的文件路径"],
  "plan_mode": true,
  "plan_path": "计划文件路径（若处于 plan 模式）"
}`)
	b.WriteString("\n\n对话历史（从旧到新）：\n")
	for i, m := range prefix {
		role := m.Role
		content := llm.MessageContentToString(m.Content)
		if role == "tool" {
			content = truncateRunes(content, 2000)
		} else {
			content = truncateRunes(content, 4000)
		}
		fmt.Fprintf(&b, "\n[%d] %s: %s", i, role, content)
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "\n  -> tool_call %s: %s", tc.Function.Name, truncateRunes(tc.Function.Arguments, 500))
		}
	}
	if focus != "" {
		fmt.Fprintf(&b, "\n\n请重点关注：%s", focus)
	}
	if planMode {
		fmt.Fprintf(&b, "\n\n【Plan 模式上下文】当前会话处于 Plan 模式。摘要中必须保留该状态，并在 critical_context 中说明：计划文件路径为 %q，后续轮次仍应遵守 plan 模式约束（只读调研，直到用户接受计划）。", planPath)
	}
	if len(readFiles) > 0 {
		fmt.Fprintf(&b, "\n\n历史已读取文件（请保留并在本次更新）：\n%s", strings.Join(readFiles, "\n"))
	}
	if len(modifiedFiles) > 0 {
		fmt.Fprintf(&b, "\n\n历史已修改文件（请保留并在本次更新）：\n%s", strings.Join(modifiedFiles, "\n"))
	}
	b.WriteString("\n\n请只输出 JSON，不要包含 markdown 代码块或解释。")
	return b.String()
}

// parseSummaryJSON 解析 LLM 返回的 JSON 摘要，兼容代码块包裹。
func parseSummaryJSON(text string) (*CompactionSummary, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) > 2 {
			text = strings.Join(lines[1:len(lines)-1], "\n")
		}
		text = strings.TrimSpace(text)
	}
	var s CompactionSummary
	if err := json.Unmarshal([]byte(text), &s); err != nil {
		return nil, fmt.Errorf("解析摘要 JSON 失败: %w", err)
	}
	return &s, nil
}

// renderCompactionMarkdown 将结构化摘要渲染为 markdown 文本。
func renderCompactionMarkdown(s *CompactionSummary) string {
	var b strings.Builder
	if s.Goal != "" {
		fmt.Fprintf(&b, "**目标**：%s\n\n", s.Goal)
	}
	if len(s.Constraints) > 0 {
		b.WriteString("**约束**：\n")
		for _, c := range s.Constraints {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		b.WriteString("\n")
	}
	if len(s.ProgressDone) > 0 || len(s.ProgressInProgress) > 0 || len(s.ProgressBlocked) > 0 {
		b.WriteString("**进展**：\n")
		for _, x := range s.ProgressDone {
			fmt.Fprintf(&b, "- ✅ 已完成：%s\n", x)
		}
		for _, x := range s.ProgressInProgress {
			fmt.Fprintf(&b, "- ⏳ 进行中：%s\n", x)
		}
		for _, x := range s.ProgressBlocked {
			fmt.Fprintf(&b, "- ❌ 阻塞：%s\n", x)
		}
		b.WriteString("\n")
	}
	if len(s.Decisions) > 0 {
		b.WriteString("**决策**：\n")
		for _, d := range s.Decisions {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\n")
	}
	if s.CriticalContext != "" {
		fmt.Fprintf(&b, "**关键上下文**：\n%s\n\n", s.CriticalContext)
	}
	if len(s.NextSteps) > 0 {
		b.WriteString("**建议下一步**：\n")
		for _, x := range s.NextSteps {
			fmt.Fprintf(&b, "- %s\n", x)
		}
		b.WriteString("\n")
	}
	if len(s.ReadFiles) > 0 {
		b.WriteString("**已读文件**：\n")
		for _, f := range s.ReadFiles {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}
	if len(s.ModifiedFiles) > 0 {
		b.WriteString("**已改文件**：\n")
		for _, f := range s.ModifiedFiles {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}
	if s.PlanMode {
		fmt.Fprintf(&b, "\n**Plan 模式**：当前会话处于 Plan 模式")
		if s.PlanPath != "" {
			fmt.Fprintf(&b, "，计划文件：`%s`", s.PlanPath)
		}
		b.WriteString("；摘要后仍需遵守只读调研约束，直到用户接受计划。\n")
	}
	return strings.TrimSpace(b.String())
}

// fallbackTruncate 摘要失败时回退硬截断：保留系统提示、截断提示与保留尾部。
func (c *ContextCompactor) fallbackTruncate(messages []llm.Message, cut int) []llm.Message {
	if cut <= 0 || cut >= len(messages) {
		return messages
	}
	retained := messages[cut:]
	out := make([]llm.Message, 0, 2+len(retained))
	if len(messages) > 0 && messages[0].Role == "system" {
		out = append(out, messages[0])
	}
	out = append(out, llm.Message{
		Role:    "system",
		Content: "[前缀上下文因长度限制被截断，仅保留最近消息]",
	})
	out = append(out, retained...)
	return out
}

// persist 保存压缩条目到数据库（仅插入，不删除旧消息；旧消息由前端根据 first_kept_entry_id 过滤）。
func (c *ContextCompactor) persist(
	ctx context.Context,
	conversationID, userID, sessionID string,
	res *CompactionResult,
) error {
	if c.repo == nil {
		return nil
	}
	meta := map[string]interface{}{
		"first_kept_entry_id":  res.FirstKeptEntryID,
		"retained_tail_count":  res.RetainedTailCount,
		"reason":               res.Reason,
		"focus":                res.Focus,
		"before_tokens":        res.BeforeTokens,
		"after_tokens":         res.AfterTokens,
		"read_files":           res.ReadFiles,
		"modified_files":       res.ModifiedFiles,
		"plan_mode":            res.PlanMode,
		"plan_path":            res.PlanPath,
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	compactionMsg := &model.ChatMessage{
		ID:             generateID("compact"),
		ConversationID: conversationID,
		Role:           "compaction",
		Content:        res.SummaryMarkdown,
		ToolResults:    string(metaJSON),
		CreatedAt:      time.Now().Unix(),
	}
	if err := c.repo.CreateMessage(compactionMsg); err != nil {
		return err
	}
	_ = ctx
	_ = userID
	_ = sessionID
	return nil
}

// loadConversationMessages 从数据库加载对话消息并转换为 llm.Message，含 compaction 条目。
func (c *ContextCompactor) loadConversationMessages(ctx context.Context, conversationID, userID string) ([]llm.Message, error) {
	if c.repo == nil {
		return nil, fmt.Errorf("repository 未初始化")
	}
	all, _, err := c.repo.ListMessages(conversationID, 1, 1000)
	if err != nil {
		return nil, err
	}
	msgs := make([]llm.Message, 0, len(all)+1)
	for _, m := range all {
		role := m.Role
		if role == "compaction" {
			role = "system"
		}
		msgs = append(msgs, llm.Message{
			Role:    role,
			Content: m.Content,
		})
	}
	_ = ctx
	_ = userID
	return msgs, nil
}

// mergeUnique 合并两个字符串数组并去重排序。
func mergeUnique(a, b []string) []string {
	set := make(map[string]struct{})
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, x := range b {
		set[x] = struct{}{}
	}
	return mapKeysSorted(set)
}

// mapKeysSorted 返回 map 排序后的键列表。
func mapKeysSorted(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
