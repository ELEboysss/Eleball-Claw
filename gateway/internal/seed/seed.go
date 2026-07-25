// Package seed 提供可选的初始化数据填充工具。
//
// 注意：gateway 启动时会自动扫描 gateway/marketplace/ 下的内置模块目录，根据每个
// 目录中的 module.json 自动确保模块记录与驱动别名存在，避免未运行 cmd/seed 时
// 依赖这些模块的 SKU 显示“驱动未注册”。
// 完整的示例 SKU 与演示数据仍由管理员显式运行 cmd/seed 或 --seed 插入。
package seed

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/marketplace"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/service"
	"go.uber.org/zap"
)

// All 执行全部默认初始化：内置模块 + 驱动别名 + 示例 SKU。
func All(agentRepo *repository.AgentRepo, moduleSvc *service.ModuleService, logger *zap.Logger) error {
	if err := AutoEnsureMarketplaceModules(moduleSvc, logger); err != nil {
		return err
	}
	if err := AgentReachSKUs(agentRepo, logger); err != nil {
		return err
	}
	if err := FirecrawlSKUs(agentRepo, logger); err != nil {
		return err
	}
	return nil
}

// BuiltinModules 预置内置集市模块记录。
// 通过扫描 marketplace/ 下的 module.json 自动补齐，支持新增官方模块而无需改代码。
func BuiltinModules(svc *service.ModuleService, logger *zap.Logger) error {
	return svc.RescanMarketplace(logger)
}

// BuiltinDrivers 预置官方驱动别名映射。
// 扫描 marketplace/ 下的 module.json 时已经同步补齐驱动别名，此处保留函数签名兼容旧调用。
func BuiltinDrivers(svc *service.ModuleService, logger *zap.Logger) error {
	return svc.RescanMarketplace(logger)
}

// marketplaceModuleManifest 内置模块目录中的 module.json 定义。
type marketplaceModuleManifest struct {
	ModuleID      string   `json:"module_id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	URL           string   `json:"url"`
	TransportType string   `json:"transport_type"`
	Capabilities  []string `json:"capabilities"`
	Driver        struct {
		ID          string `json:"driver_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"driver"`
}

// AutoEnsureMarketplaceModules 自动扫描 marketplace 目录，根据 module.json
// 确保内置模块记录与驱动别名存在。新增官方内置模块时，只需在 marketplace/
// 下新增目录和 module.json，无需修改代码。
func AutoEnsureMarketplaceModules(svc *service.ModuleService, logger *zap.Logger) error {
	return svc.RescanMarketplace(logger)
}

