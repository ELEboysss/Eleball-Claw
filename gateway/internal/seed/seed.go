// Package seed 提供可选的初始化数据填充工具。
//
// 注意：claw 启动时会自动扫描 marketplace/ 目录（RescanPackage，T2.2 统一物化），
// 一次扫描同时补齐 SkillRuntime（tool/mcp 运行时）与 AgentItem（手写 skus/*.json +
// 派生 + prompt-only SKILL.md），不再区分「模块补齐」与「官方 SKU 同步」两步。
// 本包仅保留与历史调用点兼容的薄包装（cmd/server、cmd/claw-server、cmd/seed --seed），
// 实际逻辑全部收敛到 service.ModuleService.RescanPackage。
package seed

import (
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/service"
	"go.uber.org/zap"
)

// rescanMarketplace claw 统一物化入口：先播种内嵌官方模块（首次运行写 home，只补缺失
// 不覆盖用户修改），再 RescanPackage 一次扫描物化 SkillRuntime + AgentItem。
// 云端无此步（marketplace 目录随仓库分发）；播种失败不阻断扫描（与旧 RescanMarketplace 等价）。
func rescanMarketplace(svc *service.ModuleService, logger *zap.Logger) error {
	if _, err := service.EnsureMarketplaceRoot(); err != nil {
		if logger != nil {
			logger.Warn("初始化 marketplace 目录失败，跳过模块物化", zap.Error(err))
		}
		return nil
	}
	return svc.RescanPackage("claw", logger)
}

// All 执行全部默认初始化：内置模块 + 驱动别名 + 官方 SKU（claw 侧）。
// --seed 模式调用；启动时 claw 也会调用 AutoEnsureMarketplaceModules（见 cmd/claw-server）。
// T2.2 收敛：RescanPackage 一次扫描物化 SkillRuntime + AgentItem，等价旧两步。
func All(agentRepo *repository.AgentRepo, moduleSvc *service.ModuleService, logger *zap.Logger) error {
	return rescanMarketplace(moduleSvc, logger)
}

// BuiltinModules 预置内置集市模块记录（SkillRuntime + AgentItem 一次物化）。
func BuiltinModules(svc *service.ModuleService, logger *zap.Logger) error {
	return rescanMarketplace(svc, logger)
}

// BuiltinDrivers 预置官方驱动别名映射（驱动已并入 SkillRuntime，与 BuiltinModules 等价）。
func BuiltinDrivers(svc *service.ModuleService, logger *zap.Logger) error {
	return rescanMarketplace(svc, logger)
}

// AutoEnsureMarketplaceModules 自动扫描 marketplace 目录，确保模块记录与 SKU 存在。
// 新增官方内置模块时，只需在 marketplace/ 下新增目录（package.json 或 module.json），无需修改代码。
func AutoEnsureMarketplaceModules(svc *service.ModuleService, logger *zap.Logger) error {
	return rescanMarketplace(svc, logger)
}
