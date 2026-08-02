package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/marketplace"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ModuleService 集市模块与动态驱动业务层
// 负责模块注册、注销、健康刷新，以及驱动别名到运行时的映射。
type ModuleService struct {
	registry   *ModuleRegistry
	moduleRepo *repository.ModuleRepo
	driverRepo *repository.DriverRepo
	// claw 云端秘技安装后同步本地 AgentItem/AgentPurchase（cmd/server 不注入则跳过）
	agentRepo *repository.AgentRepo
	// P4：第三方模块镜像安装器（拉镜像 + 签名校验 + 启动容器）。官方预置模块不经此安装器。
	installer *ImageInstaller
}

// NewModuleService 创建模块业务服务
func NewModuleService(registry *ModuleRegistry, moduleRepo *repository.ModuleRepo, driverRepo *repository.DriverRepo) *ModuleService {
	return &ModuleService{
		registry:   registry,
		moduleRepo: moduleRepo,
		driverRepo: driverRepo,
		installer:  NewImageInstaller(""), // 自动探测 docker/podman
	}
}

// SetAgentRepo 注入秘技仓库（claw 用：云端秘技安装后落本地 AgentItem/AgentPurchase）
func (s *ModuleService) SetAgentRepo(repo *repository.AgentRepo) {
	s.agentRepo = repo
}

// ModuleImageMeta 第三方模块容器镜像信息（云端 ModuleInstallMeta.image）
type ModuleImageMeta struct {
	Registry   string `json:"registry"`
	Repository string `json:"repository"`
	Tag        string `json:"tag,omitempty"`
	Digest     string `json:"digest,omitempty"` // sha256:... 内容寻址
}