// AgentReachSKUs 预置默认 Agent-Reach 集市 SKU。
// 官方 SKU 的 manifest 以 marketplace/ 下的 JSON 文件为准：已存在且与文件一致时跳过；
// 若 manifest 为空或与文件不一致（如历史预置的旧 manifest 缺少后续新增的 credentials
// 等字段），则重新同步。文件加载失败（返回空 JSON）时绝不覆盖数据库中的有效数据。
func AgentReachSKUs(repo *repository.AgentRepo, logger *zap.Logger) error {
	adminID := "00000000-0000-0000-0000-000000000000"
	now := time.Now()
	items := []*model.AgentItem{
		{
			ID:            "agent-reach-web",
			Name:          "全网洞察（基础版）",
			Description:   "基于 Agent-Reach 的网页阅读与全网语义搜索，零配置即用",
			Category:      "互联网",
			PriceDanwan:   0,
			AvgRating:     4.7,
			PurchaseCount: 320,
			FavoriteCount: 80,
			Status:        model.AgentStatusApproved,
			Level:         2,
			CreatorID:     adminID,
			CreatorName:   "官方",
			CreatedAt:     now,
			ManifestJSON:  loadManifestJSON("agent-reach/skus/web.json", logger),
		},
		{
			ID:            "agent-reach-video",
			Name:          "视频解析器",
			Description:   "基于 Agent-Reach 的 YouTube/B站 字幕提取与搜索",
			Category:      "互联网",
			PriceDanwan:   200,
			AvgRating:     4.8,
			PurchaseCount: 156,
			FavoriteCount: 45,
			Status:        model.AgentStatusApproved,
			Level:         3,
			CreatorID:     adminID,
			CreatorName:   "官方",
			CreatedAt:     now,
			ManifestJSON:  loadManifestJSON("agent-reach/skus/video.json", logger),
		},
		{
			ID:            "agent-reach-social",
			Name:          "社媒雷达",
			Description:   "基于 Agent-Reach 的社媒内容搜索与阅读，支持 Twitter、小红书、Reddit、B站（需配置对应平台登录态 Cookie）",
			Category:      "互联网",
			PriceDanwan:   150,
			AvgRating:     4.6,
			PurchaseCount: 98,
			FavoriteCount: 30,
			Status:        model.AgentStatusApproved,
			Level:         3,
			CreatorID:     adminID,
			CreatorName:   "官方",
			CreatedAt:     now,
			ManifestJSON:  loadManifestJSON("agent-reach/skus/social.json", logger),
		},
		{
			ID:            "agent-reach-github",
			Name:          "开发者雷达",
			Description:   "基于 Agent-Reach 的 GitHub 仓库查看与代码搜索",
			Category:      "开发",
			PriceDanwan:   80,
			AvgRating:     4.5,
			PurchaseCount: 210,
			FavoriteCount: 60,
			Status:        model.AgentStatusApproved,
			Level:         2,
			CreatorID:     adminID,
			CreatorName:   "官方",
			CreatedAt:     now,
			ManifestJSON:  loadManifestJSON("agent-reach/skus/github.json", logger),
		},
	}

	created, synced := 0, 0
	for _, item := range items {
		existing, err := repo.GetByID(item.ID)
		if err == nil && existing != nil {
			if shouldSyncManifest(existing.ManifestJSON, item.ManifestJSON) {
				existing.ManifestJSON = item.ManifestJSON
				if err := repo.Update(existing); err != nil {
					logger.Warn("同步 Agent-Reach SKU manifest 失败", zap.String("id", item.ID), zap.Error(err))
				} else {
					synced++
				}
			}
			continue
		}
		if err := repo.Create(item); err != nil {
			return err
		}
		created++
	}

	logger.Info("已预置默认 Agent-Reach 秘技", zap.Int("created", created), zap.Int("synced", synced))
	return nil
}

// FirecrawlSKUs 预置 Firecrawl 拆分后的 SKU（每个 action 一个 SKU）。
func FirecrawlSKUs(repo *repository.AgentRepo, logger *zap.Logger) error {
	adminID := "00000000-0000-0000-0000-000000000000"
	now := time.Now()

	items := []*model.AgentItem{
		{
			ID:            "firecrawl-scrape",
			Name:          "Firecrawl 网页抓取",
			Description:   "将任意网页转换为干净 Markdown，返回标题、URL、描述等元数据",
			Category:      "互联网",
			PriceDanwan:   50,
			AvgRating:     4.8,
			PurchaseCount: 0,
			FavoriteCount: 0,
			Status:        model.AgentStatusApproved,
			Level:         2,
			CreatorID:     adminID,
			CreatorName:   "官方",
			CreatedAt:     now,
			ManifestJSON:  loadManifestJSON("firecrawl/skus/scrape.json", logger),
		},
		{
			ID:            "firecrawl-crawl",
			Name:          "Firecrawl 批量爬取",
			Description:   "对指定网站启动批量爬取任务，返回任务 ID",
			Category:      "互联网",
			PriceDanwan:   150,
			AvgRating:     4.7,
			PurchaseCount: 0,
			FavoriteCount: 0,
			Status:        model.AgentStatusApproved,
			Level:         3,
			CreatorID:     adminID,
			CreatorName:   "官方",
			CreatedAt:     now,
			ManifestJSON:  loadManifestJSON("firecrawl/skus/crawl.json", logger),
		},
		{
			ID:            "firecrawl-extract",
			Name:          "Firecrawl 结构化提取",
			Description:   "按 JSON Schema 从网页中提取结构化数据",
			Category:      "互联网",
			PriceDanwan:   100,
			AvgRating:     4.6,
			PurchaseCount: 0,
			FavoriteCount: 0,
			Status:        model.AgentStatusApproved,
			Level:         3,
			CreatorID:     adminID,
			CreatorName:   "官方",
			CreatedAt:     now,
			ManifestJSON:  loadManifestJSON("firecrawl/skus/extract.json", logger),
		},
	}

	created, synced := 0, 0
	for _, item := range items {
		existing, err := repo.GetByID(item.ID)
		if err == nil && existing != nil {
			if shouldSyncManifest(existing.ManifestJSON, item.ManifestJSON) {
				existing.ManifestJSON = item.ManifestJSON
				if err := repo.Update(existing); err != nil {
					logger.Warn("同步 Firecrawl SKU manifest 失败", zap.String("id", item.ID), zap.Error(err))
				} else {
					synced++
				}
			}
			continue
		}
		if err := repo.Create(item); err != nil {
			return err
		}
		created++
	}

	logger.Info("已预置 Firecrawl SKU", zap.Int("created", created), zap.Int("synced", synced))
	return nil
}

