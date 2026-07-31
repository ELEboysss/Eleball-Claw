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
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/handler"
	"github.com/eleball/gateway/internal/middleware"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/router"
	"github.com/eleball/gateway/internal/seed"
	"github.com/eleball/gateway/internal/service"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/eleball/gateway/pkg/util"
	sqlite "github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// gitSHA 由编译时 -ldflags 注入，用于确认当前运行版本。
// 本地开发使用 go run 时，会通过 debug.ReadBuildInfo 读取 vcs.revision。
var gitSHA = "unknown"

// getGitSHA 返回当前二进制对应的 Git SHA。
// 优先使用 ldflags 注入的值；未注入时尝试从构建信息中读取。
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
	// 调试开关：显式启用 /v1/admin 管理后台接口与 admin-web。
	// 默认为 false，只有显式传入 --enable-admin 才会暴露管理后台，防止生产环境误开放。
	enableAdmin := flag.Bool("enable-admin", false, "启用 /v1/admin 管理后台接口（默认关闭）")
	// 数据填充开关：向持久化注册表写入示例模块与 SKU，然后退出；Gateway 启动时不再自动执行。
	seedMode := flag.Bool("seed", false, "预置示例模块与 SKU 后退出")
	flag.Parse()

	// 1. 加载配置
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		// 默认从 configs/config.yaml 加载
		execPath, err := os.Executable()
		if err != nil {
			log.Fatalf("获取执行路径失败: %v", err)
		}
		configPath = filepath.Join(filepath.Dir(execPath), "..", "..", "configs", "config.yaml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			configPath = "configs/config.yaml"
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 初始化日志
	var logger *zap.Logger
	if cfg.Server.Mode == "release" {
		logger, _ = zap.NewProduction()
	} else {
		logger, _ = zap.NewDevelopment()
	}
	defer logger.Sync()

	// 命令行调试开关优先级高于配置文件
	if *enableAdmin {
		cfg.Admin.Enabled = true
	}

	// 初始化管理后台运行时开关，使 /_internal/admin/* 控制与启动配置保持一致
	middleware.SetAdminEnabled(cfg.Admin.Enabled)

	logger.Info("Gateway 服务启动",
		zap.String("git_sha", getGitSHA()),
		zap.String("mode", cfg.Server.Mode),
		zap.String("config_path", configPath),
		zap.Bool("admin_enabled", cfg.Admin.Enabled),
	)

	// 3. 初始化数据库（使用 github.com/glebarez/sqlite，纯 Go SQLite 驱动，无需 CGO）
	db, err := gorm.Open(sqlite.Open(cfg.Database.DSN), &gorm.Config{})
	if err != nil {
		logger.Fatal("连接数据库失败", zap.Error(err))
	}

	// 兼容迁移检测：记录 AutoMigrate 前 ele_agent_model_configs 是否已有 supports_chat 列，
	// 用于迁移后判断该列是否本次新增、是否需要回填存量数据
	migrator := db.Migrator()
	hadSupportsChatColumn := migrator.HasColumn(&model.EleAgentModelConfig{}, "supports_chat")

	// 自动迁移
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

	// 兼容迁移：supports_chat 为本次新增列时，将存量对话协议模型回填为支持文字对话；
	// 视觉生成协议（agnes_image/agnes_video/seedance/seedream 等）保持 false。
	// 仅在加列当次执行，避免覆盖管理员后续手工调整。
	if !hadSupportsChatColumn {
		if err := db.Exec("UPDATE ele_agent_model_configs SET supports_chat = ? WHERE protocol IN ('openai_compatible','anthropic_messages')", true).Error; err != nil {
			logger.Warn("回填 ele_agent_model_configs.supports_chat 失败", zap.Error(err))
		}
	}

	// 订单幂等兜底：支付宝交易号部分唯一索引（空串为管理员确认/CDK 等非渠道订单，不参与唯一约束）。
	// 防支付宝重复通知或并发回调导致同一 trade_no 入账两次。
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_trade_no ON orders(trade_no) WHERE trade_no <> ''").Error; err != nil {
		logger.Fatal("创建 orders.trade_no 唯一索引失败", zap.Error(err))
	}

	// 兼容迁移：旧版本的 modules/drivers 表使用 runtime_type 字段，已重命名为 transport_type
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

	// 兼容迁移：drivers.auth_token 旧版为 uniqueIndex，导致多个空 token 的官方内置驱动无法同时创建。
	// 先删除旧唯一索引，再按当前模型创建普通索引（允许空 token 重复，业务层保证非空 token 唯一）。
	if migrator.HasTable(&model.DriverRecord{}) && migrator.HasIndex(&model.DriverRecord{}, "idx_drivers_auth_token") {
		if err := migrator.DropIndex(&model.DriverRecord{}, "idx_drivers_auth_token"); err != nil {
			logger.Warn("删除 drivers.auth_token 旧唯一索引失败", zap.Error(err))
		} else {
			logger.Info("已删除 drivers.auth_token 旧唯一索引")
		}
	}
	// 确保按更新后的模型创建普通索引
	if err := migrator.CreateIndex(&model.DriverRecord{}, "idx_drivers_auth_token"); err != nil {
		logger.Warn("创建 drivers.auth_token 普通索引失败", zap.Error(err))
	}

	// 初始化兑换码数据库（独立 SQLite 文件）
	cdkDB, err := gorm.Open(sqlite.Open(cfg.CDKDatabase.DSN), &gorm.Config{})
	if err != nil {
		logger.Fatal("连接兑换码数据库失败", zap.Error(err))
	}
	if err := cdkDB.AutoMigrate(&model.CDK{}); err != nil {
		logger.Fatal("兑换码数据库迁移失败", zap.Error(err))
	}

	// 4. 初始化基础设施
	jwtUtil := util.NewJWTUtil(cfg.JWT.Secret, cfg.JWT.AccessExpireHours, cfg.JWT.RefreshExpireHours)

	// 5. 初始化仓库
	userRepo := repository.NewUserRepo(db)

	// 预置默认管理员账号（用户名 admin）。
	// 密码优先级：
	//   1. 环境变量 ADMIN_SEED_PASSWORD
	//   2. 构建参数 ADMIN_SEED_PASSWORD（通过 Dockerfile ARG 注入）
	//   3. 默认密码 admin123
	// 若数据库中不存在 admin 账号则创建；若已存在且 ADMIN_SEED_PASSWORD 非空，则同步更新密码。
	adminPass := os.Getenv("ADMIN_SEED_PASSWORD")
	if adminPass == "" {
		adminPass = "admin123"
	}
	adminHash, _ := util.HashPassword(adminPass)

	if _, err := userRepo.GetByUsername("admin"); err != nil {
		if err := userRepo.Create(&model.User{
			ID:       "00000000-0000-0000-0000-000000000000",
			Username: "admin",
			Password: adminHash,
			Nickname: "管理员",
			Role:     model.UserRoleAdmin,
			Status:   1,
		}); err != nil {
			logger.Warn("预置管理员账号失败", zap.Error(err))
		} else {
			logger.Info("已预置管理员账号 admin")
		}
	} else if os.Getenv("ADMIN_SEED_PASSWORD") != "" {
		// 环境变量显式设置了密码，同步更新已有管理员密码
		if err := userRepo.UpdatePassword("00000000-0000-0000-0000-000000000000", adminHash); err != nil {
			logger.Warn("更新管理员密码失败", zap.Error(err))
		} else {
			logger.Info("已根据 ADMIN_SEED_PASSWORD 更新管理员密码")
		}
	}

	conversationRepo := repository.NewConversationRepo(db)
	chatConversationRepo := repository.NewChatConversationRepo(db)
	agentSessionRepo := repository.NewAgentSessionRepo(db)
	visualTaskRepo := repository.NewVisualTaskRepo(db)
	visualConversationRepo := repository.NewVisualConversationRepo(db)
	billingRepo := repository.NewBillingRepo(db)
	activityRepo := repository.NewActivityRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	rechargePackageRepo := repository.NewRechargePackageRepo(db)
	cdkRepo := repository.NewCDKRepo(cdkDB)
	agentRepo := repository.NewAgentRepo(db)
	withdrawalRepo := repository.NewWithdrawalRepo(db)
	apiKeyRepo := repository.NewApiKeyRepo(db)
	eleAgentModelRepo := repository.NewEleAgentModelRepo(db)
	settingRepo := repository.NewSettingRepo(db)
	vipRepo := repository.NewVIPRepo(db)

	// 6. 初始化服务
	activityService := service.NewActivityService(activityRepo)

	// 初始化 API Key 管理器
	masterKey := os.Getenv("ENCRYPTION_MASTER_KEY")
	if masterKey == "" {
		// 未配置加密主密钥时，使用 ADMIN_SEED_PASSWORD 派生一个固定默认密钥，
		// 避免部署时因未配置而无法加密存储。生产环境应单独配置强随机密钥。
		seedPass := os.Getenv("ADMIN_SEED_PASSWORD")
		if seedPass == "" {
			seedPass = "admin123"
		}
		h := sha256.Sum256([]byte("eleball:master-key:" + seedPass))
		masterKey = hex.EncodeToString(h[:])
		logger.Warn("未配置 ENCRYPTION_MASTER_KEY，已使用 ADMIN_SEED_PASSWORD 派生默认密钥；生产环境请单独配置 ENCRYPTION_MASTER_KEY")
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

	// 邮件 + OTP 服务（邮箱验证码登录）。邮件未配置时 Enabled=false，OTP 接口返回「未开通」。
	mailService := service.NewMailService(cfg.Mail)
	otpService := service.NewOTPService(mailService)

	// 认证服务需要 Ele Agent 模型服务来生成动态默认模型配置
	authService := service.NewAuthService(userRepo, jwtUtil, activityService, cfg.Server.EleagentBaseURL, eleAgentModelService, otpService)
	vipService := service.NewVIPService(db, vipRepo, userRepo, billingRepo, orderRepo, logger)
	// 开发/测试环境：若管理员未配置 Ele Agent 模型，且环境变量设置了 QWEN_API_KEY，则自动写入默认 Qwen3-8B
	if cfg.Server.Mode != "release" {
		if err := eleAgentModelService.EnsureDefaultConfigs(); err != nil {
			logger.Warn("Ele Agent 默认模型配置初始化失败", zap.Error(err))
		}
	}

	chatService := service.NewChatProxyService(keyManagerService, clientFactory, eleAgentModelService, logger)
	chatService.SetUserRepo(userRepo)
	// 上游 5xx/429/网络错误重试次数（对应 llm.max_retries，默认 3 次尝试）
	chatService.SetMaxRetries(cfg.LLM.MaxRetries)

	// 注册 LLM 客户端 fallback（环境变量兜底）
	// 当数据库中无可用 Key 时，使用这些环境变量注入的客户端。
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

	billingService := service.NewBillingService(userRepo, billingRepo, eleAgentModelService, activityService, vipService)
	// 支付宝客户端：未启用时返回 nil（支付未开通），已启用但配置非法时启动失败（fail fast）
	alipayClient, err := service.NewAlipayClient(cfg.Payment.Alipay)
	if err != nil {
		logger.Fatal("初始化支付宝客户端失败", zap.Error(err))
	}
	if alipayClient != nil {
		logger.Info("支付宝支付已启用", zap.Bool("sandbox", cfg.Payment.Alipay.Sandbox))
	}
	paymentService := service.NewPaymentService(db, userRepo, rechargePackageRepo, orderRepo, billingRepo, vipService, alipayClient)
	// 过期订单后台任务：每分钟扫描，超时 pending 订单先查支付宝补单/关单，再关闭本地订单
	paymentService.SetOrderExpiry(time.Duration(cfg.Payment.OrderExpireMinutes) * time.Minute)
	go paymentService.StartOrderExpiryJob(context.Background(), time.Minute)
	eleAgentService := service.NewEleAgentService(chatService, eleAgentModelService, billingService, cfg.Server.EleagentBaseURL)
	sttService := service.NewSttService(cfg.ASR.Provider, cfg.ASR.AppID, cfg.ASR.APIKey, cfg.ASR.SecretKey, cfg.ASR.BaseURL, cfg.ASR.Timeout, cfg.ASR.MaxAudioMB, logger)
	adminService := service.NewAdminService(db, userRepo, billingRepo, orderRepo, activityService, vipService)
	cdkService := service.NewCDKService(cdkRepo, userRepo, billingRepo, vipService)
	settingService := service.NewSettingService(settingRepo)
	// 集市模块注册表：发现并调用独立部署的秘技模块（如 agent-reach）
	moduleRegistry := service.NewModuleRegistry(&cfg.AgentReach)
	moduleRegistry.SetLogger(logger)
	moduleRepo := repository.NewModuleRepo(db)
	driverRepo := repository.NewDriverRepo(db)
	moduleRegistry.SetRepo(moduleRepo)
	moduleRegistry.SetDriverRepo(driverRepo)
	moduleService := service.NewModuleService(moduleRegistry, moduleRepo, driverRepo)

	// 自动扫描 marketplace/ 目录，根据 module.json 确保官方内置模块记录与驱动别名存在。
	// 新增官方内置模块时，只需在 marketplace/ 下新增目录和 module.json，无需改代码。
	if err := seed.AutoEnsureMarketplaceModules(moduleService, logger); err != nil {
		logger.Warn("自动补齐内置模块失败", zap.Error(err))
	}

	// 泛化同步官方 SKU（module.json sku_scope=cloud，如 agent-reach/firecrawl）：
	// 启动即按 marketplace/<mod>/skus/*.json 同步 manifest（含 credentials/price），
	// 不再依赖 --seed。已存在且 manifest 一致则跳过，保留 rating/counts 等统计。
	if err := seed.SyncOfficialSKUs(agentRepo, "cloud", logger); err != nil {
		logger.Warn("同步官方 SKU 失败", zap.Error(err))
	}

	// 若指定 --seed，写入示例模块与 SKU 后退出；不启动 HTTP 服务。
	if *seedMode {
		if err := seed.All(agentRepo, moduleService, logger); err != nil {
			logger.Fatal("预置示例数据失败", zap.Error(err))
		}
		logger.Info("示例数据预置完成")
		return
	}

	// 启动模块后台健康探测（每 5 分钟一次），避免高并发请求重复触发探测。
	moduleRegistry.Start()

	agentService := service.NewAgentMarketService(db, agentRepo, userRepo, vipService, moduleRegistry)

	// Agent 工作流服务
	agentSandbox := service.NewFileSandbox(cfg.Agent.BasePath, cfg.Agent.KnowledgeBase)
	agentRegistry := service.NewToolRegistry()
	// SKU 凭证服务（按用户 + SKU 管理 Cookie / API Key / Token）
	agentCredentialRepo := repository.NewAgentCredentialRepo(db)
	agentCredentialService := service.NewAgentCredentialService(agentCredentialRepo, agentRepo)
	// 注册通用集市模块驱动（firecrawl / agent-reach 等均通过此驱动调用）
	agentRegistry.DriverRegistry().Register(service.NewModuleDriver(moduleRegistry, agentCredentialService))
	agentSchemaBuilder := service.NewToolSchemaBuilder(agentRegistry)
	agentTrigger := service.NewAgentTrigger()

	// Agent LLM 客户端解析器：优先根据用户当前模型配置（Web 端传入）选择目标请求方。
	// - provider 为 eleagent 时，走 ChatProxyService 后端代理；
	// - provider 为 openai/deepseek/qwen/moonshot/custom 等 BYOK 时，使用用户传入的 apiKey/baseUrl；
	// - provider 为空时，保留旧的 AGENT_API_KEY / AGENT_BASE_URL 配置作为兜底。
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
	visualGenerationService := service.NewVisualGenerationService(visualTaskRepo, visualConversationService, billingService, eleAgentModelService, settingService, chatService, visualUploadService, logger)
	agentWorkflowService := service.NewAgentService(conversationService, agentSessionRepo, userRepo, vipService, billingService, eleAgentModelService, agentSandbox, agentRegistry, agentSchemaBuilder, agentTrigger, agentClientResolver, cfg.Agent.Model, cfg.Agent.MaxSteps, logger)
	agentWorkflowService.SetMaxRetries(cfg.LLM.MaxRetries)
	// 动态工具加载器：将用户购买的集市 SKU 注入 Agent 工作流
	agentToolLoader := service.NewAgentToolLoader(agentRepo, agentRegistry.DriverRegistry(), moduleRegistry)
	agentToolLoader.SetModuleService(moduleService)
	agentWorkflowService.SetAgentToolLoader(agentToolLoader)
	agentService.SetAgentToolLoader(agentToolLoader)
	agentService.SetModuleService(moduleService)

	// 初始化版本发布服务
	releaseRootPath := cfg.Release.RootPath
	if releaseRootPath == "" {
		// 未配置时默认使用 gateway 工作目录下的 releases/
		releaseRootPath = "releases"
	}
	releaseService := service.NewReleaseService(releaseRootPath)
	withdrawalService := service.NewWithdrawalService(db, withdrawalRepo, agentRepo, service.NewMockPaymentProvider())

	// 7. 初始化处理器
	authHandler := handler.NewAuthHandler(authService, vipService)
	vipHandler := handler.NewVIPHandler(vipService)
	chatHandler := handler.NewChatHandler(chatService, billingService, logger)
	billingHandler := handler.NewBillingHandler(billingService)
	syncHandler := handler.NewSyncHandler(conversationRepo)
	paymentHandler := handler.NewPaymentHandler(paymentService)
	adminHandler := handler.NewAdminHandler(adminService)
	adminKeyHandler := handler.NewAdminKeyHandler(keyManagerService)
	adminEleAgentModelHandler := handler.NewAdminEleAgentModelHandler(eleAgentModelService)
	adminSettingHandler := handler.NewAdminSettingHandler(settingService)
	publicSettingHandler := handler.NewPublicSettingHandler(settingService)
	rechargePackageService := service.NewRechargePackageService(rechargePackageRepo)
	rechargePackageHandler := handler.NewRechargePackageHandler(rechargePackageService)
	cdkHandler := handler.NewCDKHandler(cdkService)
	agentHandler := handler.NewAgentHandler(agentService)
	withdrawalHandler := handler.NewWithdrawalHandler(withdrawalService)
	eleAgentHandler := handler.NewEleAgentHandler(eleAgentService, eleAgentModelService)
	sttHandler := handler.NewSttHandler(sttService, userRepo, vipService, logger)
	releaseHandler := handler.NewReleaseHandler(releaseService, logger)
	conversationHandler := handler.NewConversationHandler(conversationService, agentWorkflowService)
	agentWorkflowHandler := handler.NewAgentWorkflowHandler(agentWorkflowService)
	visualHandler := handler.NewVisualHandler(visualGenerationService, visualUploadService, visualConversationService)

	// 9. 预置默认充值套餐（仅在表为空时）
	if err := seedRechargePackages(rechargePackageRepo, logger); err != nil {
		logger.Warn("预置默认充值套餐失败", zap.Error(err))
	}

	// 9.1 预置默认 VIP 套餐（仅在表为空时）
	if err := seedVIPPlans(vipRepo, logger); err != nil {
		logger.Warn("预置默认 VIP 套餐失败", zap.Error(err))
	}

	// 10. 初始化路由
	agentCredentialHandler := handler.NewAgentCredentialHandler(agentCredentialService)
	moduleHandler := handler.NewModuleHandler(moduleService, logger)

	r := router.NewRouter(cfg, logger, jwtUtil, authHandler, chatHandler, billingHandler, syncHandler, paymentHandler, adminHandler, adminKeyHandler, adminEleAgentModelHandler, adminSettingHandler, publicSettingHandler, agentHandler, withdrawalHandler, eleAgentHandler, rechargePackageHandler, cdkHandler, releaseHandler, sttHandler, conversationHandler, moduleHandler, agentWorkflowHandler, vipHandler, agentCredentialHandler, visualHandler)

	// 11. 启动服务
	defer moduleRegistry.Stop()
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("Eleball Gateway 启动", zap.String("addr", addr), zap.String("mode", cfg.Server.Mode))
	if err := r.Run(addr); err != nil {
		logger.Fatal("HTTP 服务启动失败", zap.Error(err))
	}
}

