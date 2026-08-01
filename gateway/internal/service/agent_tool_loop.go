package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/pkg/llm"
)

// AgentLLMClient Agent 工作流所需的 LLM 客户端能力
type AgentLLMClient interface {
	// Chat 非流式对话，支持 tools
	Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error)
	// ChatStream 流式对话
	ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error)
}

// ToolCallRecord 工具调用记录
type ToolCallRecord struct {
	Step      int                    `json:"step"`
	Tool      string                 `json:"tool"`
	Arguments string                 `json:"arguments"`
	Output    map[string]interface{} `json:"output,omitempty"`
	Error     string                 `json:"error,omitempty"`
	// AR-08：工具调用审计--延迟（毫秒）与输出大小（字节估算）
	LatencyMs  int64 `json:"latency_ms,omitempty"`
	OutputSize int   `json:"output_size,omitempty"`
}

// maxMalformedRetries 方括号标签工具调用格式错误时，反馈 LLM 重试的最大次数（AR-20）。
// 超过后放弃重试，透传最终回答（附说明），防 LLM 反复用错误格式烧步数。
const maxMalformedRetries = 2

// bracketMalformedPrompt 检测到 [工具名]...[/工具名] 文本标签但格式错误时注入的反馈（AR-20）。
const bracketMalformedPrompt = "（系统提示）检测到你用 [工具名]参数[/工具名] 文本标签调用工具，此格式不被支持。请使用结构化工具调用（function calling / tool_calls）调用工具，参数以 JSON 对象提供，工具名需精确匹配可用工具列表（区分大小写）。"

// maxBareJSONRetries 裸 JSON 工具调用反馈 LLM 重试的最大次数（AR-25）。
// 超过后放弃重试，透传最终回答（附说明），防 LLM 反复用裸 JSON 烧步数。
const maxBareJSONRetries = 2

// bareJSONPrompt 检测到裸 JSON 工具调用（无标记包裹，name 命中 registry）时注入的反馈（AR-25）。
// 不直接执行裸 JSON 以防误判正文里的 JSON 数据/示例，引导 LLM 改用结构化 tool_calls 或内联标记。
const bareJSONPrompt = "（系统提示）检测到你直接在正文输出裸 JSON 来调用工具（如 [{\"name\":\"...\",\"parameters\":{...}}]），此格式不会被直接执行（避免误判正文里的 JSON 数据/示例）。请改用以下任一方式调用工具：1. 结构化工具调用（function calling / tool_calls）；2. 内联标记格式：<|FunctionCallBegin|>[{\"name\":\"...\",\"parameters\":{...}}]<|FunctionCallEnd|>。工具名需精确匹配可用工具列表（区分大小写）。"

// functionGetName 元工具名（AR-26）：走内嵌标记（不支持原生 function calling）的模型用它
// 主动拉取当前 assistant 的工具列表+用法。不注册到 registry、不进 tools 参数，仅在 tool
// 执行循环按 name 拦截，返回 RenderToolsAsText(tools) 作为 tool_result 注入对话上下文。
const functionGetName = "FunctionGet"

// maxFunctionGetCalls 单次执行内 FunctionGet 最多返回工具列表的次数（防模型反复拉取烧步数）。
// 超过后返回「已在上文提供，勿重复请求」，不再塞工具列表。AR-26。
const maxFunctionGetCalls = 2

// ToolCallingLoop Function Calling 循环
type ToolCallingLoop struct {
	registry *ToolRegistry
	maxSteps int
	// maxRetries 上游可重试错误（5xx/429/网络错误）的最大尝试次数，默认 defaultUpstreamMaxAttempts
	maxRetries int
	// tokenBudget 单次执行 token 预算上限（0 表示不限制，AR-03）。
	// 循环内每轮累计 usage 后校验，超限强制进入最终回答，防止单次执行耗尽用户余额。
	tokenBudget int
	// logger 调试日志（内联工具调用解析路径诊断）；nil 时静默
	logger *zap.Logger
}

// SetLogger 设置调试日志（用于诊断内联工具调用解析路径）。临时排查用，可后续移除。
func (l *ToolCallingLoop) SetLogger(logger *zap.Logger) {
	l.logger = logger
}

