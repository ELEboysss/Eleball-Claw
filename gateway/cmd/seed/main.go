// Eleball Gateway 初始化数据填充工具
//
// 用法：
//   cd gateway
//   go run ./cmd/seed
//
// 说明：
// 本工具仅用于全新部署或重置数据库后，向持久化注册表插入示例模块与 SKU。
// Gateway 启动时不再自动执行这些写入，模块/SKU 应由注册表数据库记录管理。
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/seed"
	"github.com/eleball/gateway/internal/service"
	sqlite "github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func main() {
	var (
		configPath = flag.String("config", "configs/config.yaml", "配置文件路径")
		onlyModules = flag.Bool("modules", false, "仅预置模块")
		onlySKUs    = flag.Bool("skus", false, "仅预置 SKU")
	)
	flag.Parse()

	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("初始化日志失败: %v", err)
	}
	defer logger.Sync()

	cfg, err := config.Load(*configPath)
	if err != nil {
		// 尝试从常见位置加载
		candidates := []string{
			"configs/config.yaml",
			filepath.Join("..", "configs", "config.yaml"),
			filepath.Join("..", "..", "configs", "config.yaml"),
		}
		for _, p := range candidates {
			cfg, err = config.Load(p)
			if err == nil {
				break
			}
		}
		if err != nil {
			logger.Fatal("加载配置失败", zap.Error(err))
		}
	}

	db, err := gorm.Open(sqlite.Open(cfg.Database.DSN), &gorm.Config{})
	if err != nil {
		logger.Fatal("连接数据库失败", zap.Error(err))
	}

	if err := db.AutoMigrate(
		&model.User{}, &model.Device{}, &model.Conversation{},
		&model.ChatConversation{}, &model.ChatMessage{}, &model.AgentSession{},
		&model.AgentSessionOutput{}, &model.TokenUsage{}, &model.BalanceTransaction{},
		&model.ActivityEvent{}, &model.Order{}, &model.AgentItem{}, &model.AgentPurchase{},
		&model.AgentReview{}, &model.AgentFavorite{}, &model.AgentUserTool{},
		&model.DeveloperAccount{}, &model.WithdrawalRecord{}, &model.ProviderApiKey{},
		&model.EleAgentModelConfig{}, &model.SystemSetting{}, &model.RechargePackage{},
		&model.VIPPlan{}, &model.VIPSubscription{}, &model.ModuleRecord{},
		&model.DriverRecord{}, &model.AgentUserCredential{},
	); err != nil {
		logger.Fatal("数据库迁移失败", zap.Error(err))
	}

	agentRepo := repository.NewAgentRepo(db)
	moduleRepo := repository.NewModuleRepo(db)
	driverRepo := repository.NewDriverRepo(db)
	moduleRegistry := service.NewModuleRegistry(&cfg.AgentReach)
	moduleSvc := service.NewModuleService(moduleRegistry, moduleRepo, driverRepo)
	moduleRegistry.SetRepo(moduleRepo)

	if *onlyModules {
		if err := seed.BuiltinModules(moduleSvc, logger); err != nil {
			logger.Fatal("预置模块失败", zap.Error(err))
		}
		fmt.Println("模块预置完成")
		return
	}

	if *onlySKUs {
		if err := seed.SyncOfficialSKUs(agentRepo, "cloud", logger); err != nil {
			logger.Fatal("同步官方 SKU 失败", zap.Error(err))
		}
		fmt.Println("官方 SKU 同步完成")
		return
	}

	if err := seed.All(agentRepo, moduleSvc, logger); err != nil {
		logger.Fatal("预置数据失败", zap.Error(err))
	}
	fmt.Println("全部预置完成")
	os.Exit(0)
}
