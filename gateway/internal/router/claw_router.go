package router

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/eleball/gateway/admin-web"
	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/handler"
	"github.com/eleball/gateway/internal/middleware"
	"github.com/eleball/gateway/internal/service"
	"github.com/eleball/gateway/pkg/util"
	"github.com/eleball/gateway/web"
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
	chatHandler *handler.ChatHandler,
	syncHandler *handler.SyncHandler,
	eleAgentHandler *handler.EleAgentHandler,
	conversationHandler *handler.ConversationHandler,
	moduleHandler *handler.ModuleHandler,
	agentWorkflowHandler *handler.AgentWorkflowHandler,
	agentHandler *handler.AgentHandler,
	agentCredentialHandler *handler.AgentCredentialHandler,
	visualHandler *handler.VisualHandler,
	publicSettingHandler *handler.PublicSettingHandler,
	releaseHandler *handler.ReleaseHandler,
	clawConsoleHandler *handler.ClawConsoleHandler,
	cloudAccount *service.CloudAccountService,
	adminEleAgentModelHandler *handler.AdminEleAgentModelHandler,
	adminSettingHandler *handler.AdminSettingHandler,
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
		// 认证（登录/注册/刷新/邮箱OTP/me）统一走云端 eleball.cn 账户（claw web 的 authApi 直连云端）；
		// claw 本地不再提供 /v1/auth/* 路由与本地 users 账户体系（见 claw-cloud-auth-unify 计划）。
		// 本地接口的 JWTAuth 接受云端签发的 JWT（部署时 JWT_SECRET 与云端一致）。

		// 版本发布与下载（无需登录）
		v1.GET("/releases/android", releaseHandler.GetAndroidManifest)
		v1.GET("/releases/android/download", releaseHandler.DownloadAndroid)

		// 模型列表公开（claw 模型页展示本地化模型配置）
		v1.GET("/eleagent/models", eleAgentHandler.ListModels)

		// 需要认证（本地密钥验签失败时回退云端 /auth/me 验证，兼容安装脚本生成的随机本地密钥）
		auth := v1.Group("", middleware.JWTAuthCloudFallback(jwtUtil, cloudAccount.ValidateToken))
		{
			auth.POST("/chat/completions", chatHandler.ChatCompletion) // billing=nil，本地不计费
			auth.POST("/sync/push", syncHandler.Push)
			auth.POST("/sync/pull", syncHandler.Pull)

			auth.GET("/eleagent/credentials", eleAgentHandler.GetCredentials)
			// STT 已下沉为 marketplace/stt 模块（百度 ASR key 作模块凭证），不再暴露内置 /stt 端点。

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

				// 本地 token 用量统计（P3 细化，替代云端 DAU/收入）
				console.GET("/stats", clawConsoleHandler.GetStats)

				// 本地集市模块管理（扫描本地 + 已安装；提交审核走云端）
				console.GET("/modules", moduleHandler.ListModules)
				console.POST("/modules", moduleHandler.RegisterModule)
				console.DELETE("/modules/:id", moduleHandler.UnregisterModule)
				console.POST("/modules/:id/refresh", moduleHandler.RefreshModule)
				console.POST("/modules/rescan", moduleHandler.RescanMarketplace)
				// P4：安装云端已购模块到本地（拉镜像+签名校验+激活）
				console.POST("/modules/install", moduleHandler.InstallModule)
				// P4：本地秘技提交云端审核（转发云端 register）
				console.POST("/modules/submit-review", moduleHandler.SubmitForReview)

				// 本地动态驱动管理
				console.GET("/drivers", moduleHandler.ListDrivers)
				console.POST("/drivers", moduleHandler.RegisterDriver)
				console.DELETE("/drivers/:id", moduleHandler.UnregisterDriver)

				// 本地模型配置（BYOK 增删改 + Ele Agent 云端代理接入，复用云端管理端 handler）
				console.GET("/eleagent/models", adminEleAgentModelHandler.ListConfigs)
				console.POST("/eleagent/models", adminEleAgentModelHandler.CreateConfig)
				console.GET("/eleagent/models/:id", adminEleAgentModelHandler.GetConfig)
				console.PATCH("/eleagent/models/:id", adminEleAgentModelHandler.UpdateConfig)
				console.POST("/eleagent/models/:id/rotate-key", adminEleAgentModelHandler.RotateAPIKey)
				console.DELETE("/eleagent/models/:id", adminEleAgentModelHandler.DeleteConfig)

				// 本地设置（读写；页面已裁剪为本地生效项，云端运营类设置不在此暴露）
				console.GET("/settings", adminSettingHandler.GetSettings)
				console.PUT("/settings", adminSettingHandler.UpdateSettings)
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

	// web / admin-web 静态文件服务（plan §A.3 单文件分发，前端随二进制）。
	// dist 存在则 serve + SPA fallback；不存在（dev 未 build）时 NoRoute 给友好提示。
	serveStatic(r, log)

	return r
}

// serveStatic 服务前端 dist（web 在根路径，admin-web 在 /admin/*）。
// dist 经 go:embed 内嵌进二进制（web.DistFS / adminweb.DistFS），单文件分发（plan §A.3）。
// 编译期 dist 必须存在（make build 依赖 build-web），故无需磁盘回退。
func serveStatic(r *gin.Engine, log *zap.Logger) {
	// embed //go:embed all:dist 让 DistFS 文件路径带 "dist/" 前缀，先 Sub 到 dist 根
	webDist, _ := fs.Sub(web.DistFS, "dist")
	adminDist, _ := fs.Sub(adminweb.DistFS, "dist")

	// /assets/* -> dist/assets/*
	webAssets, _ := fs.Sub(webDist, "assets")
	adminAssets, _ := fs.Sub(adminDist, "assets")
	r.StaticFS("/assets", http.FS(webAssets))
	r.StaticFS("/admin/assets", http.FS(adminAssets))

	// index.html 直接读 embed 内容返回，避免 http.FileServer 对 "/index.html" 的 301 重定向（会死循环 /）。
	// index 必须禁缓存： assets 带内容哈希可长缓存，但 index 引用具体哈希文件；
	// 若浏览器缓存了旧 index（尤其旧版本异常时的空响应），升级后仍会引用不存在的旧 assets 导致白屏。
	webIndex, _ := fs.ReadFile(webDist, "index.html")
	adminIndex, _ := fs.ReadFile(adminDist, "index.html")
	serveIndex := func(c *gin.Context, body []byte) {
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", body)
	}

	r.GET("/", func(c *gin.Context) { serveIndex(c, webIndex) })
	r.GET("/admin", func(c *gin.Context) { serveIndex(c, adminIndex) })

	log.Info("web 静态服务就绪（embed）")

	// NoRoute：API 路径返回 JSON 404；其余 fallback 到 index.html（SPA 路由）
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		// /api 是云端 nginx 反代前缀，claw 本地无此前缀；命中说明前端用了错误的 baseURL，
		// 返回 JSON 404 而非 SPA HTML，避免前端把 HTML 当数据解析出现难以排查的白屏
		if strings.HasPrefix(path, "/v1") || strings.HasPrefix(path, "/api") || strings.HasPrefix(path, "/claw-console") ||
			strings.HasPrefix(path, "/_internal") || strings.HasPrefix(path, "/health") {
			c.JSON(http.StatusNotFound, gin.H{"code": 4040, "message": "接口不存在: " + path})
			return
		}
		// admin-web SPA fallback
		if strings.HasPrefix(path, "/admin") {
			serveIndex(c, adminIndex)
			return
		}
		// web SPA fallback
		serveIndex(c, webIndex)
	})
}
