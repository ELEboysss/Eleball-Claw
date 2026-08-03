package repository

import (
	"time"

	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// SkillRuntimeRepo 秘技运行时数据访问
type SkillRuntimeRepo struct {
	db *gorm.DB
}

// NewSkillRuntimeRepo 创建运行时仓库
func NewSkillRuntimeRepo(db *gorm.DB) *SkillRuntimeRepo {
	return &SkillRuntimeRepo{db: db}
}

// CreateOrUpdate 创建或更新运行时记录
func (r *SkillRuntimeRepo) CreateOrUpdate(rt *model.SkillRuntime) error {
	return r.db.Save(rt).Error
}

// GetByID 根据 ID 查询运行时
func (r *SkillRuntimeRepo) GetByID(id string) (*model.SkillRuntime, error) {
	var rt model.SkillRuntime
	if err := r.db.First(&rt, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &rt, nil
}

// GetByDriverID 根据驱动别名查询运行时
func (r *SkillRuntimeRepo) GetByDriverID(driverID string) (*model.SkillRuntime, error) {
	var rt model.SkillRuntime
	if err := r.db.First(&rt, "driver_id = ?", driverID).Error; err != nil {
		return nil, err
	}
	return &rt, nil
}

// List 查询所有运行时
func (r *SkillRuntimeRepo) List() ([]*model.SkillRuntime, error) {
	var items []*model.SkillRuntime
	err := r.db.Order("created_at DESC").Find(&items).Error
	return items, err
}

// ListOnline 查询所有在线（且未禁用）的运行时
func (r *SkillRuntimeRepo) ListOnline() ([]*model.SkillRuntime, error) {
	var items []*model.SkillRuntime
	err := r.db.Where("status = ?", model.SkillRuntimeStatusOnline).Find(&items).Error
	return items, err
}

// ListByDeployment 按 deployment 类型查询
func (r *SkillRuntimeRepo) ListByDeployment(deployment model.SkillRuntimeDeployment) ([]*model.SkillRuntime, error) {
	var items []*model.SkillRuntime
	err := r.db.Where("deployment = ?", deployment).Find(&items).Error
	return items, err
}

// ListBySource 按 source 类型查询
func (r *SkillRuntimeRepo) ListBySource(source model.SkillRuntimeSource) ([]*model.SkillRuntime, error) {
	var items []*model.SkillRuntime
	err := r.db.Where("source = ?", source).Find(&items).Error
	return items, err
}

// UpdateStatus 更新运行时状态、版本、能力、心跳
func (r *SkillRuntimeRepo) UpdateStatus(rt *model.SkillRuntime) error {
	return r.db.Model(&model.SkillRuntime{}).
		Where("id = ?", rt.ID).
		Updates(map[string]interface{}{
			"status":         rt.Status,
			"version":        rt.Version,
			"capabilities":   rt.Capabilities,
			"last_heartbeat": rt.LastHeartbeat,
			"updated_at":     rt.UpdatedAt,
		}).Error
}

// Delete 删除运行时
func (r *SkillRuntimeRepo) Delete(id string) error {
	return r.db.Delete(&model.SkillRuntime{}, "id = ?", id).Error
}

// MigrateFromLegacy 从旧 modules/drivers 表迁移到 skill_runtimes。
// 仅在旧表存在且 skill_runtimes 中不存在对应记录时执行，幂等。
func (r *SkillRuntimeRepo) MigrateFromLegacy() error {
	migrator := r.db.Migrator()

	// 1. 迁移 modules 表
	if migrator.HasTable(&model.ModuleRecord{}) {
		if err := r.migrateFromModules(); err != nil {
			return err
		}
	}

	// 2. 迁移 drivers 表（仅补充 modules 未覆盖的独立驱动）
	if migrator.HasTable(&model.DriverRecord{}) {
		if err := r.migrateFromDrivers(); err != nil {
			return err
		}
	}

	return nil
}

func (r *SkillRuntimeRepo) migrateFromModules() error {
	moduleRepo := NewModuleRepo(r.db)
	modules, err := moduleRepo.List()
	if err != nil {
		return err
	}

	// 预加载 driver 别名映射
	driverMap := make(map[string]string)
	mcpConfigs := make(map[string]*model.MCPServerConfig)
	if migrator := r.db.Migrator(); migrator.HasTable(&model.DriverRecord{}) {
		drivers, _ := NewDriverRepo(r.db).List()
		for _, d := range drivers {
			if d.ModuleID != "" {
				driverMap[d.ModuleID] = d.ID
			}
			if d.TransportType == string(model.ModuleTransportTypeMCP) && d.MCPServerConfig != nil {
				mcpConfigs[d.ModuleID] = d.MCPServerConfig
			}
		}
	}

	for _, mod := range modules {
		if _, err := r.GetByID(mod.ID); err == nil {
			continue // 已存在，跳过
		}

		rt := &model.SkillRuntime{
			ID:             mod.ID,
			Name:           mod.Name,
			Description:    mod.Description,
			Source:         model.SkillRuntimeSourceMarketplace,
			Endpoint:       mod.URL,
			ImageRef:       mod.ImageRef,
			ImageDigest:    mod.ImageDigest,
			Signature:      mod.Signature,
			Official:       mod.Official,
			AuthToken:      mod.AuthToken,
			Version:        mod.Version,
			LastHeartbeat:  mod.LastHeartbeat,
			CreatedAt:      mod.CreatedAt,
			UpdatedAt:      mod.UpdatedAt,
			DriverID:       mod.ID,
		}
		if alias, ok := driverMap[mod.ID]; ok {
			rt.DriverID = alias
		}
		rt.SetCapabilities(mod.CapabilitiesList())

		switch mod.TransportType {
		case model.ModuleTransportTypeModule:
			rt.Transport = model.SkillRuntimeTransportExecute
			rt.Deployment = model.SkillRuntimeDeploymentDocker
			rt.DockerComposePath = "marketplace/" + mod.ID + "/docker-compose.yml"
		case model.ModuleTransportTypeMCP:
			rt.Transport = model.SkillRuntimeTransportMCPHTTP
			rt.Deployment = model.SkillRuntimeDeploymentDocker
			rt.DockerComposePath = "marketplace/" + mod.ID + "/docker-compose.yml"
			if cfg := mcpConfigs[mod.ID]; cfg != nil {
				rt.SetMCPServerConfig(cfg)
			}
		case model.ModuleTransportTypeRemoteURL:
			rt.Transport = model.SkillRuntimeTransportRawHTTP
			rt.Deployment = model.SkillRuntimeDeploymentNone
		default:
			rt.Transport = model.SkillRuntimeTransportExecute
			rt.Deployment = model.SkillRuntimeDeploymentDocker
		}

		switch mod.Status {
		case model.ModuleStatusOnline:
			rt.Status = model.SkillRuntimeStatusOnline
		case model.ModuleStatusDisabled:
			rt.Status = model.SkillRuntimeStatusDisabled
		default:
			rt.Status = model.SkillRuntimeStatusOffline
		}

		if err := r.CreateOrUpdate(rt); err != nil {
			return err
		}
	}
	return nil
}

func (r *SkillRuntimeRepo) migrateFromDrivers() error {
	drivers, err := NewDriverRepo(r.db).List()
	if err != nil {
		return err
	}

	for _, d := range drivers {
		// 已随 module 一起迁移过的跳过
		if d.ModuleID != "" {
			if _, err := r.GetByID(d.ModuleID); err == nil {
				// 补 DriverID（若旧 module 没有绑定 driver 别名）
				if rt, _ := r.GetByID(d.ModuleID); rt != nil && rt.DriverID == "" {
					rt.DriverID = d.ID
					_ = r.CreateOrUpdate(rt)
				}
				continue
			}
		}

		id := d.ModuleID
		if id == "" {
			id = d.ID
		}
		if id == "" {
			continue
		}

		if _, err := r.GetByID(id); err == nil {
			continue
		}

		rt := &model.SkillRuntime{
			ID:        id,
			Name:      d.Name,
			Source:    model.SkillRuntimeSourceMarketplace,
			DriverID:  d.ID,
			AuthToken: d.AuthToken,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		switch model.ModuleTransportType(d.TransportType) {
		case model.ModuleTransportTypeRemoteURL:
			rt.Transport = model.SkillRuntimeTransportRawHTTP
			rt.Deployment = model.SkillRuntimeDeploymentNone
			rt.Endpoint = d.Endpoint
		case model.ModuleTransportTypeMCP:
			rt.Transport = model.SkillRuntimeTransportMCPHTTP
			rt.Deployment = model.SkillRuntimeDeploymentExternal
			if d.MCPServerConfig != nil {
				rt.Endpoint = d.MCPServerConfig.URL
				rt.SetMCPServerConfig(d.MCPServerConfig)
			}
		default:
			// module 型但对应 module 记录缺失，降级为 external 占位
			rt.Transport = model.SkillRuntimeTransportExecute
			rt.Deployment = model.SkillRuntimeDeploymentExternal
		}

		if err := r.CreateOrUpdate(rt); err != nil {
			return err
		}
	}
	return nil
}
