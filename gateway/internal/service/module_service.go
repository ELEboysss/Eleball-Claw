package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"go.uber.org/zap"
)

// ModuleService 集市模块与动态驱动业务层
// 负责模块注册、注销、健康刷新，以及驱动别名到运行时的映射。
type ModuleService struct {
	registry   *ModuleRegistry
	moduleRepo *repository.ModuleRepo
	driverRepo *repository.DriverRepo
}

// NewModuleService 创建模块业务服务
func NewModuleService(registry *ModuleRegistry, moduleRepo *repository.ModuleRepo, driverRepo *repository.DriverRepo) *ModuleService {
	return &ModuleService{
		registry:   registry,
		moduleRepo: moduleRepo,
		driverRepo: driverRepo,
	}
}

// RegisterModule 管理后台注册/更新模块
func (s *ModuleService) RegisterModule(record *model.ModuleRecord) error {
	if s.registry == nil {
		return errors.New("ModuleRegistry 未初始化")
	}
	return s.registry.RegisterRecord(record)
}

// UnregisterModule 注销模块
func (s *ModuleService) UnregisterModule(moduleID string) error {
	if s.registry == nil {
		return errors.New("ModuleRegistry 未初始化")
	}
	return s.registry.Unregister(moduleID)
}

// ListModules 列出所有模块（返回实时健康状态）
func (s *ModuleService) ListModules() ([]*model.ModuleRecord, error) {
	if s.registry == nil {
		return nil, errors.New("ModuleRegistry 未初始化")
	}
	statuses := s.registry.List()
	items := make([]*model.ModuleRecord, 0, len(statuses))
	for _, st := range statuses {
		rec, err := s.moduleRepo.GetByID(st.ModuleID)
		if err != nil || rec == nil {
			rec = &model.ModuleRecord{
				ID:            st.ModuleID,
				Name:          st.ModuleID,
				TransportType: model.ModuleTransportTypeModule,
			}
		}
		rec.Status = model.ModuleStatusOffline
		if st.Online {
			rec.Status = model.ModuleStatusOnline
		}
		rec.Version = st.Version
		rec.SetCapabilities(st.Capabilities)
		rec.HealthError = st.Error
		now := time.Now()
		rec.LastHeartbeat = &now
		items = append(items, rec)
	}
	return items, nil
}

// GetModule 获取单个模块
func (s *ModuleService) GetModule(moduleID string) (*model.ModuleRecord, error) {
	if s.moduleRepo == nil {
		return nil, errors.New("ModuleRepo 未初始化")
	}
	return s.moduleRepo.GetByID(moduleID)
}

// RefreshModule 强制探测模块健康状态（忽略缓存）
func (s *ModuleService) RefreshModule(moduleID string) *ModuleStatus {
	if s.registry == nil {
		return nil
	}
	return s.registry.ForceProbe(moduleID)
}

