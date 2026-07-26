// Package main 是 Eleball-claw 本地网关入口（claw-server）。
//
// claw-server 是云端 cmd/server 的「本地化裁剪版」：复用同一 gateway 模块的 internal/ 分层，
// 但仅装配本地必需的服务，注入 nil billing（本地不计费，Ele Agent 模型经 BaseURL 转发云端计费），
// 路由用 claw_router（无 /v1/admin、无支付/CDK/VIP 套餐/提现/admin gate，新增 /v1/claw-console 本地控制台）。
// 链接期未引用的包（payment/cdk/admin_gate 等自动驱动）会被剔除，产物即裁剪后的 claw 二进制。
//
// 详见 docs/marketing/claw-implementation-plan.md §B/§I（P1 脚手架）。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/handler"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/router"
	"github.com/eleball/gateway/internal/seed"
	"github.com/eleball/gateway/internal/service"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/eleball/gateway/pkg/util"
	sqlite "github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// gitSHA 由编译时 -ldflags 注入；本地 go run 时从构建信息读取。
var gitSHA = "unknown"

func getGitSHA() string {
	if gitSHA != "unknown" && gitSHA != "" {
		return gitSHA
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
				return setting.Value[:7]
			}
		}
	}
	return "unknown"
}

func main() {
	// 子命令：module（模块管理，不启动网关）；serve 默认（剥离 serve 让 flag 正常解析）。
	// 兼容无 serve 直接 --port（eleball-claw --port=8090）。
	if len(os.Args) > 1 && os.Args[1] == "module" {
		os.Exit(runModuleCommand(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	// --port 覆盖配置端口（对应 eleball-claw serve --port=8090）
	port := flag.Int("port", 0, "本地网关端口（覆盖配置）")
	flag.Parse()

	// 1. 加载配置（默认 configs/claw.yaml，可由 CONFIG_PATH 覆盖）
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		execPath, err := os.Executable()
		if err != nil {
			log.Fatalf("获取执行路径失败: %v", err)
		}
		// install 把 claw.yaml 放在 ~/.eleball-claw/claw.yaml，exe 在 bin/，故默认找上级 ../claw.yaml
		configPath = filepath.Join(filepath.Dir(execPath), "..", "claw.yaml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			// 开发期兜底：configs/claw.yaml
			configPath = "configs/claw.yaml"
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if *port > 0 {
		cfg.Server.Port = *port
	}

	// 2. 日志
	var logger *zap.Logger
	if cfg.Server.Mode == "release" {
		logger, _ = zap.NewProduction()
	} else {
		logger, _ = zap.NewDevelopment()
	}
	defer logger.Sync()

	logger.Info("Eleball-claw 本地网关启动",
		zap.String("git_sha", getGitSHA()),
		zap.String("mode", cfg.Server.Mode),
		zap.String("config_path", configPath),
		zap.String("eleagent_base_url", cfg.Server.EleagentBaseURL),
	)

	// 3. 数据库（纯 Go SQLite，无 CGO）
	db, err := gorm.Open(sqlite.Open(cfg.Database.DSN), &gorm.Config{})
	if err != nil {
		logger.Fatal("连接数据库失败", zap.Error(err))
	}

	migrator := db.Migrator()

	// 自动迁移：保留云端全表（claw 复用既有模型；裁剪在路由层）。
	// claw 不迁移 users 表：账户统一走云端，本地无 user 行（VIPService.SetUnrestricted + agent SetUnrestricted 均不查本地 user）。
	// 云端专属表（vip_plans/orders/recharge_packages 等）在 claw 为空表、不暴露路由，无副作用。
	if err := db.AutoMigrate(
		&model.Device{},
		&model.Conversation{},
		&model.ChatConversation{},
		&model.ChatMessage{},
		&model.AgentSession{},
		&model.AgentSessionOutput{},
		&model.TokenUsage{},
		&model.BalanceTransaction{},
		&model.ActivityEvent{},
		&model.Order{},
		&model.AgentItem{},
		&model.AgentPurchase{},
		&model.AgentReview{},
		&model.AgentFavorite{},
		&model.AgentUserTool{},
		&model.DeveloperAccount{},
		&model.WithdrawalRecord{},
		&model.ProviderApiKey{},
		&model.EleAgentModelConfig{},
		&model.SystemSetting{},
		&model.RechargePackage{},
		&model.VIPPlan{},
		&model.VIPSubscription{},
		&model.ModuleRecord{},
		&model.DriverRecord{},
		&model.AgentUserCredential{},
		&model.Assistant{},
		&model.AssistantItem{},
		&model.Team{},
		&model.TeamMemory{},
		&model.VisualGenerationTask{},
		&model.VisualConversation{},
	); err != nil {
		logger.Fatal("数据库迁移失败", zap.Error(err))
	}

	// 兼容迁移：modules/drivers 旧字段 runtime_type -> transport_type（复用云端 DB时生效）
	if migrator.HasColumn(&model.ModuleRecord{}, "runtime_type") {
		if err := migrator.RenameColumn(&model.ModuleRecord{}, "runtime_type", "transport_type"); err != nil {
			logger.Warn("重命名 modules.runtime_type 失败", zap.Error(err))
		}
	}
	if migrator.HasColumn(&model.DriverRecord{}, "runtime_type") {
		if err := migrator.RenameColumn(&model.DriverRecord{}, "runtime_type", "transport_type"); err != nil {
			logger.Warn("重命名 drivers.runtime_type 失败", zap.Error(err))
		}
	}
	if migrator.HasTable(&model.DriverRecord{}) && migrator.HasIndex(&model.DriverRecord{}, "idx_drivers_auth_token") {
		if err := migrator.DropIndex(&model.DriverRecord{}, "idx_drivers_auth_token"); err != nil {
			logger.Warn("删除 drivers.auth_token 旧唯一索引失败", zap.Error(err))
		}
	}
	if err := migrator.CreateIndex(&model.DriverRecord{}, "idx_drivers_auth_token"); err != nil {
		logger.Warn("创建 drivers.auth_token 普通索引失败", zap.Error(err))
	}

	// 4. 基础设施
	jwtUtil := util.NewJWTUtil(cfg.JWT.Secret, cfg.JWT.AccessExpireHours, cfg.JWT.RefreshExpireHours)

	// 5. 仓库
	userRepo := repository.NewUserRepo(db)
	conversationRepo := repository.NewConversationRepo(db)
	chatConversationRepo := repository.NewChatConversationRepo(db)
	agentSessionRepo := repository.NewAgentSessionRepo(db)
	visualTaskRepo := repository.NewVisualTaskRepo(db)
	visualConversationRepo := repository.NewVisualConversationRepo(db)
	billingRepo := repository.NewBillingRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	agentRepo := repository.NewAgentRepo(db)
	apiKeyRepo := repository.NewApiKeyRepo(db)
	eleAgentModelRepo := repository.NewEleAgentModelRepo(db)
	settingRepo := repository.NewSettingRepo(db)
	vipRepo := repository.NewVIPRepo(db)
	moduleRepo := repository.NewModuleRepo(db)
	driverRepo := repository.NewDriverRepo(db)
	assistantRepo := repository.NewAssistantRepo(db)
	teamRepo := repository.NewTeamRepo(db)

	// 6. 服务

	// API Key 加密主密钥：未配置时派生默认（与云端一致）
	masterKey := os.Getenv("ENCRYPTION_MASTER_KEY")
	if masterKey == "" {
		seedPass := os.Getenv("ADMIN_SEED_PASSWORD")
		if seedPass == "" {
			seedPass = "admin123"
		}
		h := sha256.Sum256([]byte("eleball:master-key:" + seedPass))
		masterKey = hex.EncodeToString(h[:])
		logger.Warn("未配置 ENCRYPTION_MASTER_KEY，已派生默认密钥；生产环境请单独配置")
	}
	keyManagerService, err := service.NewKeyManagerService(apiKeyRepo, masterKey)
	if err != nil {
		logger.Fatal("初始化 API Key 管理器失败", zap.Error(err))
	}

	clientFactory := service.NewClientFactory(cfg.LLM.Timeout)
	clientFactory.SetLogger(logger)
	eleAgentModelService, err := service.NewEleAgentModelService(eleAgentModelRepo, masterKey)
	if err != nil {
		logger.Fatal("初始化 Ele Agent 模型配置服务失败", zap.Error(err))
	}
	// 指向云端的代理配置在调用时自动使用请求方当前登录态，免于存储 token 过期后手动换 Key
	eleAgentModelService.SetCloudAPIBase(cfg.Server.EleagentBaseURL)

	// 认证统一走云端 eleball.cn 账户（claw web authApi 直连云端），claw 本地不再装配
	// authService/mailService/otpService，也不再提供 /v1/auth/* 路由与本地 users 账户。
	// 本地接口 JWTAuth 接受云端签发的 JWT（部署时 JWT_SECRET 与云端一致，见 config.go BindEnv）。

	// VIP 服务保留真实实例（对话配额检查依赖它；claw 不暴露 VIP 套餐/订阅路由）
	vipService := service.NewVIPService(db, vipRepo, userRepo, billingRepo, orderRepo, logger)
	// claw 本地不限对话/Agent/ASR 配额（云端 VIP 仅用于云端秘技门控，由 CloudAccountService 校验）
	vipService.SetUnrestricted(true)

	if cfg.Server.Mode != "release" {
		if err := eleAgentModelService.EnsureDefaultConfigs(); err != nil {
			logger.Warn("Ele Agent 默认模型配置初始化失败", zap.Error(err))
		}
	}

	chatService := service.NewChatProxyService(keyManagerService, clientFactory, eleAgentModelService, logger)
	// claw 不依赖本地 users 表（userRepo 留空 -> TouchActive 跳过；本地无 DAU 统计需求）
	chatService.SetMaxRetries(cfg.LLM.MaxRetries)

	// BYOK fallback（环境变量兜底）
	if openAIKey := os.Getenv("OPENAI_API_KEY"); openAIKey != "" {
		openAIClient := llm.NewOpenAIClient(openAIKey, "", cfg.LLM.Timeout)
		openAIClient.SetLogger(logger)
		chatService.RegisterFallbackClient(llm.ProviderOpenAI, openAIClient)
		logger.Info("已注册 OpenAI fallback 客户端")
	}
	if deepSeekKey := os.Getenv("DEEPSEEK_API_KEY"); deepSeekKey != "" {
		deepSeekClient := llm.NewOpenAIClient(deepSeekKey, "https://api.deepseek.com/v1", cfg.LLM.Timeout)
		deepSeekClient.SetLogger(logger)
		chatService.RegisterFallbackClient(llm.ProviderDeepSeek, deepSeekClient)
		logger.Info("已注册 DeepSeek fallback 客户端")
	}

	// 注意：claw 不创建 billingService（本地不计费）；下方 chat/agent/visual/eleagent 均注入 nil，
	// 各服务 nil 检查后跳过扣费与余额校验（Ele Agent 模型经 BaseURL 转发云端，由云端账户计费）。
	var billingService *service.BillingService // nil

	// 集市模块注册表（本地预置 + 云端拉取 + 本地自部署，统一去重）
	moduleRegistry := service.NewModuleRegistry(&cfg.AgentReach)
	moduleRegistry.SetLogger(logger)
	moduleRegistry.SetRepo(moduleRepo)
	moduleRegistry.SetDriverRepo(driverRepo)
	moduleService := service.NewModuleService(moduleRegistry, moduleRepo, driverRepo)
	// claw 云端秘技安装后落本地 AgentItem/AgentPurchase（激活链路依赖购买记录）
	moduleService.SetAgentRepo(agentRepo)

	// 扫描 marketplace/ 预置官方模块（search-web 等）
	if err := seed.AutoEnsureMarketplaceModules(moduleService, logger); err != nil {
		logger.Warn("自动补齐内置模块失败", zap.Error(err))
	}

	// 预置本地 search-web 官方搜索 SKU（免费，百度千帆/必应两变体）
	if err := seed.SearchWebSKUs(agentRepo, logger); err != nil {
		logger.Warn("预置 SearchWeb 官方搜索秘技失败", zap.Error(err))
	}

	// 启动模块后台健康探测（每 5 分钟一次）
	moduleRegistry.Start()
	defer moduleRegistry.Stop()

	agentService := service.NewAgentMarketService(db, agentRepo, userRepo, vipService, moduleRegistry)
	// claw 本地购买仅放行免费 SKU；付费秘技引导云端 eleball.cn 购买
	agentService.SetLocalFreeOnly(true)

	// Agent 工作流
	agentSandbox := service.NewFileSandbox(cfg.Agent.BasePath, cfg.Agent.KnowledgeBase)
	agentRegistry := service.NewToolRegistry()
	agentCredentialRepo := repository.NewAgentCredentialRepo(db)
	agentCredentialService := service.NewAgentCredentialService(agentCredentialRepo, agentRepo)
	agentRegistry.DriverRegistry().Register(service.NewModuleDriver(moduleRegistry, agentCredentialService))
	agentSchemaBuilder := service.NewToolSchemaBuilder(agentRegistry)
	agentTrigger := service.NewAgentTrigger()

	// Agent LLM 客户端解析器：BYOK 优先，eleagent 走云端 BaseURL
	agentClientResolver := func(ctx context.Context, provider, model, baseURL, apiKey string) (service.AgentLLMClient, error) {
		if provider == "" && cfg.Agent.APIKey != "" {
			client := llm.NewOpenAIClient(cfg.Agent.APIKey, cfg.Agent.BaseURL, cfg.LLM.Timeout)
			client.SetLogger(logger)
			return client, nil
		}
		if provider == "" || llm.Provider(provider) == llm.ProviderEleAgent {
			return chatService.ResolveAgentClient(ctx, provider, model)
		}
		if baseURL == "" || apiKey == "" {
			return nil, fmt.Errorf("自定义模型需传入 base_url 与 api_key")
		}
		client := llm.NewOpenAIClient(apiKey, baseURL, cfg.LLM.Timeout)
		client.SetLogger(logger)
		return client, nil
	}

	// 对话分组服务（Agent Team）
	teamService := service.NewTeamService(db, teamRepo)
	// 组共享记忆仓库（Agent Team P2）：删除组时级联清理组记忆
	teamMemoryRepo := repository.NewTeamMemoryRepo(db)
	teamService.SetTeamMemoryRepo(teamMemoryRepo)
	teamMemoryService := service.NewTeamMemoryService(teamMemoryRepo, teamService)
	conversationService := service.NewConversationService(chatConversationRepo, vipService, cfg.Agent.BasePath)
	// 对话归组（PATCH team_id）需校验分组归属
	conversationService.SetTeamService(teamService)
	visualUploadService := service.NewVisualUploadService(agentSandbox)
	visualConversationService := service.NewVisualConversationService(visualConversationRepo, visualTaskRepo, visualUploadService)
	settingService := service.NewSettingService(settingRepo)
	visualGenerationService := service.NewVisualGenerationService(visualTaskRepo, visualConversationService, billingService, eleAgentModelService, settingService, chatService, visualUploadService, logger)
	agentWorkflowService := service.NewAgentService(conversationService, agentSessionRepo, userRepo, vipService, billingService, eleAgentModelService, agentSandbox, agentRegistry, agentSchemaBuilder, agentTrigger, agentClientResolver, cfg.Agent.Model, cfg.Agent.MaxSteps, logger)
	agentWorkflowService.SetMaxRetries(cfg.LLM.MaxRetries)
	// claw 本地不限 Agent 模式（云端账户统一后跳过 VIP 门控，容忍无本地 user）
	agentWorkflowService.SetUnrestricted(true)
	agentToolLoader := service.NewAgentToolLoader(agentRepo, agentRegistry.DriverRegistry(), moduleRegistry)
	agentToolLoader.SetModuleService(moduleService)
	agentWorkflowService.SetAgentToolLoader(agentToolLoader)
	agentService.SetAgentToolLoader(agentToolLoader)
	agentService.SetModuleService(moduleService)
	// claw 云端秘技 provenance 判定（激活云端来源秘技需 VIP1+）
	agentService.SetModuleRepo(moduleRepo)
	// 助手服务（已激活秘技的命名组合）；Agent 执行按请求/会话绑定的助手过滤动态工具
	assistantService := service.NewAssistantService(db, assistantRepo, agentRepo)
	agentWorkflowService.SetAssistantService(assistantService)
	// 组共享记忆服务（Agent Team P2）：执行前检索注入 + 执行后异步提取
	agentWorkflowService.SetTeamMemoryService(teamMemoryService)
	// 云端账户/VIP 缓存（claw 仅从云端取 VIP 一项用于门控）
	cloudAccountService := service.NewCloudAccountService(cfg.Server.EleagentBaseURL)

	releaseRootPath := cfg.Release.RootPath
	if releaseRootPath == "" {
		releaseRootPath = "releases"
	}
	releaseService := service.NewReleaseService(releaseRootPath)

	eleAgentService := service.NewEleAgentService(chatService, eleAgentModelService, billingService, cfg.Server.EleagentBaseURL)
	// STT 已下沉为 marketplace/stt 模块（百度 ASR key 作模块凭证经 web 配置），claw 不再内置 /stt 端点。
	// 云端 cmd/server 的 /stt 内置服务保持不动（internal/service/stt_service.go 底座两端共用）。

	// 7. 处理器（仅保留 claw 所需；billing=nil 注入 chat/eleagent）
	chatHandler := handler.NewChatHandler(chatService, billingService, logger)
	syncHandler := handler.NewSyncHandler(conversationRepo)
	eleAgentHandler := handler.NewEleAgentHandler(eleAgentService, eleAgentModelService)
	// 本地模型配置 CRUD（BYOK + Ele Agent 云端代理快捷接入），复用云端管理端 handler
	adminEleAgentModelHandler := handler.NewAdminEleAgentModelHandler(eleAgentModelService)
	conversationHandler := handler.NewConversationHandler(conversationService, agentWorkflowService)
	moduleHandler := handler.NewModuleHandler(moduleService, logger)
	// P4：提交审核转发云端 register 接口
	moduleHandler.SetCloudAPIBase(cfg.Server.EleagentBaseURL)
	// claw 云端第三方模块拉取安装需 VIP1+
	moduleHandler.SetCloudAccountService(cloudAccountService)
	agentWorkflowHandler := handler.NewAgentWorkflowHandler(agentWorkflowService)
	// claw：search-providers 优先转发 search-web 模块的 list_sources（搜索源配置在模块侧）
	agentWorkflowHandler.SetModuleRegistry(moduleRegistry)
	agentHandler := handler.NewAgentHandler(agentService)
	// claw 云端来源秘技激活需 VIP1+
	agentHandler.SetCloudAccountService(cloudAccountService)
	agentCredentialHandler := handler.NewAgentCredentialHandler(agentCredentialService)
	visualHandler := handler.NewVisualHandler(visualGenerationService, visualUploadService, visualConversationService)
	publicSettingHandler := handler.NewPublicSettingHandler(settingService)
	// 本地控制台设置读写（claw 裁剪页：Prompt 融合模型等本地生效项）
	adminSettingHandler := handler.NewAdminSettingHandler(settingService)
	releaseHandler := handler.NewReleaseHandler(releaseService, logger)
	clawConsoleHandler := handler.NewClawConsoleHandler(db)
	assistantHandler := handler.NewAssistantHandler(assistantService)
	teamHandler := handler.NewTeamHandler(teamService)
	teamMemoryHandler := handler.NewTeamMemoryHandler(teamMemoryService)
	// 本地系统状态（docker/compose 可用性 + 模块自动上下线开关）
	systemHandler := handler.NewSystemHandler(cfg.Modules)

	// 8. 路由（claw 裁剪版）
	r := router.NewClawRouter(cfg, logger, jwtUtil,
		chatHandler, syncHandler, eleAgentHandler,
		conversationHandler, moduleHandler, agentWorkflowHandler, agentHandler,
		agentCredentialHandler, visualHandler, publicSettingHandler, releaseHandler,
		clawConsoleHandler, cloudAccountService, adminEleAgentModelHandler, adminSettingHandler,
		assistantHandler, teamHandler, teamMemoryHandler, systemHandler,
	)

	// 9. 启动
	// P5.2：启动 mDNS 广播，供 APP 同局域网 NSD 发现（LAN 直连通道）
	deviceID := os.Getenv("CLAW_DEVICE_ID")
	if deviceID == "" {
		if h, err := os.Hostname(); err == nil {
			deviceID = h
		}
	}
	if mdnsBroadcaster, mdnsErr := service.NewMdnsBroadcaster(deviceID, cfg.Server.Port, logger); mdnsErr != nil {
		logger.Warn("mDNS 广播启动失败（LAN 发现不可用，不影响其他功能）", zap.Error(mdnsErr))
	} else {
		defer mdnsBroadcaster.Stop()
	}

	// P5.3：启动 relay 隧道（外网兜底）。缺 RELAY_URL/CLAW_RELAY_TOKEN 时跳过（仅 LAN）。
	// CLAW_RELAY_TOKEN 为用户在控制台登录后获得的 JWT（统一账户验签，与 gateway JWT_SECRET 一致）。
	relayURL := os.Getenv("RELAY_URL")
	relayToken := os.Getenv("CLAW_RELAY_TOKEN")
	// P5.4 E2E 加密器（claw 静态 X25519 密钥对；公钥应注册云端设备列表供 APP 协商）
	e2eCipher, err := service.NewE2ECipher()
	if err != nil {
		logger.Warn("E2E 加密器初始化失败（relay 将走明文）", zap.Error(err))
		e2eCipher = nil
	} else {
		logger.Info("E2E 加密就绪", zap.String("claw_pubkey", e2eCipher.PublicKeyBase64()))
	}
	relayTunnel := service.NewRelayTunnel(
		relayURL, deviceID, relayToken,
		fmt.Sprintf("http://localhost:%d", cfg.Server.Port),
		logger, e2eCipher,
	)
	relayTunnel.Start()
	defer relayTunnel.Stop()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("Eleball-claw 启动", zap.String("addr", addr), zap.String("mode", cfg.Server.Mode))

	// 启动后自动打开浏览器访问本地控制台（双击 exe / 手动部署均生效）。
	// CLAW_NO_BROWSER=1 可关闭；Linux 无桌面环境（无 DISPLAY）时自动跳过。
	openBrowserDelayed(fmt.Sprintf("http://localhost:%d", cfg.Server.Port), logger)

	// 预置模块自动上线：后台执行（拉镜像优先、本地构建兜底，不阻塞网关启动）；
	// 成功启动的模块名经 channel 回传，供退出时自动下线。
	startedCh := make(chan []string, 1)
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if cfg.Modules.AutoStart {
		go func() {
			startedCh <- autoStartModules(sigCtx, logger, cfg.Modules, moduleRegistry)
		}()
	} else {
		startedCh <- nil
	}

	srv := &http.Server{Addr: addr, Handler: r}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("HTTP 服务启动失败", zap.Error(err))
		}
	}()

	// 终端提示（双行：英文行兜底 GBK 控制台乱码场景，中文行为用户主提示）。
	// 走 fmt 而非 zap logger：保持纯文本输出，不混进 JSON 日志流。
	fmt.Println()
	fmt.Printf("Eleball-claw is running at http://localhost:%d — press Ctrl+C or close this terminal to stop the service.\n", cfg.Server.Port)
	fmt.Println("Ctrl+C 或关闭该终端后服务将会停止。")
	fmt.Println()

	// 阻塞等待退出信号（Ctrl+C / SIGTERM），然后优雅关闭
	<-sigCtx.Done()
	logger.Info("收到退出信号，开始优雅关闭")

	// 预置模块自动下线：仅清理本网关成功启动的模块（等自动上线流程收尾，最多 30s）
	if cfg.Modules.AutoStop {
		select {
		case started := <-startedCh:
			autoStopModules(logger, started)
		case <-time.After(30 * time.Second):
			logger.Warn("等待模块上线流程收尾超时，跳过自动下线")
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("HTTP 服务优雅关闭失败", zap.Error(err))
	}
}