// NewToolCallingLoop 创建循环控制器
func NewToolCallingLoop(registry *ToolRegistry, maxSteps int) *ToolCallingLoop {
	if maxSteps <= 0 {
		maxSteps = 500
	}
	return &ToolCallingLoop{registry: registry, maxSteps: maxSteps, maxRetries: defaultUpstreamMaxAttempts}
}

// SetMaxRetries 设置上游可重试错误的最大尝试次数（对应 llm.max_retries 配置）
func (l *ToolCallingLoop) SetMaxRetries(n int) {
	if n > 0 {
		l.maxRetries = n
	}
}

// SetTokenBudget 设置单次执行的 token 预算上限（AR-03）。
// 0 或负数表示不限制。对应 config agent.max_tokens_per_execute。
func (l *ToolCallingLoop) SetTokenBudget(b int) {
	if b > 0 {
		l.tokenBudget = b
	}
}

// RunResult 循环结果
type RunResult struct {
	Messages         []llm.Message
	Records          []ToolCallRecord
	FinalContent     string
	ReachMaxSteps    bool
	LoopDetected     bool       // 检测到同工具同参数循环调用
	ReachTokenBudget bool       // AR-03：达到 token 预算上限
	BudgetExceeded   bool       // AR-03：执行中余额校验失败
	ReachCostBudget  bool       // AR-03：达到 max_cost_per_task 成本上限
	Cancelled        bool       // AR-02：客户端取消（ctx 取消）
	Usage            *llm.Usage // 整个循环累计的 token 用量，用于计费
}

// AssistantOutput 工具循环中模型单轮非流式输出的中间内容（思考过程 / 说明文字）
type AssistantOutput struct {
	ReasoningContent string
	Delta            string
	IsFinal          bool
}

// Run 执行 Function Calling 循环（使用创建时绑定的 Registry）
// onToolCall 在每步工具执行后被调用；onAssistantOutput 在每次模型非流式输出后被调用，
// 用于将思考过程与中间说明文字实时透传到前端。
func (l *ToolCallingLoop) Run(
	ctx context.Context,
	client AgentLLMClient,
	model string,
	tools []map[string]interface{},
	messages []llm.Message,
	env *ToolEnv,
	onToolCall func(record ToolCallRecord) error,
	onAssistantOutput func(output AssistantOutput),
) (*RunResult, error) {
	return l.RunWithRegistry(ctx, l.registry, client, model, tools, messages, env, onToolCall, onAssistantOutput)
}