// seedRechargePackages 预置默认充值套餐
// 当 recharge_packages 表为空时，插入 5 个默认套餐：
// 小杯、中杯、大杯、超大杯，以及基于超大杯的自定义数量套餐“重度依赖”。
func seedRechargePackages(repo *repository.RechargePackageRepo, logger *zap.Logger) error {
	count, err := repo.Count()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	packages := []struct {
		name               string
		danwan             int64
		priceFen           int64
		sortOrder          int
		description        string
		isCustomMultiplier bool
	}{
		{"小杯", 1000, 990, 10, "适合偶尔使用", false},
		{"中杯", 3000, 2880, 20, "适合日常办公", false},
		{"大杯", 5000, 4580, 30, "适合高频创作", false},
		{"超大杯", 10000, 8880, 40, "超值大包", false},
		{"重度依赖", 0, 0, 50, "自定义数量，购买多份超大杯", true},
	}

	var xlargeID string
	created := make([]*model.RechargePackage, 0, len(packages))
	for i, p := range packages {
		item := &model.RechargePackage{
			ID:                 uuid.New().String(),
			Name:               p.name,
			Danwan:             p.danwan,
			PriceFen:           p.priceFen,
			SortOrder:          p.sortOrder,
			IsEnabled:          true,
			IsCustomMultiplier: p.isCustomMultiplier,
			Description:        p.description,
		}
		if err := repo.Create(item); err != nil {
			return err
		}
		created = append(created, item)
		if i == 3 { // 超大杯
			xlargeID = item.ID
		}
	}

	// 将“重度依赖”关联到“超大杯”
	if xlargeID != "" && len(created) > 4 {
		heavy := created[4]
		heavy.BasePackageID = &xlargeID
		if err := repo.Update(heavy); err != nil {
			return err
		}
	}

	logger.Info("已预置默认充值套餐", zap.Int("count", len(created)))
	return nil
}

