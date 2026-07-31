package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ChatHandler 对话代理处理器
type ChatHandler struct {
	chatService    *service.ChatProxyService
	billingService *service.BillingService
	logger         *zap.Logger
}

// NewChatHandler 创建处理器
func NewChatHandler(chatService *service.ChatProxyService, billingService *service.BillingService, logger *zap.Logger) *ChatHandler {
	return &ChatHandler{
		chatService:    chatService,
		billingService: billingService,
		logger:         logger,
	}
}

// ChatCompletion 对话代理接口，兼容 OpenAI 格式
// 根据 req.Stream 区分流式（SSE）与非流式响应。
func (h *ChatHandler) ChatCompletion(c *gin.Context) {
	var req service.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	userID, _ := c.Get("user_id")

	// 触发对话时刷新用户活跃时间，用于 DAU 统计
	h.chatService.TouchUserActive(userID.(string))

	// 记录原始模型名（Ele Agent 模型名在 chatService 内部会被替换为子平台模型名，扣费时需用原始名匹配配置）
	originalModel := req.Model

	// Ele Agent 付费模型调用前校验余额；余额为负则拒绝调用
	// claw 本地网关注入 nil billing（本地不计费，计费转云端 api.eleball.cn），此时跳过余额校验。
	currency := service.CurrencyDanwan
	if req.Currency == service.CurrencyElegant {
		currency = service.CurrencyElegant
	}
	if h.billingService != nil {
		if err := h.billingService.CheckBalance(userID.(string), req.Provider, originalModel, currency); err != nil {
			c.JSON(http.StatusPaymentRequired, gin.H{"code": 4002, "message": err.Error()})
			return
		}
	}

	if req.Stream {
		// 流式响应：SSE
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)

		usage, err := h.chatService.ChatStream(c.Request.Context(), &req, c.Writer)
		if err != nil {
			// SSE 一旦开始无法改 HTTP 状态码，只能在流内输出错误事件
			c.Writer.WriteString("event: error\ndata: " + err.Error() + "\n\n")
			c.Writer.Flush()
			return
		}

		// 流式计费扣减（claw 注入 nil billing 时跳过，本地不计费）
		if usage != nil && h.billingService != nil {
			if err := h.billingService.Deduct(userID.(string), req.Provider, originalModel, currency, usage); err != nil {
				h.logger.Warn("流式对话扣费失败", zap.Error(err), zap.String("user_id", userID.(string)))
			}
		}
		return
	}

	// 非流式响应
	chunk, err := h.chatService.Chat(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": err.Error()})
		return
	}

	// 计费扣减（claw 注入 nil billing 时跳过，本地不计费）
	if chunk.Usage != nil && h.billingService != nil {
		if err := h.billingService.Deduct(userID.(string), req.Provider, originalModel, currency, chunk.Usage); err != nil {
			h.logger.Warn("非流式对话扣费失败", zap.Error(err), zap.String("user_id", userID.(string)))
		}
	}

	// 非流式响应：标准 OpenAI 兼容结构（choices[0].message），替代旧 {code,message,data} 私有信封，
	// 便于标准 OpenAI 客户端直连 /v1/chat/completions
	c.JSON(http.StatusOK, service.ToOpenAICompletion(*chunk, originalModel))
}