// RunWithRegistry 执行 Function Calling 循环，使用传入的 Registry（支持动态工具）
func (l *ToolCallingLoop) RunWithRegistry(
	ctx context.Context,
	registry *ToolRegistry,
	client AgentLLMClient,
	model string,
	tools []map[string]interface{},
	messages []llm.Message,
	env *ToolEnv,
	onToolCall func(record ToolCallRecord) error,
	onAssistantOutput func(output AssistantOutput),
) (*RunResult, error) {
	result := &RunResult{Messages: messages, Records: []ToolCallRecord{}}

	// maxSteps 限制的是“实际工具调用次数”而非 LLM 往返轮数，
	// 避免模型在 tool_choice=required 下出现“只说不做”或重复调用时把预算耗光。
	toolCallCount := 0
	callIndex := 1
	maxIterations := l.maxSteps + 2
	// 检测连续同工具同参数循环调用：允许最多连续 3 次，第 4 次终止工具链
	lastToolCallKey := ""
	consecutiveCount := 0
	const maxConsecutiveRepeats = 2
	// AR-20：方括号标签工具调用格式错误时反馈 LLM 重试的累计次数
	malformedRetries := 0
	// AR-25：裸 JSON 工具调用反馈 LLM 重试的累计次数
	bareJSONRetries := 0
	// AR-26：FunctionGet 元工具返回工具列表的累计次数（防反复拉取）
	functionGetCalls := 0

	for step := 0; step < maxIterations; step++ {
		// AR-02：客户端取消（断连）时立即结束循环，保留已产出供前端展示
		if ctx.Err() != nil {
			result.Cancelled = true
			break
		}
		req := llm.ChatRequest{
			Model:    model,
			Messages: result.Messages,
			Tools:    tools,
			Stream:   false,
		}
		// 统一使用 tool_choice=auto，由模型根据系统提示自行决定是否调用工具。
		// 避免某些模型对 required 不兼容导致反复失败或只不说做。
		if len(tools) > 0 {
			req.ToolChoice = "auto"
		}

		var resp *llm.ChatChunk
		err := callWithUpstreamRetry(ctx, l.maxRetries, func() error {
			var cerr error
			resp, cerr = chatWithToolFallback(ctx, client, req)
			return cerr
		})
		if err != nil {
			return nil, friendlyModelCallError("模型调用失败", err, nil)
		}
		result.Usage = addUsage(result.Usage, resp.Usage)

		// AR-03：token 预算校验，超限强制进入最终回答，防止单次执行耗尽用户余额
		if l.tokenBudget > 0 && result.Usage != nil && result.Usage.TotalTokens >= l.tokenBudget {
			result.ReachTokenBudget = true
			break
		}

		// AR-03：执行中余额校验（节流由回调内部实现），余额不足强制结束循环
		if env != nil && env.BudgetGuard != nil {
			if gErr := env.BudgetGuard(); gErr != nil {
				result.BudgetExceeded = true
				break
			}
		}

		// AR-03：每步成本门控（max_cost_per_task），累计成本超限强制进入最终回答
		if env != nil && env.CostGuard != nil && result.Usage != nil {
			if gErr := env.CostGuard(result.Usage); gErr != nil {
				result.ReachCostBudget = true
				break
			}
		}

		// 兼容在正文输出内联函数调用标记而非结构化 tool_calls 的模型，还原为真实
		// tool_calls 并剥离标记，否则标记会透传给用户（表现为「调用了却没执行」）：
		// (1) <|FunctionCallBegin|>...<|FunctionCallEnd|>（MiMo 系列）
		// (2) [ToolName]{json}[/ToolName] 方括号标签（中小厂商 / BYOK 弱模型，AR-20）
		if l.logger != nil {
			head := resp.Delta
			if len(head) > 300 {
				head = head[:300]
			}
			l.logger.Debug("[inline-parse] 收到上游响应",
				zap.Int("tool_calls", len(resp.ToolCalls)),
				zap.Int("delta_len", len(resp.Delta)),
				zap.String("delta_head", head),
				zap.Int("reasoning_len", len(resp.ReasoningContent)),
				zap.String("finish_reason", resp.FinishReason),
			)
		}
		if len(resp.ToolCalls) == 0 {
			if calls, cleaned := parseInlineFunctionCalls(resp.Delta); len(calls) > 0 {
				if l.logger != nil {
					l.logger.Debug("[inline-parse] 内联标记解析成功",
						zap.Int("calls", len(calls)),
						zap.Int("cleaned_len", len(cleaned)),
					)
				}
				resp.ToolCalls = calls
				resp.Delta = cleaned
			} else {
				if l.logger != nil {
					l.logger.Debug("[inline-parse] 内联未命中，尝试 bracket/bareJSON")
				}
				bc, bcCleaned, malformed := parseBracketToolCalls(resp.Delta)
				if malformed {
					// 形似 [tool] 标签但格式错误（非 JSON 体等）：不当最终回答透传，
					// 注入反馈让 LLM 改用结构化 function calling；超限则放弃并透传附说明。
					if malformedRetries < maxMalformedRetries {
						malformedRetries++
						result.Messages = append(result.Messages,
							llm.Message{Role: "assistant", Content: resp.Delta},
							llm.Message{Role: "user", Content: bracketMalformedPrompt},
						)
						continue
					}
					resp.Delta = resp.Delta + "\n\n（系统提示：检测到工具调用使用了不支持的文本标签格式，已超过重试上限，请改用结构化工具调用。）"
				} else if len(bc) > 0 {
					resp.ToolCalls = bc
					resp.Delta = bcCleaned
				} else if bare, _ := parseBareJSONToolCalls(resp.Delta); len(bare) > 0 && anyToolResolved(registry, bare) {
					// (3) 检测到裸 JSON 工具调用（name 命中 registry，模型意图调工具但用了裸 JSON
					// 格式）：不直接执行（防误判正文里的 JSON 数据/示例），反馈 LLM 改用结构化
					// tool_calls 或 <|FunctionCallBegin|> 标记格式（模型已支持，见 inline parser）。AR-25
					if bareJSONRetries < maxBareJSONRetries {
						bareJSONRetries++
						result.Messages = append(result.Messages,
							llm.Message{Role: "assistant", Content: resp.Delta},
							llm.Message{Role: "user", Content: bareJSONPrompt},
						)
						continue
					}
					resp.Delta = resp.Delta + "\n\n（系统提示：检测到工具调用使用了裸 JSON 格式，已超过重试上限，请改用结构化工具调用或内联标记格式。）"
				}
			}
		}

		isFinal := len(resp.ToolCalls) == 0
		if onAssistantOutput != nil {
			onAssistantOutput(AssistantOutput{
				ReasoningContent: resp.ReasoningContent,
				Delta:            resp.Delta,
				IsFinal:          isFinal,
			})
		}

		// 没有 tool_calls：模型选择直接给出最终回答
		if isFinal {
			result.FinalContent = resp.Delta
			return result, nil
		}

		// 记录 assistant 的 tool_calls（同时保留其说明文字）
		result.Messages = append(result.Messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Delta,
			ToolCalls: resp.ToolCalls,
		})

		// 先检测本轮 tool_calls 是否构成连续同工具同参数循环
		loopDetected := false
		for _, tc := range resp.ToolCalls {
			key := tc.Function.Name + ":" + tc.Function.Arguments
			if key == lastToolCallKey {
				consecutiveCount++
			} else {
				lastToolCallKey = key
				consecutiveCount = 1
			}
			if consecutiveCount > maxConsecutiveRepeats {
				loopDetected = true
				break
			}
		}
		if loopDetected {
			result.LoopDetected = true
			break
		}

		// 执行本轮返回的每个 tool_call
		for _, tc := range resp.ToolCalls {
			// AR-26：FunctionGet 元工具拦截。走内嵌标记的模型用它主动拉取工具列表，
			// 不执行真实工具，返回当前 assistant 的工具能力（RenderToolsAsText）作为 tool_result。
			if strings.EqualFold(tc.Function.Name, functionGetName) {
				functionGetCalls++
				var list string
				if functionGetCalls <= maxFunctionGetCalls {
					list = RenderToolsAsText(tools)
				} else {
					list = "工具列表已在之前的 FunctionGet 返回中提供，请查看上文消息，勿重复请求。"
				}
				record := ToolCallRecord{
					Step:      callIndex,
					Tool:      functionGetName,
					Arguments: tc.Function.Arguments,
					Output:    map[string]interface{}{"tool_list": list},
				}
				result.Records = append(result.Records, record)
				if onToolCall != nil {
					if err := onToolCall(record); err != nil {
						return nil, err
					}
				}
				callIndex++
				result.Messages = append(result.Messages, llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    list,
				})
				toolCallCount++
				continue
			}

			record := ToolCallRecord{
				Step:      callIndex,
				Tool:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			}

			tool, resolvedName, ok := registry.Resolve(tc.Function.Name)
			if !ok {
				// 工具名未命中（含归一化后）：列出可用工具名，提示 LLM 用准确名字重试
				avail := make([]string, 0, 8)
				for _, t := range registry.List() {
					avail = append(avail, t.Name)
				}
				record.Error = fmt.Sprintf("未知工具: %s。可用工具：%s", tc.Function.Name, strings.Join(avail, ", "))
			} else {
				record.Tool = resolvedName // AR-20：记录归一化后的真实工具名
				var input map[string]interface{}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
					record.Error = fmt.Sprintf("参数解析失败: %v", err)
				} else {
					// C1：审批闸（env.Approver 非空时判定 allow/deny/ask）。deny/失败时跳过 tool.Func，
					// record.Error 已填，下方 compactToolResult 将拒绝原因回灌 LLM 供其改道。
					// C3：注入当前 toolCallID，供 ExitPlanMode 等需在工具内部发起交互审批的工具用作 key。
					env.CurrentToolCallID = tc.ID
					if requestToolApproval(ctx, env, tool, resolvedName, input, tc.ID, &record) {
						// AR-08：工具调用审计--计时
						start := time.Now()
						output, err := tool.Func(ctx, input, env)
						record.LatencyMs = time.Since(start).Milliseconds()
						if err != nil {
							record.Error = err.Error()
						} else {
							record.Output = output
							// AR-08：输出大小估算（JSON 序列化字节数，失败则 0）
							if sz, mErr := json.Marshal(output); mErr == nil {
								record.OutputSize = len(sz)
							}
						}
					}
				}
			}

			result.Records = append(result.Records, record)
			if onToolCall != nil {
				if err := onToolCall(record); err != nil {
					return nil, err
				}
			}
			callIndex++

			// 将工具结果追加到对话上下文
			// 注意：OpenAI 标准 tool message 只需要 role/tool_call_id/content，
			// 不传递 name 字段，避免某些严格的上游因额外字段拒绝请求。
			// AR-01：tool 结果经 compactToolResult 提炼后回灌，避免长输出撑爆上下文。
			result.Messages = append(result.Messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    compactToolResult(record.Output, record.Error),
			})
			toolCallCount++

		}

		// AR-01 滑动窗口：保留最近 recentToolKeep 个 tool message 完整内容，
		// 更早的进一步压缩为短预算，控制 prompt tokens 随步数线性增长。
		result.Messages = compactOldToolMessages(result.Messages, result.Records, recentToolKeep)

		// 达到实际工具调用上限或检测到循环，强制进入最终回答
		if toolCallCount >= l.maxSteps || result.LoopDetected {
			break
		}
	}

	// 达到最大工具调用次数或检测到循环，强制生成最终回答
	// 防御性校验：确保每个 assistant tool_call 都有对应的 tool message，
	// 避免历史消息或异常路径导致 upstream 报 tool_call_id 缺失。
	result.Messages = normalizeToolMessages(result.Messages)

	req := llm.ChatRequest{
		Model:    model,
		Messages: result.Messages,
		Stream:   false,
	}
	var finalResp *llm.ChatChunk
	err := callWithUpstreamRetry(ctx, l.maxRetries, func() error {
		var cerr error
		finalResp, cerr = client.Chat(ctx, req)
		return cerr
	})
	if err != nil {
		return nil, friendlyModelCallError("最终回答模型调用失败", err, nil)
	}
	result.FinalContent = finalResp.Delta
	result.ReachMaxSteps = true
	result.Usage = addUsage(result.Usage, finalResp.Usage)
	if onAssistantOutput != nil {
		onAssistantOutput(AssistantOutput{
			ReasoningContent: finalResp.ReasoningContent,
			Delta:            finalResp.Delta,
			IsFinal:          true,
		})
	}
	return result, nil
}