// RegisterModuleFromPlugin 插件自助注册
// 返回注册成功的 module_id（可能由网关自动生成）。
func (s *ModuleService) RegisterModuleFromPlugin(req *model.ModuleRegisterRequest, providedToken string) (string, error) {
	if s.registry == nil {
		return "", errors.New("ModuleRegistry 未初始化")
	}
	if err := s.registry.RegisterFromPlugin(req, providedToken); err != nil {
		return "", err
	}
	return req.ModuleID, nil
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

// RescanMarketplace 运行时重新扫描 marketplace/ 目录，根据 module.json
// 自动补齐官方内置模块记录与驱动别名。新增官方模块时无需重启 gateway，
// 也无需走后台手动注册。
func (s *ModuleService) RescanMarketplace(logger *zap.Logger) error {
	root := findMarketplaceRoot()
	if root == "" {
		if logger != nil {
			logger.Warn("未找到 marketplace 目录，跳过内置模块自动补齐")
		}
		return nil
	}
	return s.ensureMarketplaceModules(root, logger)
}

// findMarketplaceRoot 查找 marketplace 根目录，支持从 gateway/ 或项目根目录启动。
func findMarketplaceRoot() string {
	for _, p := range []string{"marketplace", "gateway/marketplace"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// ensureMarketplaceModules 从指定根目录扫描内置模块并补齐到 registry/DB。
func (s *ModuleService) ensureMarketplaceModules(root string, logger *zap.Logger) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "module.json")
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		var m marketplaceModuleManifest
		if err := json.Unmarshal(data, &m); err != nil {
			if logger != nil {
				logger.Warn("解析 module.json 失败", zap.String("path", path), zap.Error(err))
			}
			continue
		}
		if m.ModuleID == "" || m.Driver.ID == "" {
			if logger != nil {
				logger.Warn("module.json 缺少必填字段", zap.String("path", path))
			}
			continue
		}

		// 确保模块记录存在
		existingModule, err := s.GetModule(m.ModuleID)
		if err != nil || existingModule == nil {
			rec := &model.ModuleRecord{
				ID:            m.ModuleID,
				Name:          m.Name,
				Description:   m.Description,
				URL:           m.URL,
				TransportType: model.ModuleTransportTypeModule,
				Status:        model.ModuleStatusOffline,
				Version:       "",
			}
			rec.SetCapabilities(m.Capabilities)
			if err := s.RegisterModule(rec); err != nil {
				if logger != nil {
					logger.Warn("自动补齐内置模块失败", zap.String("module_id", m.ModuleID), zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Info("已自动补齐内置模块", zap.String("module_id", m.ModuleID))
				}
			}
		}

		// 确保驱动别名存在
		existingDriver, err := s.ResolveDriver(m.Driver.ID)
		if err != nil || existingDriver == nil {
			if err := s.RegisterDriver(&model.DriverRegisterRequest{
				ID:            m.Driver.ID,
				Name:          m.Driver.Name,
				Description:   m.Driver.Description,
				TransportType: string(model.ModuleTransportTypeModule),
				ModuleID:      m.ModuleID,
			}); err != nil {
				if logger != nil {
					logger.Warn("自动补齐内置驱动失败", zap.String("driver_id", m.Driver.ID), zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Info("已自动补齐内置驱动", zap.String("driver_id", m.Driver.ID), zap.String("module_id", m.ModuleID))
				}
			}
		}
	}
	return nil
}

// RegisterDriver 注册/更新驱动映射
// 对于第三方模块型驱动，允许 module_id 为空，但建议设置 auth_token；
// 开发者后续可凭 auth_token 自助注册模块，网关会自动回填 module_id。
func (s *ModuleService) RegisterDriver(req *model.DriverRegisterRequest) error {
	if s.driverRepo == nil {
		return errors.New("DriverRepo 未初始化")
	}
	if req.TransportType == string(model.ModuleTransportTypeModule) && req.ModuleID == "" && req.AuthToken == "" {
		return errors.New("module 型驱动必须指定 module_id 或 auth_token")
	}
	if req.TransportType == string(model.ModuleTransportTypeRemoteURL) && req.Endpoint == "" {
		return errors.New("remote_url 型驱动必须指定 endpoint")
	}

	// 非空 auth_token 需保证全局唯一（空字符串允许重复，用于官方内置驱动）
	if req.AuthToken != "" {
		existing, err := s.driverRepo.GetByAuthToken(req.AuthToken)
		if err == nil && existing != nil && existing.ID != req.ID {
			return errors.New("auth_token 已被其他驱动使用")
		}
	}

	now := time.Now()
	record := &model.DriverRecord{
		ID:            req.ID,
		Name:          req.Name,
		Description:   req.Description,
		TransportType: req.TransportType,
		ModuleID:      req.ModuleID,
		Endpoint:      req.Endpoint,
		AuthToken:     req.AuthToken,
		SchemaJSON:    req.SchemaJSON,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	return s.driverRepo.CreateOrUpdate(record)
}

// UnregisterDriver 注销驱动映射
func (s *ModuleService) UnregisterDriver(driverID string) error {
	if s.driverRepo == nil {
		return errors.New("DriverRepo 未初始化")
	}
	return s.driverRepo.Delete(driverID)
}

// ListDrivers 列出所有驱动映射
func (s *ModuleService) ListDrivers() ([]*model.DriverRecord, error) {
	if s.driverRepo == nil {
		return nil, errors.New("DriverRepo 未初始化")
	}
	return s.driverRepo.List()
}

// ResolveDriver 根据驱动名解析动态驱动记录
func (s *ModuleService) ResolveDriver(driverID string) (*model.DriverRecord, error) {
	if s.driverRepo == nil {
		return nil, errors.New("DriverRepo 未初始化")
	}
	return s.driverRepo.GetByID(driverID)
}

// ResolveDriverByAuthToken 根据 auth_token 解析动态驱动记录
func (s *ModuleService) ResolveDriverByAuthToken(token string) (*model.DriverRecord, error) {
	if s.driverRepo == nil {
		return nil, errors.New("DriverRepo 未初始化")
	}
	return s.driverRepo.GetByAuthToken(token)
}

// BindDriverModule 将驱动别名绑定到指定模块
func (s *ModuleService) BindDriverModule(driverID, moduleID string) error {
	if s.driverRepo == nil {
		return errors.New("DriverRepo 未初始化")
	}
	return s.driverRepo.UpdateModuleID(driverID, moduleID)
}