// ModuleInstallMeta 云端秘技拉取接口返回的单项（见 specs/api-schema.yml ModuleInstallMeta）。
// claw 据此安装到本地：official=true 直接激活预置；否则走 ImageInstaller 拉镜像 + 签名校验。
type ModuleInstallMeta struct {
	ModuleID      string          `json:"module_id"`
	// AgentID 云端秘技（AgentItem）ID；安装后据此在本地 upsert AgentItem，为空回退 manifest.id
	AgentID       string          `json:"agent_id,omitempty"`
	Name          string          `json:"name"`
	Description    string          `json:"description"`
	Version       string          `json:"version"`
	TransportType string          `json:"transport_type"`
	DriverID      string          `json:"driver_id,omitempty"`
	Official      bool            `json:"official"`
	Capabilities  []string        `json:"capabilities,omitempty"`
	Image         *ModuleImageMeta `json:"image,omitempty"`
	Signature     string          `json:"signature,omitempty"`
	Manifest      json.RawMessage `json:"manifest,omitempty"`
	AuthToken     string          `json:"auth_token,omitempty"`
	UpdatedAt     string          `json:"updated_at,omitempty"`
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
	ModuleID        string                 `json:"module_id"`
	Name            string                 `json:"name"`
	Description     string                 `json:"description"`
	URL             string                 `json:"url"`
	TransportType   string                 `json:"transport_type"`
	Capabilities    []string               `json:"capabilities"`
	MCPServerConfig *model.MCPServerConfig `json:"mcp_server_config,omitempty"`
	Driver          struct {
		ID          string `json:"driver_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"driver"`
}

// RescanMarketplace 运行时重新扫描 marketplace 目录，根据 module.json
// 自动补齐官方内置模块记录与驱动别名。新增官方模块时无需重启 gateway，
// 也无需走后台手动注册。
func (s *ModuleService) RescanMarketplace(logger *zap.Logger) error {
	root, err := EnsureMarketplaceRoot()
	if err != nil {
		if logger != nil {
			logger.Warn("初始化 marketplace 目录失败，跳过内置模块自动补齐", zap.Error(err))
		}
		return nil
	}
	if root == "" {
		if logger != nil {
			logger.Warn("未找到 marketplace 目录，跳过内置模块自动补齐")
		}
		return nil
	}
	return s.ensureMarketplaceModules(root, logger)
}

// ResolveMarketplaceRoot 解析 marketplace 根目录：
//  1. CLAW_MARKETPLACE_DIR 环境变量（显式指定，优先级最高）；
//  2. 当前目录下的 marketplace / gateway/marketplace（开发模式，从仓库内启动）；
//  3. ~/.eleball-claw/marketplace（安装版默认 home，首次使用时播种官方模块）。
func ResolveMarketplaceRoot() string {
	if dir := strings.TrimSpace(os.Getenv("CLAW_MARKETPLACE_DIR")); dir != "" {
		return dir
	}
	for _, p := range []string{"marketplace", "gateway/marketplace"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".eleball-claw", "marketplace")
}

// EnsureMarketplaceRoot 解析 marketplace 根目录，并把内嵌官方模块播种进去
// （只补缺失文件，不覆盖用户修改）。返回根目录；无法确定根目录时返回空串。
func EnsureMarketplaceRoot() (string, error) {
	root := ResolveMarketplaceRoot()
	if root == "" {
		return "", nil
	}
	if _, err := marketplace.SeedOfficial(root); err != nil {
		return "", err
	}
	return root, nil
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

		// 确定传输类型，默认 module；mcp / remote_url 需要特殊处理。
		transportType := model.ModuleTransportTypeModule
		if m.TransportType != "" {
			transportType = model.ModuleTransportType(m.TransportType)
		}
		moduleURL := m.URL
		if transportType == model.ModuleTransportTypeMCP && moduleURL == "" && m.MCPServerConfig != nil {
			moduleURL = m.MCPServerConfig.URL
		}

		// 确保模块记录存在；已存在时同步 module.json 的 url/名称/描述/能力
		//（官方模块以 marketplace 文件为准，兼容旧版本登记的 URL 变更，如 docker 内网名改宿主机端口）
		existingModule, err := s.GetModule(m.ModuleID)
		if err != nil || existingModule == nil {
			rec := &model.ModuleRecord{
				ID:            m.ModuleID,
				Name:          m.Name,
				Description:   m.Description,
				URL:           moduleURL,
				TransportType: transportType,
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
					logger.Info("已自动补齐内置模块", zap.String("module_id", m.ModuleID), zap.String("transport_type", string(transportType)))
				}
			}
		} else {
			caps := model.ModuleRecord{}
			caps.SetCapabilities(m.Capabilities)
			if existingModule.URL != moduleURL || existingModule.Name != m.Name ||
				existingModule.Description != m.Description || existingModule.Capabilities != caps.Capabilities {
				if s.moduleRepo != nil {
					if err := s.moduleRepo.SyncManifest(m.ModuleID, moduleURL, m.Name, m.Description, caps.Capabilities); err != nil && logger != nil {
						logger.Warn("同步内置模块清单失败", zap.String("module_id", m.ModuleID), zap.Error(err))
					}
				}
			}
		}

		// 确保驱动别名存在
		existingDriver, err := s.ResolveDriver(m.Driver.ID)
		if err != nil || existingDriver == nil {
			req := &model.DriverRegisterRequest{
				ID:            m.Driver.ID,
				Name:          m.Driver.Name,
				Description:   m.Driver.Description,
				TransportType: string(transportType),
				ModuleID:      m.ModuleID,
			}
			if transportType == model.ModuleTransportTypeMCP {
				req.ModuleID = "" // MCP 驱动不绑定 module_id，直接调用 mcp_server_config.url
				req.MCPServerConfig = m.MCPServerConfig
			}
			if transportType == model.ModuleTransportTypeRemoteURL {
				req.Endpoint = moduleURL
			}
			if err := s.RegisterDriver(req); err != nil {
				if logger != nil {
					logger.Warn("自动补齐内置驱动失败", zap.String("driver_id", m.Driver.ID), zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Info("已自动补齐内置驱动", zap.String("driver_id", m.Driver.ID), zap.String("module_id", m.ModuleID), zap.String("transport_type", string(transportType)))
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
	if req.TransportType == string(model.ModuleTransportTypeMCP) && (req.MCPServerConfig == nil || req.MCPServerConfig.URL == "") {
		return errors.New("mcp 型驱动必须指定 mcp_server_config.url")
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
		ID:              req.ID,
		Name:            req.Name,
		Description:     req.Description,
		TransportType:   req.TransportType,
		ModuleID:        req.ModuleID,
		Endpoint:        req.Endpoint,
		MCPServerConfig: req.MCPServerConfig,
		AuthToken:       req.AuthToken,
		SchemaJSON:      req.SchemaJSON,
		CreatedAt:       now,
		UpdatedAt:       now,
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

// InstallFromCloudMeta 把云端拉取的 ModuleInstallMeta 安装到本地。
//
// P4 安装流程（见 docs/marketing/claw-implementation-plan.md §F.2）：
//   - official=true：直接激活本地预置（marketplace/ 已扫描注册），无需拉镜像。
//   - 第三方：ImageInstaller 拉镜像 + 签名校验 + 启动容器，再写入 registry 激活。
//
// 返回安装后的 ModuleRecord（含 image/signature 元数据）。已安装同 module_id 视为幂等成功。
func (s *ModuleService) InstallFromCloudMeta(meta ModuleInstallMeta) (*model.ModuleRecord, error) {
	if s.registry == nil {
		return nil, errors.New("ModuleRegistry 未初始化")
	}

	// 已存在则幂等返回（避免重复拉镜像/启动容器）
	if existing, err := s.moduleRepo.GetByID(meta.ModuleID); err == nil && existing != nil {
		// 官方预置模块：幂等路径也补齐 official/来源标记（本地扫描建记录时无此信息）。
		// 注意：经云端 installed 接口安装的官方模块同样标记 cloud-purchased，
		// 与本地纯扫描预置（InstallSource 为空）区分，激活时统一走 VIP 门控。
		if meta.Official && (!existing.Official || existing.InstallSource == "") {
			existing.Official = true
			existing.InstallSource = "cloud-purchased"
			if meta.Version != "" {
				existing.Version = meta.Version
			}
			if err := s.moduleRepo.CreateOrUpdate(existing); err != nil {
				return nil, err
			}
		}
		// 幂等路径也要补齐驱动绑定（首次安装时驱动写库失败重试、云端补发 driver_id 等场景）
		if err := s.upsertDriverBinding(meta); err != nil {
			return nil, err
		}
		existing.Status = model.ModuleStatusOnline
		if st := s.registry.Check(meta.ModuleID); st != nil {
			if !st.Online {
				existing.Status = model.ModuleStatusOffline
			}
			existing.HealthError = st.Error
		}
		return existing, nil
	}

	var record *model.ModuleRecord
	if meta.Official {
		// 官方模块：依赖 marketplace 扫描已注册；此处补齐 official 标记并返回
		rec, err := s.moduleRepo.GetByID(meta.ModuleID)
		if err != nil || rec == nil {
			return nil, fmt.Errorf("官方模块 %s 未在本地预置，请确认 marketplace/ 已包含", meta.ModuleID)
		}
		rec.Official = true
		// 云端安装的官方模块标记 cloud-purchased（激活走 VIP 门控）；
		// 本地纯扫描预置保持空值，不受门控。
		rec.InstallSource = "cloud-purchased"
		if meta.Version != "" {
			rec.Version = meta.Version
		}
		if err := s.moduleRepo.CreateOrUpdate(rec); err != nil {
			return nil, err
		}
		record = rec
	} else {
		// 第三方：拉镜像 + 签名 + 启动容器
		if s.installer == nil || s.installer.Runtime() == "" {
			return nil, fmt.Errorf("未检测到容器运行时（docker/podman），无法安装第三方模块 %s", meta.ModuleID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		rec, err := s.installer.Install(ctx, meta)
		if err != nil {
			return nil, err
		}
		if err := s.registry.RegisterRecord(rec); err != nil {
			return nil, fmt.Errorf("注册模块到 registry 失败: %w", err)
		}
		record = rec
	}

	// 安装成功后按 meta upsert 本地驱动别名并绑定模块（official/第三方通用）。
	// 本地 drivers 表缺该 DriverRecord 时新建，存在则更新绑定与令牌。
	if err := s.upsertDriverBinding(meta); err != nil {
		return nil, err
	}

	// 触发一次健康探测刷新状态
	if st := s.registry.ForceProbe(meta.ModuleID); st != nil {
		record.Status = model.ModuleStatusOffline
		if st.Online {
			record.Status = model.ModuleStatusOnline
		}
		record.Version = st.Version
		record.SetCapabilities(st.Capabilities)
		record.HealthError = st.Error
	}
	return record, nil
}

// upsertDriverBinding 按云端 meta upsert 本地驱动别名并绑定到已安装模块。
// meta.DriverID 为空时不做任何事。已存在的驱动记录只更新绑定/传输类型/令牌，保留其余字段。
func (s *ModuleService) upsertDriverBinding(meta ModuleInstallMeta) error {
	if meta.DriverID == "" {
		return nil
	}
	if s.driverRepo == nil {
		return errors.New("DriverRepo 未初始化")
	}

	transportType := meta.TransportType
	if transportType == "" {
		transportType = string(model.ModuleTransportTypeModule)
	}

	if existing, err := s.driverRepo.GetByID(meta.DriverID); err == nil && existing != nil {
		changed := false
		if existing.ModuleID != meta.ModuleID {
			existing.ModuleID = meta.ModuleID
			changed = true
		}
		if meta.TransportType != "" && existing.TransportType != meta.TransportType {
			existing.TransportType = meta.TransportType
			changed = true
		}
		if meta.AuthToken != "" && existing.AuthToken != meta.AuthToken {
			existing.AuthToken = meta.AuthToken
			changed = true
		}
		if existing.Name == "" {
			existing.Name = meta.Name
			if existing.Name == "" {
				existing.Name = meta.DriverID
			}
			changed = true
		}
		if !changed {
			return nil
		}
		if err := s.driverRepo.CreateOrUpdate(existing); err != nil {
			return fmt.Errorf("更新驱动别名 %s 失败: %w", meta.DriverID, err)
		}
		return nil
	}

	name := meta.Name
	if name == "" {
		name = meta.DriverID
	}
	rec := &model.DriverRecord{
		ID:            meta.DriverID,
		Name:          name,
		TransportType: transportType,
		ModuleID:      meta.ModuleID,
		AuthToken:     meta.AuthToken,
	}
	if err := s.driverRepo.CreateOrUpdate(rec); err != nil {
		return fmt.Errorf("创建驱动别名 %s 失败: %w", meta.DriverID, err)
	}
	return nil
}

// EnsureCloudAgentProvision 云端秘技安装成功后，按 meta.Manifest 在本地落库：
//   - upsert AgentItem（status=approved、manifest_json 落库；已存在时保留 purchase_count 等统计字段）；
//   - 为当前用户幂等写入 AgentPurchase（金额 0，来源为云端已购）。
//
// 未注入 AgentRepo（云端 cmd/server）或 meta 未携带 manifest 时直接跳过。
// 本地 AgentItem.ID 优先取 meta.AgentID，为空回退 manifest.id。
func (s *ModuleService) EnsureCloudAgentProvision(meta ModuleInstallMeta, userID string) error {
	if s.agentRepo == nil {
		return nil
	}
	if len(meta.Manifest) == 0 || string(meta.Manifest) == "null" {
		return nil
	}
	if userID == "" {
		return errors.New("无法识别当前用户，无法写入秘技购买记录")
	}

	var manifest model.ToolManifest
	if err := json.Unmarshal(meta.Manifest, &manifest); err != nil {
		return fmt.Errorf("manifest 解析失败: %w", err)
	}

	agentID := meta.AgentID
	if agentID == "" {
		agentID = manifest.ID
	}
	if agentID == "" {
		return errors.New("manifest 缺少 id 且 meta 未下发 agent_id，无法落库秘技记录")
	}

	name := manifest.Name
	if name == "" {
		name = meta.Name
	}
	description := manifest.Description
	if description == "" {
		description = meta.Description
	}
	level := model.AgentLevel(manifest.Level)
	if level < model.AgentLevelHuang || level > model.AgentLevelFenJue {
		level = model.AgentLevelHuang
	}

	if existing, err := s.agentRepo.GetByID(agentID); err == nil && existing != nil {
		// 已存在：只更新描述性字段，保留 purchase_count/avg_rating 等统计与价格、创作者信息
		existing.Name = name
		existing.Description = description
		existing.Category = manifest.Category
		existing.Level = level
		existing.ManifestJSON = string(meta.Manifest)
		existing.Status = model.AgentStatusApproved
		if err := s.agentRepo.Update(existing); err != nil {
			return fmt.Errorf("更新本地秘技 %s 失败: %w", agentID, err)
		}
	} else {
		item := &model.AgentItem{
			ID:           agentID,
			Name:         name,
			Description:  description,
			CreatorID:    "cloud",
			CreatorName:  "Eleball 云端",
			Category:     manifest.Category,
			Level:        level,
			ManifestJSON: string(meta.Manifest),
			Status:       model.AgentStatusApproved,
		}
		if err := s.agentRepo.Create(item); err != nil {
			return fmt.Errorf("创建本地秘技 %s 失败: %w", agentID, err)
		}
	}

	// 幂等写入购买记录：云端已购秘技本地补单，金额记 0
	purchased, err := s.agentRepo.HasPurchased(agentID, userID)
	if err != nil {
		return err
	}
	if purchased {
		return nil
	}
	purchase := &model.AgentPurchase{
		ID:              uuid.New().String(),
		AgentID:         agentID,
		BuyerID:         userID,
		PricePaid:       0,
		Currency:        "cloud-purchased",
		CreatorEarnings: 0,
		PlatformFee:     0,
	}
	if err := s.agentRepo.CreatePurchase(purchase); err != nil {
		return fmt.Errorf("写入秘技购买记录失败: %w", err)
	}
	return nil
}