// normalizeToolMessages 防御性校验 messages 中 assistant tool_calls 与 tool messages 的对应关系。
// 某些上游或历史消息可能导致 tool_call_id 不匹配，自动补充缺失的 tool message 可避免 upstream 400。
func normalizeToolMessages(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}
	result := make([]llm.Message, 0, len(messages))
	pendingToolCallIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			result = append(result, msg)
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					pendingToolCallIDs[tc.ID] = true
				}
			}
			continue
		}
		if msg.Role == "tool" {
			if msg.ToolCallID != "" {
				delete(pendingToolCallIDs, msg.ToolCallID)
			}
			result = append(result, msg)
			continue
		}
		result = append(result, msg)
	}
	// 为仍未响应的 tool_call_id 补充兜底 tool message
	for id := range pendingToolCallIDs {
		result = append(result, llm.Message{
			Role:       "tool",
			ToolCallID: id,
			Content:    "工具响应缺失",
		})
	}
	return result
}

// addUsage 累加两次调用的 token 用量
func addUsage(dst, src *llm.Usage) *llm.Usage {
	if src == nil {
		return dst
	}
	if dst == nil {
		return &llm.Usage{
			PromptTokens:     src.PromptTokens,
			CompletionTokens: src.CompletionTokens,
			TotalTokens:      src.TotalTokens,
			CachedTokens:     src.CachedTokens,
		}
	}
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.TotalTokens += src.TotalTokens
	dst.CachedTokens += src.CachedTokens
	return dst
}

