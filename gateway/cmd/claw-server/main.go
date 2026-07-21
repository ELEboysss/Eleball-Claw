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
	"os"
	"path/filepath"
	"runtime/debug"

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
	// --port 覆盖配置端口（对应 eleball-claw serve --port=8080）
	port := flag.Int("port", 0, "本地网关端口（覆盖配置）")
	flag.Parse()

	// 1. 加载配置（默认 configs/claw.yaml，可由 CONFIG_PATH 覆盖）
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		execPath, err := os.Executable()
		if err != nil {
			log.Fatalf("获取执行路径失败: %v", err)
		}
		configPath = filepath.Join(filepath.Dir(execPath), "..", "..", "configs", "claw.yaml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
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
	// 云端专属表（vip_plans/orders/recharge_packages 等）在 claw 为空表、不暴露路由，无副作用。
	if err := db.AutoMigrate(
		&model.User{},
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
	activityRepo := repository.NewActivityRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	agentRepo := repository.NewAgentRepo(db)
	apiKeyRepo := repository.NewApiKeyRepo(db)
	eleAgentModelRepo := repository.NewEleAgentModelRepo(db)
	settingRepo := repository.NewSettingRepo(db)
	vipRepo := repository.NewVIPRepo(db)
	moduleRepo := repository.NewModuleRepo(db)
	driverRepo := repository.NewDriverRepo(db)

	// 6. 服务
	activityService := service.NewActivityService(activityRepo)

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

	// 邮件 + OTP（邮箱验证码登录）；mail.enabled=false 时 OTP 返回「未开通」
	mailService := service.NewMailService(cfg.Mail)
	otpService := service.NewOTPService(mailService)

	authService := service.NewAuthService(userRepo, jwtUtil, activityService, cfg.Server.EleagentBaseURL, eleAgentModelService, otpService)
	// VIP 服务保留真实实例（对话配额检查依赖它；claw 不暴露 VIP 套餐/订阅路由）
	vipService := service.NewVIPService(db, vipRepo, userRepo, billingRepo, orderRepo, logger)

	if cfg.Server.Mode != "release" {
		if err := eleAgentModelService.EnsureDefaultConfigs(); err != nil {
			logger.Warn("Ele Agent 默认模型配置初始化失败", zap.Error(err))
		}
	}

	chatService := service.NewChatProxyService(keyManagerService, clientFactory, eleAgentModelService, logger)
	chatService.SetUserRepo(userRepo)
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

	// 扫描 marketplace/ 预置官方模块（search-web 等）
	if err := seed.AutoEnsureMarketplaceModules(moduleService, logger); err != nil {
		logger.Warn("自动补齐内置模块失败", zap.Error(err))
	}

	// 启动模块后台健康探测（每 5 分钟一次）
	moduleRegistry.Start()
	defer moduleRegistry.Stop()

	agentService := service.NewAgentMarketService(db, agentRepo, userRepo, vipService, moduleRegistry)

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

	conversationService := service.NewConversationService(chatConversationRepo, vipService, cfg.Agent.BasePath)
	visualUploadService := service.NewVisualUploadService(agentSandbox)
	visualConversationService := service.NewVisualConversationService(visualConversationRepo, visualTaskRepo, visualUploadService)
	settingService := service.NewSettingService(settingRepo)
	visualGenerationService := service.NewVisualGenerationService(visualTaskRepo, visualConversationService, billingService, eleAgentModelService, settingService, chatService, visualUploadService, logger)
	agentWorkflowService := service.NewAgentService(conversationService, agentSessionRepo, userRepo, vipService, billingService, eleAgentModelService, agentSandbox, agentRegistry, agentSchemaBuilder, agentTrigger, agentClientResolver, cfg.Agent.Model, cfg.Agent.MaxSteps, logger)
	agentWorkflowService.SetMaxRetries(cfg.LLM.MaxRetries)
	agentToolLoader := service.NewAgentToolLoader(agentRepo, agentRegistry.DriverRegistry(), moduleRegistry)
	agentToolLoader.SetModuleService(moduleService)
	agentWorkflowService.SetAgentToolLoader(agentToolLoader)
	agentService.SetAgentToolLoader(agentToolLoader)
	agentService.SetModuleService(moduleService)

	releaseRootPath := cfg.Release.RootPath
	if releaseRootPath == "" {
		releaseRootPath = "releases"
	}
	releaseService := service.NewReleaseService(releaseRootPath)

	eleAgentService := service.NewEleAgentService(chatService, eleAgentModelService, billingService, cfg.Server.EleagentBaseURL)
	sttService := service.NewSttService(cfg.ASR.Provider, cfg.ASR.AppID, cfg.ASR.APIKey, cfg.ASR.SecretKey, cfg.ASR.BaseURL, cfg.ASR.Timeout, cfg.ASR.MaxAudioMB, logger)

	// 7. 处理器（仅保留 claw 所需；billing=nil 注入 chat/eleagent）
	authHandler := handler.NewAuthHandler(authService, vipService)
	chatHandler := handler.NewChatHandler(chatService, billingService, logger)
	syncHandler := handler.NewSyncHandler(conversationRepo)
	eleAgentHandler := handler.NewEleAgentHandler(eleAgentService, eleAgentModelService)
	sttHandler := handler.NewSttHandler(sttService, userRepo, vipService, logger)
	conversationHandler := handler.NewConversationHandler(conversationService, agentWorkflowService)
	moduleHandler := handler.NewModuleHandler(moduleService, logger)
	agentWorkflowHandler := handler.NewAgentWorkflowHandler(agentWorkflowService)
	agentHandler := handler.NewAgentHandler(agentService)
	agentCredentialHandler := handler.NewAgentCredentialHandler(agentCredentialService)
	visualHandler := handler.NewVisualHandler(visualGenerationService, visualUploadService, visualConversationService)
	publicSettingHandler := handler.NewPublicSettingHandler(settingService)
	releaseHandler := handler.NewReleaseHandler(releaseService, logger)

	// 8. 路由（claw 裁剪版）
	r := router.NewClawRouter(cfg, logger, jwtUtil,
		authHandler, chatHandler, syncHandler, eleAgentHandler, sttHandler,
		conversationHandler, moduleHandler, agentWorkflowHandler, agentHandler,
		agentCredentialHandler, visualHandler, publicSettingHandler, releaseHandler,
	)

	// 9. 启动
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("Eleball-claw 启动", zap.String("addr", addr), zap.String("mode", cfg.Server.Mode))
	if err := r.Run(addr); err != nil {
		logger.Fatal("HTTP 服务启动失败", zap.Error(err))
	}
}
