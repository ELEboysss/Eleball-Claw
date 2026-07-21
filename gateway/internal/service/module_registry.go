package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ModuleStatus 集市模块状态
type ModuleStatus struct {
	ModuleID     string   `json:"module_id"`
	Version      string   `json:"version"`
	Online       bool     `json:"online"`
	Capabilities []string `json:"capabilities,omitempty"`
	Error        string   `json:"error,omitempty"`
	CheckedAt    time.Time
}

// ModuleRegistry 发现并缓存集市模块健康状态
// 每个模块是一个独立容器，通过 /health 接口上报在线状态与能力清单。
// 支持从数据库加载持久化记录，也支持运行时动态注册/注销。
type ModuleRegistry struct {
	cfg           *config.AgentReachConfig
	client        *http.Client
	mu            sync.RWMutex
	statuses      map[string]*ModuleStatus
	urls          map[string]string // module_id -> url
	moduleRepo    *repository.ModuleRepo
	driverRepo    *repository.DriverRepo
	logger        *zap.Logger
	stopCh        chan struct{}
	startOnce     sync.Once
	probeInterval time.Duration
}

// NewModuleRegistry 创建模块注册表
func NewModuleRegistry(cfg *config.AgentReachConfig) *ModuleRegistry {
	if cfg == nil {
		cfg = &config.AgentReachConfig{}
	}
	interval := cfg.HealthCheckInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	probeInterval := cfg.ProbeInterval
	if probeInterval <= 0 {
		probeInterval = 5 * time.Minute
	}
	cfgCopy := *cfg
	cfgCopy.HealthCheckInterval = interval
	cfgCopy.ProbeInterval = probeInterval
	return &ModuleRegistry{
		cfg:          &cfgCopy,
		client:       newModuleHTTPClient(cfg),
		statuses:     make(map[string]*ModuleStatus),
		urls:         make(map[string]string),
		stopCh:       make(chan struct{}),
		probeInterval: probeInterval,
	}
}

// SetLogger 设置日志器，用于记录健康探测结果。
func (r *ModuleRegistry) SetLogger(logger *zap.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
}

// newModuleHTTPClient 创建模块健康探测/调用客户端，支持配置代理。
func newModuleHTTPClient(cfg *config.AgentReachConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg != nil && cfg.Proxy != "" {
		if proxyURL, err := url.Parse(cfg.Proxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}
}

// SetRepo 设置持久化仓库并加载已有模块
// 在正式服务器启动时调用，测试环境可跳过。
func (r *ModuleRegistry) SetRepo(repo *repository.ModuleRepo) {
	r.mu.Lock()
	r.moduleRepo = repo
	r.mu.Unlock()
	r.loadAll()
}

// SetDriverRepo 设置驱动仓库，用于插件自助注册时按 auth_token 绑定驱动别名。
func (r *ModuleRegistry) SetDriverRepo(repo *repository.DriverRepo) {
	r.mu.Lock()
	r.driverRepo = repo
	r.mu.Unlock()
}

// Start 启动后台主动探测协程。
// 应在 SetRepo 加载模块记录之后调用；重复调用无效。
func (r *ModuleRegistry) Start() {
	r.startOnce.Do(func() {
		if r.probeInterval <= 0 {
			return
		}
		if r.logger != nil {
			r.logger.Info("启动模块后台健康探测", zap.Duration("interval", r.probeInterval))
		}
		go r.probeLoop()
	})
}

// Stop 停止后台主动探测协程。
func (r *ModuleRegistry) Stop() {
	close(r.stopCh)
}

// probeLoop 后台定期探测所有已注册模块。
func (r *ModuleRegistry) probeLoop() {
	ticker := time.NewTicker(r.probeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.runBackgroundProbe()
		case <-r.stopCh:
			if r.logger != nil {
				r.logger.Info("停止模块后台健康探测")
			}
			return
		}
	}
}

