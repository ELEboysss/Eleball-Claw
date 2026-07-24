package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	"go.uber.org/zap"
)

// maxContextRuneLen 单条消息文本上下文最大长度，防止超长文件撑爆模型上下文窗口
const maxContextRuneLen = 100000

// ChatProxyService 对话代理服务
type ChatProxyService struct {
	keyManager           *KeyManagerService
	clientFactory        *ClientFactory
	eleAgentModelService *EleAgentModelService
	userRepo             *repository.UserRepo
	logger               *zap.Logger
	// envFallback 保存环境变量兜底客户端（provider -> client）
	envFallback map[llm.Provider]llm.Client
	// maxRetries 上游可重试错误（5xx/429/网络错误）的最大尝试次数，默认 defaultUpstreamMaxAttempts
	maxRetries int
}

// NewChatProxyService 创建对话代理服务
func NewChatProxyService(keyManager *KeyManagerService, clientFactory *ClientFactory, eleAgentModelService *EleAgentModelService, logger *zap.Logger) *ChatProxyService {
	if keyManager == nil {
		keyManager = NewNoOpKeyManager()
	}
	if clientFactory == nil {
		// 思考模型首包可能较慢，默认响应头超时放宽到 3 分钟
		clientFactory = NewClientFactory(180 * time.Second)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ChatProxyService{
		keyManager:           keyManager,
		clientFactory:        clientFactory,
		eleAgentModelService: eleAgentModelService,
		logger:               logger,
		envFallback:          make(map[llm.Provider]llm.Client),
		maxRetries:           defaultUpstreamMaxAttempts,
	}
}

// SetMaxRetries 设置上游可重试错误的最大尝试次数（对应 llm.max_retries 配置）
func (s *ChatProxyService) SetMaxRetries(n int) {
	if n > 0 {
		s.maxRetries = n
	}
}

// RegisterFallbackClient 注册环境变量兜底客户端
// 当数据库中无可用 Key 时，使用这些客户端。
func (s *ChatProxyService) RegisterFallbackClient(provider llm.Provider, client llm.Client) {
	s.envFallback[provider] = client
}

// GetFallbackClient 获取已注册的兜底客户端
func (s *ChatProxyService) GetFallbackClient(provider llm.Provider) llm.Client {
	return s.envFallback[provider]
}

// SetUserRepo 设置用户仓库，用于在对话时刷新用户活跃时间
func (s *ChatProxyService) SetUserRepo(userRepo *repository.UserRepo) {
	s.userRepo = userRepo
}

// TouchUserActive 更新用户最近活跃时间，用于 DAU 统计
func (s *ChatProxyService) TouchUserActive(userID string) {
	if s.userRepo == nil || userID == "" {
		return
	}
	if err := s.userRepo.TouchActive(userID); err != nil {
		s.logger.Warn("刷新用户活跃时间失败", zap.String("user_id", userID), zap.Error(err))
	}
}

// HasClient 检查是否已注册指定 Provider 的客户端（数据库 Key 或兜底客户端）
func (s *ChatProxyService) HasClient(provider llm.Provider) bool {
	if s.keyManager.HasAnyKey(string(provider)) {
		return true
	}
	_, ok := s.envFallback[provider]
	return ok
}

// ChatRequest 对话请求 DTO（与 handler 层共享）
type ChatRequest struct {
	Provider            string               `json:"provider" binding:"required"`
	Model               string               `json:"model" binding:"required"`
	Messages            []llm.Message        `json:"messages" binding:"required"`
	Stream              bool                 `json:"stream"`
	Temperature         float64              `json:"temperature,omitempty"`
	TopP                float64              `json:"top_p,omitempty"`
	MaxTokens           int                  `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                  `json:"max_completion_tokens,omitempty"`
	Thinking            *llm.ThinkingOptions `json:"thinking,omitempty"`
	PromptCacheKey      string               `json:"prompt_cache_key,omitempty"`
	SafetyIdentifier    string               `json:"safety_identifier,omitempty"`
	Stop                []string             `json:"stop,omitempty"`
	Currency            string               `json:"currency,omitempty"`
}

// toLLMChatRequest 将 service 层 ChatRequest 转换为 llm 层 ChatRequest，
// 保留所有 OpenAI / Kimi Code 兼容的扩展字段。
func toLLMChatRequest(req ChatRequest, messages []llm.Message) llm.ChatRequest {
	return llm.ChatRequest{
		Model:               req.Model,
		Messages:            messages,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		MaxTokens:           req.MaxTokens,
		MaxCompletionTokens: req.MaxCompletionTokens,
		Stream:              req.Stream,
		Thinking:            req.Thinking,
		PromptCacheKey:      req.PromptCacheKey,
		SafetyIdentifier:    req.SafetyIdentifier,
		Stop:                req.Stop,
	}
}

// Chat 非流式对话
func (s *ChatProxyService) Chat(ctx context.Context, req *ChatRequest) (*llm.ChatChunk, error) {
	// 纯图片/纯视频生成模型不允许发起文字对话，避免通道与协议不对齐打到上游
	if !s.supportsChat(req.Provider, req.Model) {
		return nil, errors.New("当前模型不支持文字对话，请切换到对话模型后重试")
	}
	supportsVision := s.supportsVision(req.Provider, req.Model)
	messages, err := normalizeMessageContents(req.Messages, supportsVision)
	if err != nil {
		return nil, err
	}

	// Ele Agent 官方模型：后端代理到真实子平台模型
	if llm.Provider(req.Provider) == llm.ProviderEleAgent {
		req.Messages = messages
		return s.chatEleAgent(ctx, *req)
	}

	client, _, err := s.getClient(req.Provider)
	if err != nil {
		return nil, err
	}

	llmReq := toLLMChatRequest(*req, messages)
	chunk, err := client.Chat(ctx, llmReq)
	if err != nil {
		return nil, err
	}

	// 非流式响应同样要检测空内容，便于定位“模型未返回任何内容”问题
	if chunk != nil && chunk.Delta == "" && chunk.ReasoningContent == "" {
		s.logger.Error("上游非流式响应内容为空",
			zap.String("provider", req.Provider),
			zap.String("model", req.Model),
			zap.Any("usage", chunk.Usage),
			zap.String("finish_reason", chunk.FinishReason),
		)
	}

	return chunk, nil
}

// ChatStream 流式对话
func (s *ChatProxyService) ChatStream(ctx context.Context, req *ChatRequest, w io.Writer) (*llm.Usage, error) {
	// 纯图片/纯视频生成模型不允许发起文字对话，避免通道与协议不对齐打到上游
	if !s.supportsChat(req.Provider, req.Model) {
		return nil, errors.New("当前模型不支持文字对话，请切换到对话模型后重试")
	}
	supportsVision := s.supportsVision(req.Provider, req.Model)
	messages, err := normalizeMessageContents(req.Messages, supportsVision)
	if err != nil {
		return nil, err
	}

	s.logger.Debug("ChatStream 请求",
		zap.String("provider", req.Provider),
		zap.String("model", req.Model),
		zap.Bool("supports_vision", supportsVision),
		zap.Bool("stream", req.Stream),
		zap.Int("messages", len(messages)),
		zap.Int("image_count", countImageContent(messages)),
		zap.Int("file_count", countFileContent(messages)),
		zap.Int("text_rune_len", totalTextRuneLen(messages)),
	)

	// Ele Agent 官方模型：后端代理到真实子平台模型
	if llm.Provider(req.Provider) == llm.ProviderEleAgent {
		req.Messages = messages
		return s.chatEleAgentStream(ctx, *req, w)
	}

	client, keyID, err := s.getClient(req.Provider)
	if err != nil {
		s.logger.Error("获取客户端失败", zap.String("provider", req.Provider), zap.Error(err))
		return nil, err
	}

	llmReq := toLLMChatRequest(*req, messages)

	chunkChan, err := client.ChatStream(ctx, llmReq)
	if err != nil {
		s.logger.Error("调用上游模型流式接口失败", zap.String("provider", req.Provider), zap.Error(err))
		if keyID != "" {
			_ = s.keyManager.ReportFailure(keyID, err.Error())
		}
		return nil, err
	}

	var usage *llm.Usage
	chunkCount := 0
	contentChunkCount := 0
	totalDeltaLen := 0
	totalReasoningLen := 0
	var lastFinishReason string
	for chunk := range chunkChan {
		chunkCount++
		data, _ := json.Marshal(toOpenAIStreamChunk(chunk))
		s.logger.Debug("转发流式 chunk",
			zap.Int("index", chunkCount),
			zap.Int("delta_len", len(chunk.Delta)),
			zap.Int("reasoning_len", len(chunk.ReasoningContent)),
			zap.Int("data_len", len(data)),
		)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if chunk.Delta != "" || chunk.ReasoningContent != "" {
			contentChunkCount++
			totalDeltaLen += len(chunk.Delta)
			totalReasoningLen += len(chunk.ReasoningContent)
		}
		if chunk.FinishReason != "" {
			lastFinishReason = chunk.FinishReason
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if flusher, ok := w.(httpFlusher); ok {
			_ = flusher.Flush()
		}
	}

	// 检测到上游未返回任何有效内容时，打印完整调试信息，便于定位问题
	if contentChunkCount == 0 {
		s.logger.Error("上游流式响应未返回任何有效内容",
			zap.String("provider", req.Provider),
			zap.String("model", req.Model),
			zap.String("key_id", maskKeyID(keyID)),
			zap.Int("chunks", chunkCount),
			zap.Int("total_delta_len", totalDeltaLen),
			zap.Int("total_reasoning_len", totalReasoningLen),
			zap.Any("usage", usage),
			zap.String("finish_reason", lastFinishReason),
			zap.Int("messages", len(messages)),
			zap.String("request_summary", summarizeMessages(messages, 500)),
		)
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	if chunkCount == 0 {
		s.logger.Error("上游模型未返回任何 chunk", zap.String("provider", req.Provider), zap.String("model", req.Model))
	} else {
		s.logger.Debug("ChatStream 完成", zap.Int("chunks", chunkCount), zap.Int("content_chunks", contentChunkCount), zap.Int("total_delta_len", totalDeltaLen), zap.Int("total_reasoning_len", totalReasoningLen), zap.Any("usage", usage))
	}

	if keyID != "" && usage != nil && usage.TotalTokens > 0 {
		_ = s.keyManager.ReportSuccess(keyID, int64(usage.TotalTokens))
	}

	return usage, nil
}

// getClient 根据 Provider 获取客户端
// 优先从 KeyManagerService 动态创建；无可用 Key 时回退到环境变量客户端。
func (s *ChatProxyService) getClient(provider string) (llm.Client, string, error) {
	selected, err := s.keyManager.SelectKey(provider)
	if err == nil {
		client, err := s.clientFactory.Create(selected.Key.Provider, selected.Plaintext, selected.Key.BaseURL)
		if err != nil {
			return nil, "", fmt.Errorf("创建 %s 客户端失败: %w", provider, err)
		}
		return client, selected.Key.ID, nil
	}

	// fallback：使用环境变量预注册的客户端
	client, ok := s.envFallback[llm.Provider(provider)]
	if !ok {
		return nil, "", fmt.Errorf("不支持的模型厂商: %s", provider)
	}
	return client, "", nil
}

// ResolveAgentClient 为 Agent 工作流解析真实上游 LLM 客户端
// 当 provider 为 eleagent 时，根据 model 解析子平台并获取管理员配置的真实模型凭据。
func (s *ChatProxyService) ResolveAgentClient(ctx context.Context, provider, model string) (llm.Client, error) {
	if llm.Provider(provider) == llm.ProviderEleAgent {
		subProvider, subModel, err := parseEleAgentModel(model)
		if err != nil {
			return nil, err
		}
		credential, err := s.eleAgentModelService.GetCredentialForRequest(ctx, subProvider, subModel)
		if err != nil {
			return nil, fmt.Errorf("Ele Agent 模型未配置: %w", err)
		}
		client, err := s.clientFactory.CreateByProtocol(credential.Protocol, credential.APIKey, credential.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("创建 Ele Agent 上游客户端失败: %w", err)
		}
		return client, nil
	}

	// 非 Ele Agent：优先从 KeyManager 获取；无可用 Key 时使用环境变量兜底客户端。
	client, _, err := s.getClient(provider)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// chatEleAgent Ele Agent 后端代理：根据管理员配置调用真实模型
func (s *ChatProxyService) chatEleAgent(ctx context.Context, req ChatRequest) (*llm.ChatChunk, error) {
	subProvider, subModel, err := parseEleAgentModel(req.Model)
	if err != nil {
		return nil, err
	}

	credential, err := s.eleAgentModelService.GetCredentialForRequest(ctx, subProvider, subModel)
	if err != nil {
		return nil, fmt.Errorf("Ele Agent 模型未配置: %w", err)
	}

	client, err := s.clientFactory.CreateByProtocol(credential.Protocol, credential.APIKey, credential.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("创建 Ele Agent 上游客户端失败: %w", err)
	}

	// 将 Ele Agent 模型名替换为真实子模型名，同时透传温度、top_p、thinking 等扩展字段
	req.Model = subModel
	// 部分模型（如 Kimi 的 coding 系列）要求 temperature 只能为 1
	req.Temperature = normalizeEleAgentTemperature(subProvider, subModel, req.Temperature)
	llmReq := toLLMChatRequest(req, req.Messages)
	llmReq.Stream = false

	var chunk *llm.ChatChunk
	err = callWithUpstreamRetry(ctx, s.maxRetries, func() error {
		var cerr error
		chunk, cerr = client.Chat(ctx, llmReq)
		return cerr
	})
	if err != nil {
		return nil, friendlyModelCallError("Ele Agent 模型调用失败", err, s.logger)
	}

	if chunk != nil && chunk.Delta == "" && chunk.ReasoningContent == "" {
		s.logger.Error("Ele Agent 上游非流式响应内容为空",
			zap.String("sub_provider", subProvider),
			zap.String("sub_model", subModel),
			zap.String("base_url", credential.BaseURL),
			zap.Any("usage", chunk.Usage),
			zap.String("finish_reason", chunk.FinishReason),
		)
	}

	return chunk, nil
}

// chatEleAgentStream Ele Agent 流式后端代理
func (s *ChatProxyService) chatEleAgentStream(ctx context.Context, req ChatRequest, w io.Writer) (*llm.Usage, error) {
	subProvider, subModel, err := parseEleAgentModel(req.Model)
	if err != nil {
		s.logger.Error("解析 Ele Agent 模型名失败", zap.String("model", req.Model), zap.Error(err))
		return nil, err
	}
	s.logger.Debug("Ele Agent 流式代理",
		zap.String("subProvider", subProvider),
		zap.String("subModel", subModel),
		zap.Int("messages", len(req.Messages)),
	)

	credential, err := s.eleAgentModelService.GetCredentialForRequest(ctx, subProvider, subModel)
	if err != nil {
		s.logger.Error("获取 Ele Agent 模型凭据失败", zap.String("subProvider", subProvider), zap.String("subModel", subModel), zap.Error(err))
		return nil, fmt.Errorf("Ele Agent 模型未配置: %w", err)
	}
	s.logger.Debug("Ele Agent 凭据已获取",
		zap.String("subProvider", subProvider),
		zap.String("subModel", subModel),
		zap.String("baseURL", credential.BaseURL),
		zap.Int("apiKey_len", len(credential.APIKey)),
	)

	client, err := s.clientFactory.CreateByProtocol(credential.Protocol, credential.APIKey, credential.BaseURL)
	if err != nil {
		s.logger.Error("创建 Ele Agent 上游客户端失败", zap.String("subProvider", subProvider), zap.String("subModel", subModel), zap.String("protocol", credential.Protocol), zap.Error(err))
		return nil, fmt.Errorf("创建 Ele Agent 上游客户端失败: %w", err)
	}

	// 将 Ele Agent 模型名替换为真实子模型名，同时透传温度、top_p、thinking 等扩展字段
	req.Model = subModel
	// 部分模型（如 Kimi 的 coding 系列）要求 temperature 只能为 1
	req.Temperature = normalizeEleAgentTemperature(subProvider, subModel, req.Temperature)
	llmReq := toLLMChatRequest(req, req.Messages)
	llmReq.Stream = true

	var chunkChan <-chan llm.ChatChunk
	err = callWithUpstreamRetry(ctx, s.maxRetries, func() error {
		var cerr error
		chunkChan, cerr = client.ChatStream(ctx, llmReq)
		return cerr
	})
	if err != nil {
		s.logger.Error("Ele Agent 上游流式调用失败", zap.String("subProvider", subProvider), zap.String("subModel", subModel), zap.Error(err))
		return nil, friendlyModelCallError("Ele Agent 流式模型调用失败", err, s.logger)
	}

	var usage *llm.Usage
	chunkCount := 0
	contentChunkCount := 0
	totalDeltaLen := 0
	totalReasoningLen := 0
	var lastFinishReason string
	for chunk := range chunkChan {
		chunkCount++
		data, _ := json.Marshal(toOpenAIStreamChunk(chunk))
		s.logger.Debug("Ele Agent 转发 chunk",
			zap.Int("index", chunkCount),
			zap.Int("delta_len", len(chunk.Delta)),
			zap.Int("reasoning_len", len(chunk.ReasoningContent)),
			zap.Int("data_len", len(data)),
		)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if chunk.Delta != "" || chunk.ReasoningContent != "" {
			contentChunkCount++
			totalDeltaLen += len(chunk.Delta)
			totalReasoningLen += len(chunk.ReasoningContent)
		}
		if chunk.FinishReason != "" {
			lastFinishReason = chunk.FinishReason
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if flusher, ok := w.(httpFlusher); ok {
			_ = flusher.Flush()
		}
	}

	// Ele Agent 代理模式下同样检测空响应
	if contentChunkCount == 0 {
		s.logger.Error("Ele Agent 上游流式响应未返回任何有效内容",
			zap.String("sub_provider", subProvider),
			zap.String("sub_model", subModel),
			zap.String("base_url", credential.BaseURL),
			zap.Int("chunks", chunkCount),
			zap.Int("total_delta_len", totalDeltaLen),
			zap.Int("total_reasoning_len", totalReasoningLen),
			zap.Any("usage", usage),
			zap.String("finish_reason", lastFinishReason),
			zap.Int("messages", len(req.Messages)),
			zap.String("request_summary", summarizeMessages(req.Messages, 500)),
		)
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	s.logger.Debug("Ele Agent 流式代理完成", zap.Int("chunks", chunkCount), zap.Int("content_chunks", contentChunkCount), zap.Int("total_delta_len", totalDeltaLen), zap.Int("total_reasoning_len", totalReasoningLen), zap.Any("usage", usage))

	return usage, nil
}

// normalizeEleAgentTemperature 根据子平台模型特性规范化 temperature。
// 部分上游模型（如 Kimi 的 coding / K3 系列）要求 temperature 必须为 1，否则返回 400
// （`invalid temperature: only 1 is allowed for this model`，k3 已实测确认）。
// 后续若管理员后台支持模型级参数配置，可替换为从配置读取。
func normalizeEleAgentTemperature(subProvider, subModel string, temperature float64) float64 {
	if !strings.EqualFold(subProvider, "kimi") {
		return temperature
	}
	m := strings.ToLower(subModel)
	// kimi-for-coding(-highspeed) / k3 / kimi-k3* 均强制 temperature=1
	if strings.Contains(m, "kimi-for-coding") || m == "k3" || strings.HasPrefix(m, "kimi-k3") {
		return 1.0
	}
	return temperature
}

// parseEleAgentModel 解析 Ele Agent 模型名中的子平台信息
// 约定格式："subProvider/subModel"，例如 "qwen/Qwen/Qwen3-8B"
// 按第一个 "/" 分割，subProvider 允许任意平台标识（大小写敏感）。
func parseEleAgentModel(model string) (subProvider, subModel string, err error) {
	idx := strings.Index(model, "/")
	if idx <= 0 {
		return "", "", errors.New("Ele Agent 模型名格式错误，应为 subProvider/subModel")
	}
	return model[:idx], model[idx+1:], nil
}

// httpFlusher 用于流式响应时刷新缓冲区
type httpFlusher interface {
	Flush() error
}

// summarizeMessages 将消息列表序列化并截断，用于日志中快速查看请求上下文
func summarizeMessages(messages []llm.Message, maxLen int) string {
	if len(messages) == 0 {
		return "(empty)"
	}
	b, err := json.Marshal(messages)
	if err != nil {
		return fmt.Sprintf("(marshal error: %v)", err)
	}
	s := string(b)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// maskKeyID 对 Key ID 做脱敏处理，避免日志泄露完整凭证标识
func maskKeyID(keyID string) string {
	if keyID == "" {
		return ""
	}
	if len(keyID) <= 4 {
		return "****"
	}
	return keyID[:4] + "****"
}

// openAIStreamChunk 是网关对外暴露的 OpenAI 兼容 SSE 数据格式
type openAIStreamChunk struct {
	ID      string               `json:"id,omitempty"`
	Object  string               `json:"object,omitempty"`
	Created int64                `json:"created,omitempty"`
	Choices []openAIStreamChoice `json:"choices"`
}

type openAIStreamChoice struct {
	Index        int                `json:"index"`
	Delta        openAIDeltaContent `json:"delta"`
	FinishReason *string            `json:"finish_reason"`
}

type openAIDeltaContent struct {
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Role             string `json:"role,omitempty"`
}

// supportsChat 判断当前 provider/model 是否支持文字对话。
// 仅 Ele Agent 以管理员配置的 supports_chat 为准；BYOK 厂商不做拦截（返回 true）。
// 模型未配置或解析失败时放行，由下游 GetCredential 给出统一的「未找到配置」报错。
func (s *ChatProxyService) supportsChat(provider, modelName string) bool {
	if llm.Provider(provider) != llm.ProviderEleAgent {
		return true
	}
	subProvider, subModel, err := parseEleAgentModel(modelName)
	if err != nil || s.eleAgentModelService == nil {
		return true
	}
	if !s.eleAgentModelService.HasModel(subProvider, subModel) {
		return true
	}
	return s.eleAgentModelService.GetModelChatSupport(subProvider, subModel)
}

// supportsVision 判断当前 provider/model 是否支持图片理解。
// Ele Agent 以管理员配置的 supports_vision 为准；直接调用的厂商按常见命名规则兜底。
// 未知自定义模型保守返回 false，避免非视觉模型收到 image_url 后空响应。
func (s *ChatProxyService) supportsVision(provider, modelName string) bool {
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

// modelSupportsVision 根据常见命名规则判断模型是否可能支持视觉。
// 返回 (supported, known)，unknown 时不应做强制拦截。
func modelSupportsVision(provider, modelName string) (supported bool, known bool) {
	m := strings.ToLower(modelName)
	switch provider {
	case "openai":
		return strings.Contains(m, "vision") || strings.Contains(m, "gpt-4o") || strings.Contains(m, "gpt-4-turbo"), true
	case "moonshot":
		return strings.Contains(m, "vision") || strings.Contains(m, "kimi-k2.5") || strings.Contains(m, "kimi-k2.6") || strings.Contains(m, "kimi-k2.7"), true
	case "qwen":
		return strings.Contains(m, "vision") || strings.Contains(m, "qvq") || strings.Contains(m, "vl"), true
	case "deepseek":
		return false, true
	}
	return false, false
}

// hasImageContent 判断消息列表中是否包含图片内容
func hasImageContent(messages []llm.Message) bool {
	for _, msg := range messages {
		parts, ok := msg.Content.([]interface{})
		if !ok {
			continue
		}
		for _, p := range parts {
			part, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if part["type"] == "image_url" {
				return true
			}
		}
	}
	return false
}

// normalizeMessageContents 将 content parts 中的 file 文本降级为纯字符串，
// 以兼容大多数非 VLM 模型；在 supportsVision 为 true 时保留 image_url 供视觉模型使用，
// 否则拒绝图片内容，避免非视觉模型将提示文本透传进对话。
// 同时过滤掉 content 为空的 assistant 消息，避免 Kimi 等厂商返回 400。
func normalizeMessageContents(messages []llm.Message, supportsVision bool) ([]llm.Message, error) {
	filtered := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		// Kimi 等厂商要求 assistant 消息 content 不能为空
		if msg.Role == "assistant" {
			if content, ok := msg.Content.(string); ok && strings.TrimSpace(content) == "" {
				continue
			}
		}
		filtered = append(filtered, msg)
	}

	result := make([]llm.Message, len(filtered))
	for i, msg := range filtered {
		content := msg.Content
		parts, ok := content.([]interface{})
		if !ok {
			result[i] = msg
			continue
		}

		var textParts []string
		var otherParts []interface{}
		for _, p := range parts {
			part, ok := p.(map[string]interface{})
			if !ok {
				otherParts = append(otherParts, p)
				continue
			}
			partType, _ := part["type"].(string)
			if partType == "text" {
				if t, ok := part["text"].(string); ok {
					textParts = append(textParts, t)
				}
			} else if partType == "file" {
				// 文本类文件直接提取 text 字段拼接到文本中
				file, ok := part["file"].(map[string]interface{})
				if !ok {
					continue
				}
				name, _ := file["name"].(string)
				if t, ok := file["text"].(string); ok && t != "" {
					if name != "" {
						textParts = append(textParts, fmt.Sprintf("【文件：%s】\n%s", name, t))
					} else {
						textParts = append(textParts, t)
					}
				}
				// 二进制文件 data URL 暂不上游，避免非 VLM 模型报错
				if _, ok := file["data"].(string); ok {
					if name != "" {
						textParts = append(textParts, fmt.Sprintf("【文件：%s】（二进制文件，当前模型可能无法直接解析）", name))
					}
				}
			} else if partType == "image_url" {
				if supportsVision {
					// 只保留 url 字段，去掉 detail 等扩展字段，避免部分厂商（如 Kimi）不识别 detail 而返回空内容
					imageURL, ok := part["image_url"].(map[string]interface{})
					if ok {
						url, _ := imageURL["url"].(string)
						otherParts = append(otherParts, map[string]interface{}{
							"type":      "image_url",
							"image_url": map[string]interface{}{"url": url},
						})
					} else {
						otherParts = append(otherParts, p)
					}
				} else {
					// 非视觉模型：拒绝图片内容，不将其作为文本占位透传进对话
					return nil, errors.New("当前模型不支持图片理解，请切换到视觉模型后重试")
				}
			} else {
				otherParts = append(otherParts, p)
			}
		}

		if len(textParts) > 0 && len(otherParts) == 0 {
			// 只有文本/文件内容，直接降级为字符串，并对超长上下文做截断保护
			result[i] = llm.Message{Role: msg.Role, Content: truncateContextText(strings.Join(textParts, "\n\n"))}
		} else if len(textParts) > 0 {
			// 混合内容（如图片+文字）：将图片等非文本 parts 放在文本之前，
			// 与 OpenAI / Kimi 官方示例顺序保持一致，提升多模态模型理解效果
			newParts := make([]interface{}, 0, len(otherParts)+1)
			newParts = append(newParts, otherParts...)
			newParts = append(newParts, map[string]interface{}{"type": "text", "text": truncateContextText(strings.Join(textParts, "\n\n"))})
			result[i] = llm.Message{Role: msg.Role, Content: newParts}
		} else {
			result[i] = msg
		}
	}
	return result, nil
}

// countFileContent 统计消息列表中 file 类型 content part 的数量
func countFileContent(messages []llm.Message) int {
	count := 0
	for _, msg := range messages {
		parts, ok := msg.Content.([]interface{})
		if !ok {
			continue
		}
		for _, p := range parts {
			part, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if partType, _ := part["type"].(string); partType == "file" {
				count++
			}
		}
	}
	return count
}

// totalTextRuneLen 统计消息列表中纯文本内容的总长度
func totalTextRuneLen(messages []llm.Message) int {
	count := 0
	for _, msg := range messages {
		switch v := msg.Content.(type) {
		case string:
			count += utf8.RuneCountInString(v)
		case []interface{}:
			for _, p := range v {
				part, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				if partType, _ := part["type"].(string); partType == "text" {
					if t, ok := part["text"].(string); ok {
						count += utf8.RuneCountInString(t)
					}
				}
			}
		}
	}
	return count
}

// truncateContextText 对超长文本上下文做截断，避免撑爆模型上下文窗口导致空回复
func truncateContextText(s string) string {
	if utf8.RuneCountInString(s) <= maxContextRuneLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxContextRuneLen-20]) + "\n\n[...内容过长，已截断...]"
}

// countImageContent 统计消息列表中 image_url 类型 content part 的数量
func countImageContent(messages []llm.Message) int {
	count := 0
	for _, msg := range messages {
		parts, ok := msg.Content.([]interface{})
		if !ok {
			continue
		}
		for _, p := range parts {
			part, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if partType, _ := part["type"].(string); partType == "image_url" {
				count++
			}
		}
	}
	return count
}

// toOpenAIStreamChunk 将内部 llm.ChatChunk 转换为 OpenAI 兼容 SSE 格式
func toOpenAIStreamChunk(chunk llm.ChatChunk) openAIStreamChunk {
	var finishReason *string
	if chunk.FinishReason != "" {
		finishReason = &chunk.FinishReason
	}
	return openAIStreamChunk{
		ID:      "chatcmpl_eleball",
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Choices: []openAIStreamChoice{
			{
				Index: 0,
				Delta: openAIDeltaContent{
					Content:          chunk.Delta,
					ReasoningContent: chunk.ReasoningContent,
				},
				FinishReason: finishReason,
			},
		},
	}
}
