package router

import (
	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/handler"
	"github.com/eleball/gateway/internal/middleware"
	"github.com/eleball/gateway/pkg/util"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// NewClawRouter 创建 Eleball-claw 本地网关路由（裁剪自 NewRouter）。
//
// 与云端 NewRouter 的差异（§B.3 路由改造）：
//   - 删除 /v1/admin/* 全部管理后台路由，改为 /v1/claw-console/* 本地控制台（最小集，P3 扩充）。
//   - 删除计费/支付/订单/充值套餐/CDK/VIP 套餐/提现/admin gate 路由（claw 本地不计费、无交易、无前置闸门）。
//   - 删除 /agents/:id/purchase 与 /developer/withdrawals（购买与提现走云端 eleball.cn）。
//   - 保留对话/视觉/Agent 工作流/集市模块/对话历史/同步/模型列表/STT/凭证/秘技审核提交。
//
// claw 本地网关注入 nil billing，chat/agent/visual/eleagent 流程均 nil 检查后跳过计费（本地不计费，
// Ele Agent 模型经 BaseURL 转发至云端 api.eleball.cn/v1，由云端账户计费）。
func NewClawRouter(
	cfg *config.AppConfig,
	log *zap.Logger,
	jwtUtil *util.JWTUtil,
	authHandler *handler.AuthHandler,
	chatHandler *handler.ChatHandler,
	syncHandler *handler.SyncHandler,
	eleAgentHandler *handler.EleAgentHandler,
	sttHandler *handler.SttHandler,
	conversationHandler *handler.ConversationHandler,
	moduleHandler *handler.ModuleHandler,
	agentWorkflowHandler *handler.AgentWorkflowHandler,
	agentHandler *handler.AgentHandler,
	agentCredentialHandler *handler.AgentCredentialHandler,
	visualHandler *handler.VisualHandler,
	publicSettingHandler *handler.PublicSettingHandler,
	releaseHandler *handler.ReleaseHandler,
) *gin.Engine {
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())
	r.Use(middleware.Logger(log))
	if cfg.Server.Mode != "test" {
		readLimit := int(float64(cfg.RateLimit.RequestsPerMinute) * cfg.RateLimit.ReadMultiplier)
		if readLimit < cfg.RateLimit.RequestsPerMinute {
			readLimit = cfg.RateLimit.RequestsPerMinute
		}
		r.Use(middleware.RateLimit(
			middleware.NewRateLimiter(readLimit),
			middleware.NewRateLimiter(cfg.RateLimit.RequestsPerMinute),
		))
	}

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"code": 0, "message": "ok", "data": gin.H{"node": "eleball-claw"}})
	})

	v1 := r.Group("/v1")
	{
		// 公开接口：登录/注册。P1 走本地用户表；P2 改为转发云端统一账户（见 claw-implementation-plan §J）。
		v1.POST("/auth/register", authHandler.Register)
		v1.POST("/auth/login", authHandler.Login)
		v1.POST("/auth/refresh", authHandler.Refresh)
		v1.POST("/auth/email/otp/send", authHandler.SendEmailOTP)
		v1.POST("/auth/email/login", authHandler.EmailLogin)

		// 版本发布与下载（无需登录）
		v1.GET("/releases/android", releaseHandler.GetAndroidManifest)
		v1.GET("/releases/android/download", releaseHandler.DownloadAndroid)

		// 模型列表公开（claw 模型页展示本地化模型配置）
		v1.GET("/eleagent/models", eleAgentHandler.ListModels)

		// 需要认证
		auth := v1.Group("", middleware.JWTAuth(jwtUtil))
		{
			auth.GET("/auth/me", authHandler.Me)
			auth.POST("/chat/completions", chatHandler.ChatCompletion) // billing=nil，本地不计费
			auth.POST("/sync/push", syncHandler.Push)
			auth.POST("/sync/pull", syncHandler.Pull)

			auth.GET("/eleagent/credentials", eleAgentHandler.GetCredentials)
			auth.POST("/stt", sttHandler.Transcribe)

			// 视觉生成（图片/视频）独立限流
			visualReadLimiter := middleware.NewRateLimiter(180)
			visualWriteLimiter := middleware.NewRateLimiter(60)
			visual := auth.Group("/visual")
			visual.Use(middleware.RateLimit(visualReadLimiter, visualWriteLimiter))
			{
				visual.POST("/conversations", visualHandler.CreateConversation)
				visual.GET("/conversations", visualHandler.ListConversations)
				visual.GET("/conversations/:id", visualHandler.GetConversation)
				visual.PATCH("/conversations/:id", visualHandler.UpdateConversation)
				visual.DELETE("/conversations/:id", visualHandler.DeleteConversation)
				visual.POST("/generations", middleware.RateLimit(
					middleware.NewRateLimiter(30),
					middleware.NewRateLimiter(10),
				), visualHandler.CreateTask)
				visual.GET("/generations/:id", middleware.RateLimit(
					middleware.NewRateLimiter(30),
					middleware.NewRateLimiter(30),
				), visualHandler.GetTask)
				visual.POST("/generations/:id/cancel", middleware.RateLimit(
					middleware.NewRateLimiter(30),
					middleware.NewRateLimiter(20),
				), visualHandler.CancelTask)
				visual.POST("/upload", visualHandler.UploadFile)
			}

			// 公开配置（用户端展示用）
			auth.GET("/public/settings", publicSettingHandler.GetPublicSettings)

			// 对话历史（本地存储）
			auth.GET("/conversations", conversationHandler.ListConversations)
			auth.POST("/conversations", conversationHandler.CreateConversation)
			auth.GET("/conversations/:id", conversationHandler.GetConversation)
			auth.PATCH("/conversations/:id", conversationHandler.UpdateConversation)
			auth.DELETE("/conversations/:id", conversationHandler.DeleteConversation)
			auth.GET("/conversations/:id/messages", conversationHandler.ListMessages)
			auth.POST("/conversations/:id/messages", conversationHandler.CreateMessage)

			// Agent 工作流
			if cfg.Agent.Enabled {
				auth.POST("/agent/execute", agentWorkflowHandler.Execute)
				auth.GET("/agent/search-providers", agentWorkflowHandler.ListSearchProviders)
				auth.GET("/agent/sessions", agentWorkflowHandler.ListSessions)
				auth.DELETE("/agent/sessions", agentWorkflowHandler.DeleteSessions)
				auth.GET("/agent/sessions/:id", agentWorkflowHandler.GetSession)
				auth.DELETE("/agent/sessions/:id", agentWorkflowHandler.DeleteSession)
			}

			// 秘技集市（本地 + 云端拉取合并展示，P2 实现登录态拉云端）
			// 注意：不含 /agents/:id/purchase（购买走云端 eleball.cn）与 /developer/withdrawals（提现走云端）
			auth.GET("/agents", agentHandler.ListAgents)
			auth.GET("/agents/:id", agentHandler.GetAgent)
			auth.POST("/agents", agentHandler.CreateAgent)
			auth.POST("/agents/:id/active", agentHandler.ToggleAgentActive)
			auth.GET("/agents/:id/reviews", agentHandler.ListReviews)
			auth.POST("/agents/:id/reviews", agentHandler.CreateReview)
			auth.POST("/agents/:id/favorite", agentHandler.ToggleFavorite)
			auth.GET("/market/categories", agentHandler.GetCategories)
			auth.GET("/developer/account", agentHandler.GetDeveloperAccount)
			auth.GET("/space", agentHandler.GetUserSpace)
			auth.GET("/capabilities", agentHandler.GetCapabilities)

			// SKU 凭证管理
			if agentCredentialHandler != nil {
				auth.GET("/agents/:id/credentials", agentCredentialHandler.List)
				auth.POST("/agents/:id/credentials", agentCredentialHandler.Save)
			}

			// 本地控制台（替代云端 /v1/admin/*）：P3 扩充，仅 JWT 用户登录（无 admin gate / admin auth）。
			// 复用既有 handler，端点挂到 /claw-console/* 下。
			console := auth.Group("/claw-console")
			{
				console.GET("/health", func(c *gin.Context) {
					c.JSON(200, gin.H{"code": 0, "message": "ok", "data": gin.H{"node": "eleball-claw", "mode": cfg.Server.Mode}})
				})

				// 本地集市模块管理（扫描本地 + 已安装；提交审核走云端）
				console.GET("/modules", moduleHandler.ListModules)
				console.POST("/modules", moduleHandler.RegisterModule)
				console.DELETE("/modules/:id", moduleHandler.UnregisterModule)
				console.POST("/modules/:id/refresh", moduleHandler.RefreshModule)
				console.POST("/modules/rescan", moduleHandler.RescanMarketplace)

				// 本地动态驱动管理
				console.GET("/drivers", moduleHandler.ListDrivers)
				console.POST("/drivers", moduleHandler.RegisterDriver)
				console.DELETE("/drivers/:id", moduleHandler.UnregisterDriver)

				// 本地模型配置（只读列表，复用 EleAgent 模型列表；CRUD 在云端 admin-web）
				console.GET("/eleagent/models", eleAgentHandler.ListModels)

				// 本地设置（公开配置只读；写设置在云端 admin）
				console.GET("/settings", publicSettingHandler.GetPublicSettings)
			}
		}

		// Agent 输出资源匿名代理下载
		if cfg.Agent.Enabled {
			v1.GET("/agent/resources/:id", agentWorkflowHandler.GetResource)
		}

		// 视觉生成参考图/首帧图公网访问（上游厂商需直接拉取）
		v1.GET("/visual/files/:id", visualHandler.GetFile)

		// 插件自助注册（无需登录，需 auth_token）—本地秘技提交云端审核走此接口转发云端（P4）
		v1.POST("/market/modules/register", moduleHandler.RegisterModuleFromPlugin)
	}

	// 本地内部接口（仅 127.0.0.1）：P1 不暴露 admin 开关，保留 LocalhostOnly 组以便后续扩展
	internal := r.Group("/_internal", middleware.LocalhostOnly())
	{
		internal.GET("/claw/ping", func(c *gin.Context) {
			c.JSON(200, gin.H{"code": 0, "message": "pong"})
		})
	}

	return r
}
