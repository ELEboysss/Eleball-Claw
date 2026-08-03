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
	"go.uber.org/zap"
)

// SkillRuntimeStatusSnapshot 运行时状态快照
type SkillRuntimeStatusSnapshot struct {
	RuntimeID    string   `json:"runtime_id"`
	Version      string   `json:"version"`
	Online       bool     `json:"online"`
	Capabilities []string `json:"capabilities,omitempty"`
	Error        string   `json:"error,omitempty"`
	CheckedAt    time.Time
}

// SkillRuntimeRegistry 发现并缓存秘技运行时健康状态
// 支持多种 transport（execute/mcp_http/mcp_stdio/raw_http）和 deployment（none/process/docker/external）。
type SkillRuntimeRegistry struct {
	cfg           *config.AgentReachConfig
	client        *http.Client
	mu            sync.RWMutex
	statuses      map[string]*SkillRuntimeStatusSnapshot
	runtimes      map[string]*model.SkillRuntime
	runtimeRepo   *repository.SkillRuntimeRepo
	logger        *zap.Logger
	stopCh        chan struct{}
	startOnce     sync.Once
	probeInterval time.Duration
	mcpHTTP       *MCPHTTPProtocol // 用于 MCP 探活
}

// NewSkillRuntimeRegistry 创建运行时注册表
func NewSkillRuntimeRegistry(cfg *config.AgentReachConfig) *SkillRuntimeRegistry {
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
	return &SkillRuntimeRegistry{
		cfg:           &cfgCopy,
		client:        newModuleHTTPClient(cfg),
		statuses:      make(map[string]*SkillRuntimeStatusSnapshot),
		runtimes:      make(map[string]*model.SkillRuntime),
		stopCh:        make(chan struct{}),
		probeInterval: probeInterval,
		mcpHTTP:       NewMCPHTTPProtocol(nil),
	}
}

// SetLogger 设置日志器
func (r *SkillRuntimeRegistry) SetLogger(logger *zap.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
}

// SetRepo 设置持久化仓库并加载已有运行时
func (r *SkillRuntimeRegistry) SetRepo(repo *repository.SkillRuntimeRepo) {
	r.mu.Lock()
	r.runtimeRepo = repo
	r.mu.Unlock()
	r.loadAll()
}

// Start 启动后台主动探测协程
func (r *SkillRuntimeRegistry) Start() {
	r.startOnce.Do(func() {
		if r.probeInterval <= 0 {
			return
		}
		if r.logger != nil {
			r.logger.Info("启动秘技运行时后台健康探测", zap.Duration("interval", r.probeInterval))
		}
		go r.probeLoop()
	})
}

// Stop 停止后台主动探测协程
func (r *SkillRuntimeRegistry) Stop() {
	close(r.stopCh)
}

func (r *SkillRuntimeRegistry) probeLoop() {
	ticker := time.NewTicker(r.probeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.runBackgroundProbe()
		case <-r.stopCh:
			if r.logger != nil {
				r.logger.Info("停止秘技运行时后台健康探测")
			}
			return
		}
	}
}

