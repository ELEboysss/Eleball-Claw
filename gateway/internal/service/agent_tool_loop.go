package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
}

// ToolCallingLoop Function Calling 循环
type ToolCallingLoop struct {
	registry  *ToolRegistry
	maxSteps  int
	// maxRetries 上游可重试错误（5xx/429/网络错误）的最大尝试次数，默认 defaultUpstreamMaxAttempts
	maxRetries int
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

// RunResult 循环结果
type RunResult struct {
	Messages      []llm.Message
	Records       []ToolCallRecord
	FinalContent  string
	ReachMaxSteps bool
	LoopDetected  bool // 检测到同工具同参数循环调用
	Usage         *llm.Usage // 整个循环累计的 token 用量，用于计费
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

	for step := 0; step < maxIterations; step++ {
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
			record := ToolCallRecord{
				Step:      callIndex,
				Tool:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			}

			tool, ok := registry.Get(tc.Function.Name)
			if !ok {
				record.Error = fmt.Sprintf("未知工具: %s", tc.Function.Name)
			} else {
				var input map[string]interface{}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
					record.Error = fmt.Sprintf("参数解析失败: %v", err)
				} else {
					output, err := tool.Func(ctx, input, env)
					if err != nil {
						record.Error = err.Error()
					} else {
						record.Output = output
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
			result.Messages = append(result.Messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    toolResultToString(record.Output, record.Error),
			})
			toolCallCount++

		}

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