// chatWithToolFallback 调用模型，若上游因 tool_choice=required 与 thinking 冲突而拒绝，
// 则降级为 tool_choice="auto" 重试一次（部分 Kimi 思考模型不支持 required）。
func chatWithToolFallback(ctx context.Context, client AgentLLMClient, req llm.ChatRequest) (*llm.ChatChunk, error) {
	resp, err := client.Chat(ctx, req)
	if err == nil {
		return resp, nil
	}
	if req.ToolChoice == "" || !strings.Contains(err.Error(), "tool_choice") || !strings.Contains(err.Error(), "thinking") {
		return nil, err
	}
	fallbackReq := req
	fallbackReq.ToolChoice = "auto"
	return client.Chat(ctx, fallbackReq)
}

// toolResultToString 将工具结果转为字符串
func toolResultToString(output map[string]interface{}, errStr string) string {
	if errStr != "" {
		return fmt.Sprintf("工具执行失败: %s", errStr)
	}
	b, err := json.Marshal(output)
	if err != nil {
		return fmt.Sprintf("%v", output)
	}
	return string(b)
}

// requestToolApproval C1 审批闸：先查 PermissionService 决策（allow/deny/ask），
// ask 时经 env.Approver 阻塞请求用户审批（发 SSE approval_request + 等待 /agent/approve 投递）。
// 返回 true=放行执行，false=拒绝（record.Error 已填，调用方跳过 tool.Func）。
// env 或 env.Approver 为 nil（云端）时直接放行，行为同现状。
func requestToolApproval(ctx context.Context, env *ToolEnv, tool *Tool, toolName string, input map[string]interface{}, toolCallID string, record *ToolCallRecord) bool {
	if env == nil || env.Approver == nil {
		return true // 云端无审批器：放行
	}
	var decision model.PermissionDecision
	if env.PermissionSvc != nil {
		decision = env.PermissionSvc.Decide(env.PermissionMode, tool, input)
	} else {
		// 无决策引擎：只读放行，其余 ask
		if tool.ReadOnly {
			decision = model.PermissionDecisionAllow
		} else {
			decision = model.PermissionDecisionAsk
		}
	}
	switch decision {
	case model.PermissionDecisionAllow:
		return true
	case model.PermissionDecisionDeny:
		record.Error = "权限拒绝：当前模式（" + string(env.PermissionMode) + "）不允许执行此工具"
		return false
	default: // ask
		req := ApprovalRequest{
			SessionID:     env.SessionID,
			ToolCallID:    toolCallID,
			Tool:          toolName,
			Arguments:     input,
			ArgumentsRaw:  record.Arguments,
			RiskLevel:     riskLevelFor(tool, toolName),
			Reason:        approvalReason(tool, toolName, env.PermissionMode),
		}
		dec, err := env.Approver.RequestApproval(ctx, req)
		if err != nil {
			record.Error = "审批未通过：" + err.Error()
			return false
		}
		if dec.Decision != "allow" {
			record.Error = "用户拒绝执行：" + dec.Reason
			return false
		}
		// 用户选「总是允许」：构造 Tool(spec) 落库
		if dec.AlwaysAllow == "" && env.PermissionSvc != nil {
			if spec := alwaysAllowSpec(toolName, input); spec != "" {
				env.PermissionSvc.AddAlwaysAllow(spec, model.PermissionDecisionAllow)
			}
		} else if dec.AlwaysAllow != "" && env.PermissionSvc != nil {
			env.PermissionSvc.AddAlwaysAllow(dec.AlwaysAllow, model.PermissionDecisionAllow)
		}
		return true
	}
}