// seedVIPPlans 预置默认 VIP 套餐
func seedVIPPlans(repo *repository.VIPRepo, logger *zap.Logger) error {
	count, err := repo.CountPlans()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	plans := []struct {
		level            int
		name             string
		priceFen         int64
		discountPercent  int
		maxConversations int
		maxAgentSessions int
		asrQuotaMonthly  int64
		agentEnabled     bool
		fileToolsEnabled bool
		description      string
	}{
		{0, "小弹丸", 0, 100, 100, 10, 1000, false, false, "新用户默认等级，仅支持普通对话"},
		{1, "强力弹丸", 4900, 100, 500, 100, 5000, true, true, "解锁 Agent 模式与文件处理能力"},
		{2, "超级弹丸", 9900, 80, 1000, 200, 20000, true, true, "全部能力 + 模型调用 8 折优惠"},
	}

	for _, p := range plans {
		plan := &model.VIPPlan{
			ID:               uuid.New().String(),
			Level:            p.level,
			Name:             p.name,
			PriceFen:         p.priceFen,
			DurationDays:     30,
			DiscountPercent:  p.discountPercent,
			MaxConversations: p.maxConversations,
			MaxAgentSessions: p.maxAgentSessions,
			AsrQuotaMonthly:  p.asrQuotaMonthly,
			AgentEnabled:     p.agentEnabled,
			FileToolsEnabled: p.fileToolsEnabled,
			IsEnabled:        true,
			Description:      p.description,
		}
		if err := repo.CreatePlan(plan); err != nil {
			return err
		}
	}

	logger.Info("已预置默认 VIP 套餐", zap.Int("count", len(plans)))
	return nil
}