// runBackgroundProbe 对当前内存中所有模块执行一次强制探测。
func (r *ModuleRegistry) runBackgroundProbe() {
	var ids []string
	r.mu.RLock()
	for id := range r.statuses {
		ids = append(ids, id)
	}
	r.mu.RUnlock()

	for _, id := range ids {
		r.ForceProbe(id)
	}
}

// loadAll 从 DB 加载模块到内存
func (r *ModuleRegistry) loadAll() {
	if r.moduleRepo == nil {
		return
	}
	records, err := r.moduleRepo.List()
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range records {
		if rec.Status == model.ModuleStatusDisabled {
			continue
		}
		r.statuses[rec.ID] = &ModuleStatus{
			ModuleID:     rec.ID,
			Version:      rec.Version,
			Online:       rec.Status == model.ModuleStatusOnline,
			Capabilities: rec.CapabilitiesList(),
			CheckedAt:    time.Time{},
		}
		r.urls[rec.ID] = rec.URL
	}
}

// Register 注册一个模块地址（兼容旧逻辑）
func (r *ModuleRegistry) Register(moduleID, url string) {
	if url == "" {
		url = r.defaultModuleURL(moduleID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.statuses[moduleID] == nil {
		r.statuses[moduleID] = &ModuleStatus{ModuleID: moduleID}
	}
	r.statuses[moduleID].Capabilities = []string{}
	r.urls[moduleID] = url

	if r.moduleRepo != nil {
		_ = r.moduleRepo.CreateOrUpdate(&model.ModuleRecord{
			ID:          moduleID,
			Name:        moduleID,
			URL:         url,
			TransportType: model.ModuleTransportTypeModule,
			Status:      model.ModuleStatusOffline,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		})
	}
}

// RegisterRecord 通过完整记录注册/更新模块（管理后台用）
// 若 record.ID 为空，会根据 name 自动生成唯一 module_id。
func (r *ModuleRegistry) RegisterRecord(record *model.ModuleRecord) error {
	if record.ID == "" {
		record.ID = r.generateUniqueModuleID(record.Name)
	}
	if record.URL == "" {
		record.URL = r.defaultModuleURL(record.ID)
	}
	now := time.Now()
	record.UpdatedAt = now
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.Status == "" {
		record.Status = model.ModuleStatusOffline
	}

	if r.moduleRepo != nil {
		if err := r.moduleRepo.CreateOrUpdate(record); err != nil {
			return err
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses[record.ID] = &ModuleStatus{
		ModuleID:     record.ID,
		Version:      record.Version,
		Online:       record.Status == model.ModuleStatusOnline,
		Capabilities: record.CapabilitiesList(),
	}
	r.urls[record.ID] = record.URL
	return nil
}

// generateUniqueModuleID 根据名称生成不冲突的 module_id。
func (r *ModuleRegistry) generateUniqueModuleID(name string) string {
	base := model.GenerateModuleID(name)
	if base == "" {
		base = "mod-" + uuid.New().String()[:8]
	}
	id := base
	for i := 1; i < 1000; i++ {
		exists := false
		if r.moduleRepo != nil {
			_, err := r.moduleRepo.GetByID(id)
			exists = err == nil
		}
		if !exists {
			r.mu.RLock()
			_, exists = r.statuses[id]
			r.mu.RUnlock()
		}
		if !exists {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return base + "-" + uuid.New().String()[:8]
}

// RegisterFromPlugin 插件自助注册
// 支持两种模式：
//   1. 新流程：提供 auth_token 时，优先按 token 查找 driver 记录，将模块绑定到该 driver 别名；
//   2. 旧流程：直接指定 module_id，按 module.auth_token 做简单校验。
// module_id 可选：若插件未提供，会根据 name 自动生成。
func (r *ModuleRegistry) RegisterFromPlugin(req *model.ModuleRegisterRequest, providedToken string) error {
	if req.URL == "" {
		return errors.New("url 不能为空")
	}

	// 新流程：通过 driver 的 auth_token 绑定模块
	if providedToken != "" && r.driverRepo != nil {
		driver, err := r.driverRepo.GetByAuthToken(providedToken)
		if err == nil && driver != nil {
			return r.registerModuleAndBindDriver(req, driver, providedToken)
		}
	}

	// 旧流程：直接注册模块
	if req.ModuleID == "" {
		req.ModuleID = r.generateUniqueModuleID(req.Name)
	}
	return r.registerModuleInternal(req, providedToken)
}

// registerModuleAndBindDriver 按 driver auth_token 注册模块并绑定到驱动别名。
func (r *ModuleRegistry) registerModuleAndBindDriver(req *model.ModuleRegisterRequest, driver *model.DriverRecord, providedToken string) error {
	// transport_type 未指定时，以 driver 为准
	if req.TransportType == "" {
		req.TransportType = string(driver.TransportType)
	}
	if req.TransportType != string(driver.TransportType) {
		return fmt.Errorf("transport_type 与驱动记录不一致：期望 %s，实际 %s", driver.TransportType, req.TransportType)
	}

	// 确定 module_id：请求指定 > driver 已绑定 > 自动生成
	moduleID := req.ModuleID
	if moduleID == "" {
		moduleID = driver.ModuleID
	}
	if moduleID == "" {
		moduleID = r.generateUniqueModuleID(req.Name)
	}
	req.ModuleID = moduleID

	if err := r.registerModuleInternal(req, providedToken); err != nil {
		return err
	}

	// 若 driver 未绑定或绑定了别的模块，更新绑定关系
	if driver.ModuleID != moduleID {
		if err := r.driverRepo.UpdateModuleID(driver.ID, moduleID); err != nil {
			return fmt.Errorf("绑定驱动失败: %w", err)
		}
	}
	return nil
}

// registerModuleInternal 注册/更新模块记录（旧流程共用）。
func (r *ModuleRegistry) registerModuleInternal(req *model.ModuleRegisterRequest, providedToken string) error {
	var existing *model.ModuleRecord
	if r.moduleRepo != nil {
		existing, _ = r.moduleRepo.GetByID(req.ModuleID)
	}

	// 已存在且设置了 auth_token 时校验令牌
	if existing != nil && existing.AuthToken != "" && existing.AuthToken != providedToken {
		return errors.New("注册令牌无效")
	}

	record := &model.ModuleRecord{
		ID:            req.ModuleID,
		Name:          req.Name,
		Description:   req.Description,
		URL:           req.URL,
		TransportType: model.ModuleTransportType(req.TransportType),
		Status:        model.ModuleStatusOffline,
		Version:       req.Version,
		Capabilities:  "[]",
	}
	record.SetCapabilities(req.Capabilities)
	if existing != nil {
		record.AuthToken = existing.AuthToken
		record.CreatedAt = existing.CreatedAt
	} else if providedToken != "" {
		// 新模块首次自助注册时保存提供的令牌，后续注册需校验
		record.AuthToken = providedToken
	}
	return r.RegisterRecord(record)
}

// Unregister 注销模块
func (r *ModuleRegistry) Unregister(moduleID string) error {
	if r.moduleRepo != nil {
		if err := r.moduleRepo.Delete(moduleID); err != nil {
			return err
		}
	}
	r.mu.Lock()
	delete(r.statuses, moduleID)
	delete(r.urls, moduleID)
	r.mu.Unlock()
	return nil
}

// Check 查询指定模块状态，带本地缓存
// 未注册的模块返回 nil，避免探测随机地址
func (r *ModuleRegistry) Check(moduleID string) *ModuleStatus {
	r.mu.RLock()
	st := r.statuses[moduleID]
	r.mu.RUnlock()

	if st == nil {
		return nil
	}
	if time.Since(st.CheckedAt) < r.cfg.HealthCheckInterval {
		return st
	}

	return r.probe(moduleID)
}

// ForceProbe 强制探测指定模块健康状态（忽略缓存）。
// 模块未注册时返回 nil。
func (r *ModuleRegistry) ForceProbe(moduleID string) *ModuleStatus {
	r.mu.RLock()
	_, exists := r.statuses[moduleID]
	r.mu.RUnlock()
	if !exists {
		return nil
	}
	return r.probe(moduleID)
}

// List 返回所有已注册模块状态
// 返回前会触发一次健康探测，保证结果最新
func (r *ModuleRegistry) List() []*ModuleStatus {
	var ids []string
	r.mu.RLock()
	for moduleID := range r.statuses {
		ids = append(ids, moduleID)
	}
	r.mu.RUnlock()

	var list []*ModuleStatus
	for _, id := range ids {
		if st := r.Check(id); st != nil {
			list = append(list, st)
		}
	}
	return list
}

// ModuleURL 返回模块地址
func (r *ModuleRegistry) ModuleURL(moduleID string) string {
	if moduleID == "agent-reach" && r.cfg.ModuleURL != "" {
		return r.cfg.ModuleURL
	}
	r.mu.RLock()
	url, ok := r.urls[moduleID]
	r.mu.RUnlock()
	if ok && url != "" {
		return url
	}
	return r.defaultModuleURL(moduleID)
}

func (r *ModuleRegistry) defaultModuleURL(moduleID string) string {
	return fmt.Sprintf("http://%s:8080", moduleID)
}

// Execute 调用模块执行指定 action
func (r *ModuleRegistry) Execute(moduleID, action string, params map[string]interface{}, userID string) (map[string]interface{}, error) {
	url := r.ModuleURL(moduleID) + "/execute"
	body, _ := json.Marshal(map[string]interface{}{
		"action":  action,
		"params":  params,
		"user_id": userID,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("模块调用失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("模块响应解析失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("模块返回 HTTP %d", resp.StatusCode)
	}
	return result, nil
}

// probe 实际探测模块 /health
func (r *ModuleRegistry) probe(moduleID string) *ModuleStatus {
	url := r.ModuleURL(moduleID) + "/health"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return r.setStatus(moduleID, false, "", nil, err.Error())
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return r.setStatus(moduleID, false, "", nil, err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return r.setStatus(moduleID, false, "", nil, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}

	var payload struct {
		ModuleID     string   `json:"module_id"`
		Version      string   `json:"version"`
		Status       string   `json:"status"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return r.setStatus(moduleID, false, "", nil, err.Error())
	}

	online := payload.Status == "ok"
	if r.logger != nil {
		r.logger.Info("模块健康探测完成",
			zap.String("module_id", moduleID),
			zap.String("url", url),
			zap.Bool("online", online),
		)
	}
	return r.setStatus(moduleID, online, payload.Version, payload.Capabilities, "")
}

func (r *ModuleRegistry) setStatus(moduleID string, online bool, version string, caps []string, errMsg string) *ModuleStatus {
	if errMsg != "" && r.logger != nil {
		r.logger.Warn("模块健康探测失败",
			zap.String("module_id", moduleID),
			zap.String("error", errMsg),
		)
	}
	st := &ModuleStatus{
		ModuleID:     moduleID,
		Version:      version,
		Online:       online,
		Capabilities: caps,
		Error:        errMsg,
		CheckedAt:    time.Now(),
	}
	r.mu.Lock()
	r.statuses[moduleID] = st
	r.mu.Unlock()

	// 异步写回 DB，避免探测阻塞
	if r.moduleRepo != nil {
		go r.persistStatus(moduleID, online, version, caps)
	}
	return st
}

func (r *ModuleRegistry) persistStatus(moduleID string, online bool, version string, caps []string) {
	status := model.ModuleStatusOffline
	if online {
		status = model.ModuleStatusOnline
	}
	now := time.Now()
	record := &model.ModuleRecord{
		ID:            moduleID,
		Version:       version,
		Status:        status,
		LastHeartbeat: &now,
		UpdatedAt:     now,
	}
	record.SetCapabilities(caps)
	_ = r.moduleRepo.UpdateStatus(record)
}
