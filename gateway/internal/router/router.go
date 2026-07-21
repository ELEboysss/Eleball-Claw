package router

import (
	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/handler"
	"github.com/eleball/gateway/internal/middleware"
	"github.com/eleball/gateway/internal/service"
	"github.com/eleball/gateway/pkg/util"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// NewRouter 创建并配置 Gin 路由
func NewRouter(
	cfg *config.AppConfig,
	log *zap.Logger,
	jwtUtil *util.JWTUtil,
	authHandler *handler.AuthHandler,
	chatHandler *handler.ChatHandler,
	billingHandler *handler.BillingHandler,
	syncHandler *handler.SyncHandler,
	paymentHandler *handler.PaymentHandler,
	adminHandler *handler.AdminHandler,
	adminKeyHandler *handler.AdminKeyHandler,
	adminEleAgentModelHandler *handler.AdminEleAgentModelHandler,
	adminSettingHandler *handler.AdminSettingHandler,
	publicSettingHandler *handler.PublicSettingHandler,
	agentHandler *handler.AgentHandler,
	withdrawalHandler *handler.WithdrawalHandler,
	eleAgentHandler *handler.EleAgentHandler,
	rechargePackageHandler *handler.RechargePackageHandler,
	cdkHandler *handler.CDKHandler,
	releaseHandler *handler.ReleaseHandler,
	sttHandler *handler.SttHandler,
	conversationHandler *handler.ConversationHandler,
	moduleHandler *handler.ModuleHandler,
	agentWorkflowHandler *handler.AgentWorkflowHandler,
	vipHandler *handler.VIPHandler,
	agentCredentialHandler *handler.AgentCredentialHandler,
	visualHandler *handler.VisualHandler,
) *gin.Engine {
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 管理后台前置闸门（Pre-Auth Gate）：release 模式 cookie Secure=true（生产经 nginx HTTPS 终止）
	// 内部从 cfg.AdminGate 构造，避免在 NewRouter 调用方（main.go 与测试）重复注入
	adminGateHandler := handler.NewAdminGateHandler(service.NewAdminGateService(&cfg.AdminGate, cfg.Server.Mode == "release"))

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())
	r.Use(middleware.Logger(log))
	// 测试模式禁用速率限制，避免测试并发触发 429
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
		c.JSON(200, gin.H{"code": 0, "message": "ok"})
	})

	v1 := r.Group("/v1")
	{
		// 公开接口
		v1.POST("/auth/register", authHandler.Register)
		v1.POST("/auth/login", authHandler.Login)
		v1.POST("/auth/refresh", authHandler.Refresh)
		v1.POST("/auth/email/otp/send", authHandler.SendEmailOTP)
		v1.POST("/auth/email/login", authHandler.EmailLogin)

		// 版本发布与下载（无需登录，供官网落地页与客户端更新使用）
		v1.GET("/releases/android", releaseHandler.GetAndroidManifest)
		v1.GET("/releases/android/download", releaseHandler.DownloadAndroid)

		// 需要认证
		auth := v1.Group("", middleware.JWTAuth(jwtUtil))
		{
			auth.GET("/auth/me", authHandler.Me)
			auth.POST("/chat/completions", chatHandler.ChatCompletion)
			auth.GET("/billing/balance", billingHandler.GetBalance)
			auth.GET("/billing/recharge-history", billingHandler.GetRechargeHistory)
			auth.POST("/sync/push", syncHandler.Push)
			auth.POST("/sync/pull", syncHandler.Pull)
			// 模型列表公开，无需登录即可在官网模型中心展示
			v1.GET("/eleagent/models", eleAgentHandler.ListModels)

			auth.GET("/eleagent/credentials", eleAgentHandler.GetCredentials)
			auth.POST("/stt", sttHandler.Transcribe)

			// 视觉生成（图片/视频）独立限流，避免影响文本对话
			visualReadLimiter := middleware.NewRateLimiter(180)  // GET 180/min
			visualWriteLimiter := middleware.NewRateLimiter(60)  // POST/PATCH/DELETE 60/min
			visual := auth.Group("/visual")
			visual.Use(middleware.RateLimit(visualReadLimiter, visualWriteLimiter))
			{
				visual.POST("/conversations", visualHandler.CreateConversation)
				visual.GET("/conversations", visualHandler.ListConversations)
				visual.GET("/conversations/:id", visualHandler.GetConversation)
				visual.PATCH("/conversations/:id", visualHandler.UpdateConversation)
				visual.DELETE("/conversations/:id", visualHandler.DeleteConversation)

				// 任务创建更严格：10/min；查询 30/min；取消 20/min
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

			auth.POST("/payment/wechat/prepay", paymentHandler.WechatPrepay)
			auth.POST("/payment/alipay/order", paymentHandler.AlipayOrder)
			auth.POST("/payment/alipay/precreate", paymentHandler.AlipayPrecreate)
			auth.GET("/orders/:id/status", paymentHandler.GetOrderStatus)
			auth.GET("/recharge/packages", rechargePackageHandler.ListUserPackages)
			auth.POST("/cdk/redeem", cdkHandler.Redeem)

			// VIP 会员
			auth.GET("/vip/plans", vipHandler.ListPlans)
			auth.GET("/vip/status", vipHandler.GetStatus)
			auth.POST("/vip/subscribe", vipHandler.Subscribe)

			// 公开配置（用户端展示用）
			auth.GET("/public/settings", publicSettingHandler.GetPublicSettings)

			// 对话历史（服务端明文存储）
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
		}

		// Agent 输出资源匿名代理下载（通过 resource_id 访问，不暴露磁盘路径）
		if cfg.Agent.Enabled {
			v1.GET("/agent/resources/:id", agentWorkflowHandler.GetResource)
		}

		// 视觉生成参考图/首帧图公网访问（上游厂商需直接拉取）
		v1.GET("/visual/files/:id", visualHandler.GetFile)

		// 支付异步通知（微信/支付宝回调，无需 JWT，由平台签名验证）
		v1.POST("/payment/wechat/notify", paymentHandler.WechatNotify)
		v1.POST("/payment/alipay/notify", paymentHandler.AlipayNotify)

		// Agent 市场（需登录）
		auth.GET("/agents", agentHandler.ListAgents)
		auth.GET("/agents/:id", agentHandler.GetAgent)
		auth.POST("/agents", agentHandler.CreateAgent)
		auth.POST("/agents/:id/purchase", agentHandler.PurchaseAgent)
		auth.POST("/agents/:id/active", agentHandler.ToggleAgentActive)
		auth.GET("/agents/:id/reviews", agentHandler.ListReviews)
		auth.POST("/agents/:id/reviews", agentHandler.CreateReview)
		auth.POST("/agents/:id/favorite", agentHandler.ToggleFavorite)
		auth.GET("/market/categories", agentHandler.GetCategories)
		auth.GET("/developer/account", agentHandler.GetDeveloperAccount)
		auth.GET("/space", agentHandler.GetUserSpace)
		auth.GET("/capabilities", agentHandler.GetCapabilities)

		// 插件自助注册（无需登录，需 auth_token）
		v1.POST("/market/modules/register", moduleHandler.RegisterModuleFromPlugin)

		// SKU 凭证管理（按用户隔离的 Cookie / API Key / Token 等）
		if agentCredentialHandler != nil {
			auth.GET("/agents/:id/credentials", agentCredentialHandler.List)
			auth.POST("/agents/:id/credentials", agentCredentialHandler.Save)
		}

		auth.POST("/developer/withdrawals", withdrawalHandler.ApplyWithdrawal)
		auth.GET("/developer/withdrawals", withdrawalHandler.ListMyWithdrawals)

		// 管理后台前置闸门端点（Pre-Auth Gate，无需登录，nginx 直接 proxy 到此）
		// 总是注册：nginx 静态配置 /admin/knock -> gateway，启用与否由 handler 内部 Enabled() 决定
		r.GET("/admin/knock", adminGateHandler.KnockPage)
		r.POST("/_admin_gate", adminGateHandler.Verify)
		r.GET("/_admin_gate_check", adminGateHandler.Check)

		// 管理后台接口（需管理员权限 + IP 白名单 + 运行时开关）。
		// 仅在 admin.enabled 为 true 时注册，生产环境默认关闭以防止管理后台被扫描/暴露。
		// 即使已注册，运行时也可通过 /_internal/admin/off 关闭开关，使所有 /v1/admin/* 返回 404。
		if cfg.Admin.Enabled {
			// 注意顺序：AdminGateEnforce（闸门 cookie）-> AdminSwitch（运行时 404）-> AdminAuth（JWT）-> IP 白名单。
			// 闸门最外：无 cookie 直接拦截，不暴露后续鉴权逻辑（纵深防御，即便 nginx 配错直连 gateway:8080 仍生效）。
			admin := v1.Group("/admin", adminGateHandler.Enforce(), middleware.AdminSwitch(), middleware.AdminAuth(jwtUtil), middleware.AdminIPWhitelist(&cfg.Admin))
			{
				// Dashboard
				admin.GET("/stats", adminHandler.GetStats)
				admin.GET("/stats/dau", adminHandler.GetDailyActive)
				admin.GET("/stats/token-usage", adminHandler.GetTokenUsage)
				admin.GET("/activities", adminHandler.GetRecentActivities)

				// 用户管理
				admin.GET("/users", adminHandler.ListUsers)
				admin.GET("/users/:id", adminHandler.GetUserDetail)
				admin.PATCH("/users/:id/status", adminHandler.UpdateUserStatus)
				admin.DELETE("/users/:id", adminHandler.DeleteUser)

				// 计费管理
				admin.GET("/billing/transactions", adminHandler.ListTransactions)
				admin.POST("/billing/recharge", adminHandler.Recharge)

				// ASR 额度管理
				admin.GET("/users/:id/asr-quota", adminHandler.GetUserAsrQuota)
				admin.PATCH("/users/:id/asr-quota", adminHandler.UpdateUserAsrQuota)

				// 订单管理
				admin.GET("/orders", adminHandler.ListOrders)
				admin.POST("/orders/:id/refund", adminHandler.RefundOrder)
				admin.POST("/orders/:id/confirm", adminHandler.ConfirmOrder)

				// VIP 会员管理
				admin.GET("/vip/plans", vipHandler.ListPlansAdmin)
				admin.POST("/vip/plans", vipHandler.CreatePlan)
				admin.PATCH("/vip/plans/:id", vipHandler.UpdatePlan)
				admin.DELETE("/vip/plans/:id", vipHandler.DeletePlan)
				admin.GET("/vip/subscriptions", vipHandler.ListSubscriptions)
				admin.POST("/users/:id/vip", vipHandler.GrantVIP)

				// 提现审核
				admin.GET("/withdrawals", withdrawalHandler.ListAllWithdrawals)
				admin.POST("/withdrawals/:id/approve", withdrawalHandler.ApproveWithdrawal)
				admin.POST("/withdrawals/:id/reject", withdrawalHandler.RejectWithdrawal)

				// 秘技审核
				admin.GET("/agents", agentHandler.ListAgentsForAdmin)
				admin.GET("/agents/:id/dependencies", agentHandler.GetAgentDependencies)
				admin.POST("/agents/:id/approve", agentHandler.ReviewAgent)
				admin.POST("/agents/:id/reject", agentHandler.ReviewAgent)
				admin.POST("/agents/:id/delist", agentHandler.DelistAgent)

				// API Key 管理
				admin.GET("/keys/providers", adminKeyHandler.ListProviders)
				admin.GET("/keys", adminKeyHandler.ListKeys)
				admin.POST("/keys", adminKeyHandler.CreateKey)
				admin.GET("/keys/:id", adminKeyHandler.GetKey)
				admin.PATCH("/keys/:id", adminKeyHandler.UpdateKey)
				admin.DELETE("/keys/:id", adminKeyHandler.DeleteKey)
				admin.POST("/keys/:id/test", adminKeyHandler.TestKey)
				admin.POST("/keys/reset-quota", adminKeyHandler.ResetQuota)

				// Ele Agent 模型配置管理
				admin.GET("/eleagent/models", adminEleAgentModelHandler.ListConfigs)
				admin.POST("/eleagent/models", adminEleAgentModelHandler.CreateConfig)
				admin.GET("/eleagent/models/export", adminEleAgentModelHandler.ExportConfigs)
				admin.POST("/eleagent/models/import", adminEleAgentModelHandler.ImportConfigs)
				admin.GET("/eleagent/models/:id", adminEleAgentModelHandler.GetConfig)
				admin.PATCH("/eleagent/models/:id", adminEleAgentModelHandler.UpdateConfig)
				admin.POST("/eleagent/models/:id/rotate-key", adminEleAgentModelHandler.RotateAPIKey)
				admin.DELETE("/eleagent/models/:id", adminEleAgentModelHandler.DeleteConfig)

				// 系统设置
				admin.GET("/settings", adminSettingHandler.GetSettings)
				admin.PUT("/settings", adminSettingHandler.UpdateSettings)

				// 充值套餐配置
				admin.GET("/recharge/packages", rechargePackageHandler.ListAdminPackages)
				admin.POST("/recharge/packages", rechargePackageHandler.CreatePackage)
				admin.PATCH("/recharge/packages/:id", rechargePackageHandler.UpdatePackage)
				admin.DELETE("/recharge/packages/:id", rechargePackageHandler.DeletePackage)

				// 兑换码管理
				admin.POST("/cdk/batch", cdkHandler.BatchGenerate)
				admin.GET("/cdk", cdkHandler.ListCDKs)
				admin.DELETE("/cdk/:id", cdkHandler.DeleteCDK)

				// 集市模块与动态驱动管理
				admin.GET("/modules", moduleHandler.ListModules)
				admin.GET("/modules/:id", moduleHandler.GetModule)
				admin.POST("/modules", moduleHandler.RegisterModule)
				admin.DELETE("/modules/:id", moduleHandler.UnregisterModule)
				admin.POST("/modules/:id/refresh", moduleHandler.RefreshModule)
				admin.POST("/modules/rescan", moduleHandler.RescanMarketplace)
				admin.GET("/drivers", moduleHandler.ListDrivers)
				admin.POST("/drivers", moduleHandler.RegisterDriver)
				admin.DELETE("/drivers/:id", moduleHandler.UnregisterDriver)
			}
		}
	}

	// 本地内部接口：运行时动态控制 /v1/admin 开关。
	// 仅允许 127.0.0.1 / ::1 访问，避免外部网络直接操作。
	adminSwitchHandler := handler.NewAdminSwitchHandler()
	internal := r.Group("/_internal", middleware.LocalhostOnly())
	{
		internal.POST("/admin/:action", adminSwitchHandler.Toggle)
	}

	return r
}