func (r *SkillRuntimeRegistry) runBackgroundProbe() {
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

func (r *SkillRuntimeRegistry) loadAll() {
	if r.runtimeRepo == nil {
		return
	}
	records, err := r.runtimeRepo.List()
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range records {
		if rec.Status == model.SkillRuntimeStatusDisabled {
			continue
		}
		r.statuses[rec.ID] = &SkillRuntimeStatusSnapshot{
			RuntimeID:    rec.ID,
			Version:      rec.Version,
			Online:       rec.Status == model.SkillRuntimeStatusOnline,
			Capabilities: rec.CapabilitiesList(),
			CheckedAt:    time.Time{},
		}
		r.runtimes[rec.ID] = rec
	}
}

// Register 注册/更新运行时记录
func (r *SkillRuntimeRegistry) Register(runtime *model.SkillRuntime) error {
	if runtime.ID == "" {
		return errors.New("runtime id 不能为空")
	}
	now := time.Now()
	runtime.UpdatedAt = now
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = now
	}
	if runtime.Status == "" {
		runtime.Status = model.SkillRuntimeStatusOffline
	}
	if runtime.Capabilities == "" {
		runtime.Capabilities = "[]"
	}

	if r.runtimeRepo != nil {
		if err := r.runtimeRepo.CreateOrUpdate(runtime); err != nil {
			return err
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimes[runtime.ID] = runtime
	r.statuses[runtime.ID] = &SkillRuntimeStatusSnapshot{
		RuntimeID:    runtime.ID,
		Version:      runtime.Version,
		Online:       runtime.Status == model.SkillRuntimeStatusOnline,
		Capabilities: runtime.CapabilitiesList(),
		CheckedAt:    time.Time{},
	}
	return nil
}

// Unregister 注销运行时
func (r *SkillRuntimeRegistry) Unregister(runtimeID string) error {
	if r.runtimeRepo != nil {
		if err := r.runtimeRepo.Delete(runtimeID); err != nil {
			return err
		}
	}
	r.mu.Lock()
	delete(r.statuses, runtimeID)
	delete(r.runtimes, runtimeID)
	r.mu.Unlock()
	return nil
}

// Get 获取运行时记录
func (r *SkillRuntimeRegistry) Get(runtimeID string) *model.SkillRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runtimes[runtimeID]
}

// Check 查询运行时状态，带本地缓存
func (r *SkillRuntimeRegistry) Check(runtimeID string) *SkillRuntimeStatusSnapshot {
	r.mu.RLock()
	st := r.statuses[runtimeID]
	r.mu.RUnlock()

	if st == nil {
		return nil
	}
	if !r.cfg.ProbeOnRequest {
		return st
	}
	if time.Since(st.CheckedAt) < r.cfg.HealthCheckInterval {
		return st
	}

	return r.probe(runtimeID)
}

// ForceProbe 强制探测运行时健康状态（忽略缓存）
func (r *SkillRuntimeRegistry) ForceProbe(runtimeID string) *SkillRuntimeStatusSnapshot {
	r.mu.RLock()
	_, exists := r.statuses[runtimeID]
	r.mu.RUnlock()
	if !exists {
		return nil
	}
	return r.probe(runtimeID)
}

// List 返回所有已注册运行时状态
func (r *SkillRuntimeRegistry) List() []*SkillRuntimeStatusSnapshot {
	var ids []string
	r.mu.RLock()
	for runtimeID := range r.statuses {
		ids = append(ids, runtimeID)
	}
	r.mu.RUnlock()

	var list []*SkillRuntimeStatusSnapshot
	for _, id := range ids {
		if st := r.Check(id); st != nil {
			list = append(list, st)
		}
	}
	return list
}

// Endpoint 返回运行时连接地址
func (r *SkillRuntimeRegistry) Endpoint(runtimeID string) string {
	r.mu.RLock()
	rt := r.runtimes[runtimeID]
	r.mu.RUnlock()
	if rt == nil {
		return ""
	}
	return rt.Endpoint
}

// Execute 调用运行时执行指定 action（兼容 execute/raw_http）
func (r *SkillRuntimeRegistry) Execute(runtimeID, action string, params map[string]interface{}, userID string) (map[string]interface{}, error) {
	endpoint := r.Endpoint(runtimeID)
	if endpoint == "" {
		return nil, errors.New("runtime 未注册或 endpoint 为空")
	}

	rt := r.Get(runtimeID)
	if rt == nil {
		return nil, errors.New("runtime 记录不存在")
	}

	switch rt.Transport {
	case model.SkillRuntimeTransportExecute:
		return r.executeProtocol(endpoint, action, params, userID)
	case model.SkillRuntimeTransportRawHTTP:
		return r.rawHTTPProtocol(endpoint, action, params, userID)
	case model.SkillRuntimeTransportMCPHTTP:
		return r.mcpHTTPProtocol(rt, action, params, userID)
	default:
		return nil, fmt.Errorf("不支持的 transport: %s", rt.Transport)
	}
}

func (r *SkillRuntimeRegistry) executeProtocol(endpoint, action string, params map[string]interface{}, userID string) (map[string]interface{}, error) {
	url := endpoint + "/execute"
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

func (r *SkillRuntimeRegistry) rawHTTPProtocol(endpoint, action string, params map[string]interface{}, userID string) (map[string]interface{}, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"action":  action,
		"params":  params,
		"user_id": userID,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("远程调用失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("远程返回 HTTP %d", resp.StatusCode)
	}
	return result, nil
}

func (r *SkillRuntimeRegistry) mcpHTTPProtocol(rt *model.SkillRuntime, action string, params map[string]interface{}, userID string) (map[string]interface{}, error) {
	if r.mcpHTTP == nil {
		return nil, errors.New("MCP HTTP 协议未初始化")
	}
	// 从 DriverRecord 的 MCPServerConfig 取 headers（由 AgentToolLoader 注入 params 中）
	var headers map[string]string
	if h, ok := params["__mcp_headers__"].(map[string]string); ok {
		headers = h
	}
	return r.mcpHTTP.Execute(rt.Endpoint, action, params, headers)
}

// probe 根据 transport 类型探测运行时健康状态
func (r *SkillRuntimeRegistry) probe(runtimeID string) *SkillRuntimeStatusSnapshot {
	rt := r.Get(runtimeID)
	if rt == nil {
		return r.setStatus(runtimeID, false, "", nil, "runtime 记录不存在")
	}

	switch rt.Transport {
	case model.SkillRuntimeTransportExecute, model.SkillRuntimeTransportRawHTTP:
		return r.probeHTTP(runtimeID)
	case model.SkillRuntimeTransportMCPHTTP:
		return r.probeMCPHTTP(runtimeID)
	case model.SkillRuntimeTransportMCPStdio:
		// stdio 探活由 supervisor 管理，这里返回当前状态
		r.mu.RLock()
		st := r.statuses[runtimeID]
		r.mu.RUnlock()
		return st
	default:
		return r.setStatus(runtimeID, false, "", nil, fmt.Sprintf("不支持的 transport: %s", rt.Transport))
	}
}

func (r *SkillRuntimeRegistry) probeHTTP(runtimeID string) *SkillRuntimeStatusSnapshot {
	endpoint := r.Endpoint(runtimeID)
	if endpoint == "" {
		return r.setStatus(runtimeID, false, "", nil, "endpoint 为空")
	}

	url := endpoint + "/health"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return r.setStatus(runtimeID, false, "", nil, err.Error())
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return r.setStatus(runtimeID, false, "", nil, err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return r.setStatus(runtimeID, false, "", nil, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}

	var payload struct {
		ModuleID     string   `json:"module_id"`
		Version      string   `json:"version"`
		Status       string   `json:"status"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return r.setStatus(runtimeID, false, "", nil, err.Error())
	}

	online := payload.Status == "ok"
	if r.logger != nil {
		r.logger.Info("运行时健康探测完成",
			zap.String("runtime_id", runtimeID),
			zap.String("url", url),
			zap.Bool("online", online),
		)
	}
	return r.setStatus(runtimeID, online, payload.Version, payload.Capabilities, "")
}

func (r *SkillRuntimeRegistry) probeMCPHTTP(runtimeID string) *SkillRuntimeStatusSnapshot {
	rt := r.Get(runtimeID)
	if rt == nil {
		return r.setStatus(runtimeID, false, "", nil, "runtime 记录不存在")
	}

	// MCP 探活使用 tools/list 而不是 /health
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := r.mcpHTTP.ListTools(ctx, rt.Endpoint, nil)
	if err != nil {
		return r.setStatus(runtimeID, false, "", nil, err.Error())
	}

	caps := make([]string, 0, len(tools))
	for _, t := range tools {
		caps = append(caps, t.Name)
	}

	if r.logger != nil {
		r.logger.Info("MCP 运行时健康探测完成",
			zap.String("runtime_id", runtimeID),
			zap.String("endpoint", rt.Endpoint),
			zap.Int("tools_count", len(tools)),
		)
	}
	return r.setStatus(runtimeID, true, rt.Version, caps, "")
}

func (r *SkillRuntimeRegistry) setStatus(runtimeID string, online bool, version string, caps []string, errMsg string) *SkillRuntimeStatusSnapshot {
	if errMsg != "" && r.logger != nil {
		r.logger.Warn("运行时健康探测失败",
			zap.String("runtime_id", runtimeID),
			zap.String("error", errMsg),
		)
	}
	st := &SkillRuntimeStatusSnapshot{
		RuntimeID:    runtimeID,
		Version:      version,
		Online:       online,
		Capabilities: caps,
		Error:        errMsg,
		CheckedAt:    time.Now(),
	}
	r.mu.Lock()
	r.statuses[runtimeID] = st
	r.mu.Unlock()

	// 异步写回 DB
	if r.runtimeRepo != nil {
		go r.persistStatus(runtimeID, online, version, caps)
	}
	return st
}

func (r *SkillRuntimeRegistry) persistStatus(runtimeID string, online bool, version string, caps []string) {
	status := model.SkillRuntimeStatusOffline
	if online {
		status = model.SkillRuntimeStatusOnline
	}
	now := time.Now()
	record := &model.SkillRuntime{
		ID:            runtimeID,
		Version:       version,
		Status:        status,
		LastHeartbeat: &now,
		UpdatedAt:     now,
	}
	record.SetCapabilities(caps)
	_ = r.runtimeRepo.UpdateStatus(record)
}

// defaultRuntimeURL 生成默认运行时地址（兼容旧逻辑）
func defaultRuntimeURL(runtimeID string) string {
	return fmt.Sprintf("http://%s:8080", runtimeID)
}

// parseRuntimeEndpoint 从 MCP URL 提取基础地址
func parseRuntimeEndpoint(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
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