// SearchWebSKUs 预置本地 search-web 官方搜索 SKU（百度千帆 / 必应两变体）。
// 免费上架（PriceDanwan=0），claw 本地可直接走免费购买路径（0 元记录）并激活；
// 模块 install_source 为空（本地预置），购买/激活均无 VIP 门禁。
func SearchWebSKUs(repo *repository.AgentRepo, logger *zap.Logger) error {
	adminID := "00000000-0000-0000-0000-000000000000"
	now := time.Now()
	items := []*model.AgentItem{
		{
			ID:            "search-web-baidu",
			Name:          "百度千帆搜索",
			Description:   "基于本地 search-web 模块的百度千帆 AI 搜索（需在秘技卡片配置百度千帆 API Key）",
			Category:      "搜索",
			PriceDanwan:   0,
			PriceElegant:  nil,
			Status:        model.AgentStatusApproved,
			Level:         model.AgentLevelHuang,
			CreatorID:     adminID,
			CreatorName:   "官方",
			CreatedAt:     now,
			ManifestJSON:  loadManifestJSON("search-web/skus/baidu.json", logger),
		},
		{
			ID:            "search-web-bing",
			Name:          "必应搜索",
			Description:   "基于本地 search-web 模块的必应（Bing）网页搜索（需在秘技卡片配置 Bing Search API Key）",
			Category:      "搜索",
			PriceDanwan:   0,
			PriceElegant:  nil,
			Status:        model.AgentStatusApproved,
			Level:         model.AgentLevelHuang,
			CreatorID:     adminID,
			CreatorName:   "官方",
			CreatedAt:     now,
			ManifestJSON:  loadManifestJSON("search-web/skus/bing.json", logger),
		},
	}

	created, synced := 0, 0
	for _, item := range items {
		existing, err := repo.GetByID(item.ID)
		if err == nil && existing != nil {
			if shouldSyncManifest(existing.ManifestJSON, item.ManifestJSON) {
				existing.ManifestJSON = item.ManifestJSON
				if err := repo.Update(existing); err != nil {
					logger.Warn("同步 SearchWeb SKU manifest 失败", zap.String("id", item.ID), zap.Error(err))
				} else {
					synced++
				}
			}
			continue
		}
		if err := repo.Create(item); err != nil {
			return err
		}
		created++
	}

	logger.Info("已预置 SearchWeb 官方搜索秘技", zap.Int("created", created), zap.Int("synced", synced))
	return nil
}

// shouldSyncManifest 判断是否需要用 marketplace 文件中的 manifest 覆盖数据库值。
// 仅当文件加载成功（非空、非 "{}"）且与数据库内容不一致时才覆盖：
// 既能让历史预置的旧 manifest 自动补齐后续新增字段（如 credentials），
// 又避免容器内缺少 marketplace 目录时用空 JSON 冲掉有效数据。
func shouldSyncManifest(existing, fromFile string) bool {
	if fromFile == "" || fromFile == "{}" {
		return false
	}
	return strings.TrimSpace(existing) != strings.TrimSpace(fromFile)
}

// loadManifestJSON 加载指定 SKU 的 ToolManifest 原始 JSON。
// 优先从内嵌的 marketplace.FS 读取（relativePath 形如 search-web/skus/baidu.json，
// 单文件二进制分发时无磁盘 marketplace 目录也可用），失败再回退文件系统候选路径。
func loadManifestJSON(relativePath string, logger *zap.Logger) string {
	if data, err := marketplace.FS.ReadFile(relativePath); err == nil {
		return string(data)
	}
	candidates := []string{
		filepath.Join("marketplace", relativePath),
		filepath.Join("..", "marketplace", relativePath),
		filepath.Join("..", "..", "marketplace", relativePath),
		filepath.Join("..", "..", "..", "marketplace", relativePath),
		filepath.Join("gateway", "marketplace", relativePath),
		filepath.Join("..", "gateway", "marketplace", relativePath),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data)
		}
	}
	logger.Warn("无法加载 SKU manifest，将使用空 JSON", zap.String("path", relativePath))
	return "{}"
}
