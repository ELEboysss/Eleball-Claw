// Package seed 提供可选的初始化数据填充工具。
//
// 注意：gateway 启动时会自动扫描 gateway/marketplace/ 下的内置模块目录，根据每个
// 目录中的 module.json 自动确保模块记录与驱动别名存在，避免未运行 cmd/seed 时
// 依赖这些模块的 SKU 显示“驱动未注册”。
// 官方 SKU 的 manifest 同样由 SyncOfficialSKUs 泛化扫描 marketplace/<mod>/skus/*.json
// 完成（不再需要每个模块手写一份 seed 函数）。完整的示例数据仍可由管理员显式运行
// cmd/seed 或 --seed 插入。
package seed

import (
	"encoding/json"
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

// All 执行全部默认初始化：内置模块 + 驱动别名 + 官方 SKU（云端侧）。
// --seed 模式调用；启动时云端也会单独调用 SyncOfficialSKUs（见 cmd/server）。
func All(agentRepo *repository.AgentRepo, moduleSvc *service.ModuleService, logger *zap.Logger) error {
	if err := AutoEnsureMarketplaceModules(moduleSvc, logger); err != nil {
		return err
	}
	if err := SyncOfficialSKUs(agentRepo, "cloud", logger); err != nil {
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

// AutoEnsureMarketplaceModules 自动扫描 marketplace 目录，根据 module.json
// 确保内置模块记录与驱动别名存在。新增官方内置模块时，只需在 marketplace/
// 下新增目录和 module.json，无需修改代码。
func AutoEnsureMarketplaceModules(svc *service.ModuleService, logger *zap.Logger) error {
	return svc.RescanMarketplace(logger)
}

// SyncOfficialSKUs 泛化扫描 marketplace/<module>/skus/*.json，按 manifest 同步官方 SKU。
// 替代原先每个官方模块手写的 AgentReachSKUs/FirecrawlSKUs/SearchWebSKUs：新增官方模块
// 只需在 marketplace/<mod>/ 下放 module.json（含 sku_scope）+ skus/*.json，无需改 Go。
//
// side 决定收录哪些模块（按 module.json 的 sku_scope）：
//   - "cloud": 收 sku_scope 为 "" / "cloud" / "both"（云端对外提供的官方 SKU，如 agent-reach/firecrawl）
//   - "claw":  收 sku_scope 为 "claw" / "both"（claw 本地官方 SKU，如 search-web）
//
// AgentItem.ID 约定 "{module}-{sku_file}"（与历史预置一致，不破坏已购记录）。
// 已存在且 manifest 与文件一致则跳过；manifest 变化（如新增 credentials/price）则同步
// manifest_json + name/desc/category/level/price，保留 rating/counts 等运行时统计。
// 文件加载失败（空/非 JSON/缺必填字段）时跳过该 SKU，绝不覆盖数据库有效数据。
func SyncOfficialSKUs(repo *repository.AgentRepo, side string, logger *zap.Logger) error {
	root := resolveMarketplaceRoot()
	if root == "" {
		logger.Warn("未找到 marketplace 目录，跳过官方 SKU 同步")
		return nil
	}
	adminID := "00000000-0000-0000-0000-000000000000"
	now := time.Now()
	created, synced, skipped := 0, 0, 0

	modEntries, err := os.ReadDir(root)
	if err != nil {
		logger.Warn("读取 marketplace 目录失败，跳过官方 SKU 同步", zap.Error(err))
		return nil
	}
	for _, modEntry := range modEntries {
		if !modEntry.IsDir() {
			continue
		}
		modName := modEntry.Name()
		scope, ok := readModuleSKUScope(filepath.Join(root, modName, "module.json"))
		if !ok {
			continue // 无 module.json 或解析失败，非官方模块目录
		}
		if !scopeIncluded(scope, side) {
			continue
		}
		skuDir := filepath.Join(root, modName, "skus")
		skuEntries, err := os.ReadDir(skuDir)
		if err != nil {
			continue // 无 skus/ 目录（如 stt、skill-maker）跳过
		}
		for _, sf := range skuEntries {
			if sf.IsDir() || !strings.HasSuffix(sf.Name(), ".json") {
				continue
			}
			agentID := modName + "-" + strings.TrimSuffix(sf.Name(), ".json")
			path := filepath.Join(skuDir, sf.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				logger.Warn("读取 SKU manifest 失败", zap.String("path", path), zap.Error(err))
				continue
			}
			fileStr := string(data)
			var m model.ToolManifest
			if err := json.Unmarshal(data, &m); err != nil {
				logger.Warn("解析 SKU manifest 失败", zap.String("path", path), zap.Error(err))
				continue
			}
			if m.ID == "" || m.Name == "" || m.Driver == "" {
				logger.Warn("SKU manifest 缺少必填字段，跳过", zap.String("path", path))
				continue
			}

			existing, err := repo.GetByID(agentID)
			if err == nil && existing != nil {
				if !shouldSyncManifest(existing.ManifestJSON, fileStr) {
					skipped++
					continue
				}
				existing.ManifestJSON = fileStr
				existing.Name = m.Name
				existing.Description = m.Description
				existing.Category = m.Category
				existing.Level = model.AgentLevel(m.Level)
				existing.PriceDanwan = m.PriceDanwan
				existing.PriceElegant = m.PriceElegant
				if err := repo.Update(existing); err != nil {
					logger.Warn("同步官方 SKU manifest 失败", zap.String("id", agentID), zap.Error(err))
				} else {
					synced++
				}
				continue
			}
			item := &model.AgentItem{
				ID:           agentID,
				Name:         m.Name,
				Description:  m.Description,
				Category:     m.Category,
				Level:        model.AgentLevel(m.Level),
				PriceDanwan:  m.PriceDanwan,
				PriceElegant: m.PriceElegant,
				ManifestJSON: fileStr,
				Status:       model.AgentStatusApproved,
				CreatorID:    adminID,
				CreatorName:  "官方",
				CreatedAt:    now,
			}
			if err := repo.Create(item); err != nil {
				logger.Warn("创建官方 SKU 失败", zap.String("id", agentID), zap.Error(err))
			} else {
				created++
			}
		}
	}
	logger.Info("已同步官方 SKU",
		zap.String("side", side),
		zap.Int("created", created), zap.Int("synced", synced), zap.Int("skipped", skipped))
	return nil
}

// resolveMarketplaceRoot 用候选路径定位 marketplace 根目录（纯磁盘，cloud/claw 通用）。
// 与历史 loadManifestJSON 的候选路径一致：覆盖仓库内开发（marketplace / gateway/marketplace）
// 与安装版（cwd 即安装根，marketplace 已由 EnsureMarketplaceRoot 落盘）。
func resolveMarketplaceRoot() string {
	for _, p := range []string{
		"marketplace",
		filepath.Join("..", "marketplace"),
		filepath.Join("..", "..", "marketplace"),
		filepath.Join("..", "..", "..", "marketplace"),
		"gateway/marketplace",
		filepath.Join("..", "gateway", "marketplace"),
	} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// readModuleSKUScope 读 module.json 的 sku_scope。返回 (scope, ok)；无 module.json
// 或缺 id/module_id 时 ok=false（视为非官方模块目录，跳过）。
// 支持新格式 id 与旧格式 module_id。
func readModuleSKUScope(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var mod struct {
		ID       string `json:"id"`
		ModuleID string `json:"module_id"` // 兼容旧格式
		SKUScope string `json:"sku_scope"`
	}
	if err := json.Unmarshal(data, &mod); err != nil {
		return "", false
	}
	id := mod.ID
	if id == "" {
		id = mod.ModuleID
	}
	if id == "" {
		return "", false
	}
	return mod.SKUScope, true
}

// scopeIncluded 判断该模块的 sku_scope 是否被当前 side 收录。
func scopeIncluded(scope, side string) bool {
	if side == "claw" {
		return scope == "claw" || scope == "both"
	}
	// side == "cloud" 或空：默认收录云端对外模块
	return scope == "" || scope == "cloud" || scope == "both"
}

// SkillMakerSKU 预置官方 Prompt 型秘技「秘技制造机」（driver=none）。
// 与 SearchWebSKUs 不同，它是纯 Prompt 型：SystemPrompt 非空、ManifestJSON 为空，
// 不注入工具列表、不走 tool_call，激活并绑定到 Agent Team 助手后，其 SystemPrompt
// 经 buildChildSystemPrompt 以「技能提示」注入子 agent，全程指导开发集市秘技模块。
// 免费上架（PriceDanwan=0）、无模块依赖 -> IsCloudPurchasedAgent 返回 false -> 免 VIP 门控，
// claw 本地走 0 元购买路径自动激活。参照 docs/tool-driver-guide.md §13 Prompt 型 SKU。
func SkillMakerSKU(repo *repository.AgentRepo, logger *zap.Logger) error {
	adminID := "00000000-0000-0000-0000-000000000000"
	now := time.Now()
	prompt := loadSkillMakerPrompt(logger)
	if prompt == "" {
		logger.Warn("秘技制造机 SystemPrompt 加载为空，跳过预置")
		return nil
	}
	item := &model.AgentItem{
		ID:           "skill-maker",
		Name:         "秘技制造机",
		Description:  "官方专家秘技：指导从零开发符合 Eleball 集市标准接口的秘技模块（定位、/health+/execute、module.json、ToolManifest、验证），激活并绑定助手后注入造模块方法论。",
		Category:     "开发",
		PriceDanwan:  0,
		PriceElegant: nil,
		Status:       model.AgentStatusApproved,
		Level:        model.AgentLevelXuan,
		CreatorID:    adminID,
		CreatorName:  "官方",
		CreatedAt:    now,
		SystemPrompt: prompt,
		// ManifestJSON 留空：Prompt 型秘技不携带可执行 manifest，不进工具列表。
	}

	existing, err := repo.GetByID(item.ID)
	if err == nil && existing != nil {
		if shouldSyncPrompt(existing.SystemPrompt, prompt) {
			existing.SystemPrompt = prompt
			if err := repo.Update(existing); err != nil {
				logger.Warn("同步秘技制造机 SystemPrompt 失败", zap.String("id", item.ID), zap.Error(err))
			} else {
				logger.Info("已同步秘技制造机 SystemPrompt")
			}
		}
		return nil
	}
	if err := repo.Create(item); err != nil {
		return err
	}
	logger.Info("已预置官方秘技「秘技制造机」", zap.String("id", item.ID))
	return nil
}

// shouldSyncPrompt 判断是否需要用文件中的 SystemPrompt 覆盖数据库值。
// 仅当文件加载成功（非空）且与数据库内容不一致时才覆盖，逻辑与 shouldSyncManifest 一致。
func shouldSyncPrompt(existing, fromFile string) bool {
	if fromFile == "" {
		return false
	}
	return strings.TrimSpace(existing) != strings.TrimSpace(fromFile)
}

// loadSkillMakerPrompt 加载秘技制造机的 SystemPrompt 纯文本（marketplace/skill-maker/SKILL.md）。
// 优先从内嵌 marketplace.FS 读取（单文件二进制分发可用），失败回退文件系统候选路径。
// 与 loadManifestJSON 不同：返回纯文本而非 JSON，空串表示加载失败。
func loadSkillMakerPrompt(logger *zap.Logger) string {
	const relativePath = "skill-maker/SKILL.md"
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
	logger.Warn("无法加载秘技制造机 SystemPrompt", zap.String("path", relativePath))
	return ""
}

// shouldSyncManifest 判断是否需要用 marketplace 文件中的 manifest 覆盖数据库值。
// 仅当文件加载成功（非空、非 "{}"）且与数据库内容不一致时才覆盖：
// 既能让历史预置的旧 manifest 自动补齐后续新增字段（如 credentials/price），
// 又避免容器内缺少 marketplace 目录时用空 JSON 冲掉有效数据。
func shouldSyncManifest(existing, fromFile string) bool {
	if fromFile == "" || fromFile == "{}" {
		return false
	}
	return strings.TrimSpace(existing) != strings.TrimSpace(fromFile)
}
