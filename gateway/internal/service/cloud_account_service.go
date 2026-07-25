package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 云端账户缓存 TTL
const cloudAccountCacheTTL = 5 * time.Minute

// cloudAccountEntry 云端账户信息缓存项
type cloudAccountEntry struct {
	userID      string
	vipLevel    int
	vipExpireAt time.Time
	role        string
	username    string
	email       string
	fetchedAt   time.Time
}

// CloudAccountService 云端账户信息缓存服务。
//
// claw 本地不再持有 users 表账户体系，VIP 等级等从云端 /auth/me 拉取并按 user_id 缓存。
// 仅用于 claw 本地门控（如云端秘技 VIP1+ 校验）；token 由调用方从请求头透传。
// 云端不可达时降级为 VIP0（本地 BYOK/agent 不受影响，仅云端秘技门控一律拦--安全侧降级）。
type CloudAccountService struct {
	cloudBase  string
	http       *http.Client
	cache      map[string]*cloudAccountEntry
	tokenCache map[string]*cloudAccountEntry // ValidateToken 按 token 缓存
	mu         sync.Mutex
}

// NewCloudAccountService 创建云端账户缓存服务。cloudBase 形如 https://api.eleball.cn/v1。
func NewCloudAccountService(cloudBase string) *CloudAccountService {
	if cloudBase != "" && !strings.HasSuffix(cloudBase, "/") {
		cloudBase += "/"
	}
	return &CloudAccountService{
		cloudBase:  cloudBase,
		http:       &http.Client{Timeout: 10 * time.Second},
		cache:      make(map[string]*cloudAccountEntry),
		tokenCache: make(map[string]*cloudAccountEntry),
	}
}

// cloudMeResponse 云端 /auth/me 响应（data 部分）
type cloudMeResponse struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	VipLevel int    `json:"vip_level"`
}

// GetVipLevel 取用户云端 VIP 等级（缓存优先）。token 为用户云端 JWT（不含 "Bearer " 前缀）。
// 云端不可达或未配置时返回 0 + error，调用方按安全侧降级处理。
func (s *CloudAccountService) GetVipLevel(ctx context.Context, userID, token string) (int, error) {
	entry, err := s.getAccount(ctx, userID, token)
	if err != nil {
		return 0, err
	}
	return entry.vipLevel, nil
}

// getAccount 取云端账户信息（vip_level/role 等，缓存优先）
func (s *CloudAccountService) getAccount(ctx context.Context, userID, token string) (*cloudAccountEntry, error) {
	if s.cloudBase == "" {
		return nil, errors.New("云端 API Base 未配置")
	}
	if entry, ok := s.getCached(userID); ok {
		return entry, nil
	}
	if token == "" {
		return nil, errors.New("未提供云端 token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cloudBase+"auth/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("云端 /auth/me 返回非 200")
	}

	var wrapper struct {
		Code int             `json:"code"`
		Data cloudMeResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, err
	}
	if wrapper.Code != 0 {
		return nil, errors.New("云端 /auth/me 业务错误")
	}

	entry := &cloudAccountEntry{
		vipLevel:  wrapper.Data.VipLevel,
		role:      wrapper.Data.Role,
		username:  wrapper.Data.Username,
		email:     wrapper.Data.Email,
		fetchedAt: time.Now(),
	}
	s.putCached(userID, entry)
	return entry, nil
}

// RequireVIP1 校验用户云端 VIP >= 1，不满足返回 error。token 不含 "Bearer " 前缀。
// 管理员（role=admin）视为等效 VIP1+ 直接放行——管理员是运营方，不受 VIP 门禁限制。
// 云端不可达时返回 error（安全侧降级：云端秘技门控一律拦）。
func (s *CloudAccountService) RequireVIP1(ctx context.Context, userID, token string) error {
	entry, err := s.getAccount(ctx, userID, token)
	if err != nil {
		return errors.New("无法校验云端会员等级，请稍后重试")
	}
	if entry.vipLevel < 1 && entry.role != "admin" {
		return errors.New("该云端秘技需 VIP1 及以上")
	}
	return nil
}

// CloudTransientError 云端暂时性故障（网络错误、超时、429、5xx）。
// 与「token 确实无效」（云端明确 401）区分：暂时性故障不应把用户当退出登录处理。
type CloudTransientError struct {
	Err error
}

func (e *CloudTransientError) Error() string { return e.Err.Error() }

// Transient 标记暂时性故障。中间件经此接口识别（避免 middleware -> service 导入环）
func (e *CloudTransientError) Transient() bool { return true }

// ValidateToken 用云端 /auth/me 校验 token 有效性，返回用户 ID 与角色。
//
// 用于 claw 本地 JWT 密钥与云端不一致时的回退验证：安装脚本为本地生成随机密钥
// （不可能也不应与每个用户设备共享云端签名密钥），云端签发的 token 本地验签必然失败。
// 结果按 token 缓存（TTL 同账户缓存），避免每个本地 API 请求都打到云端。
// token 不含 "Bearer " 前缀。
//
// 错误语义：云端明确拒绝（401）返回普通 error（调用方按登录失效处理）；
// 网络/超时/429/5xx 等暂时性故障返回 *CloudTransientError（调用方应提示稍后重试而非登出）。
func (s *CloudAccountService) ValidateToken(ctx context.Context, token string) (string, string, error) {
	if s.cloudBase == "" {
		return "", "", &CloudTransientError{Err: errors.New("云端 API Base 未配置")}
	}
	if token == "" {
		return "", "", errors.New("未提供云端 token")
	}
	if entry, ok := s.getTokenCached(token); ok {
		return entry.userID, entry.role, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cloudBase+"auth/me", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.http.Do(req)
	if err != nil {
		// 网络错误/超时：暂时性故障
		return "", "", &CloudTransientError{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// 云端明确拒绝：token 无效或已过期
		return "", "", errors.New("云端 token 校验未通过")
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", "", &CloudTransientError{Err: fmt.Errorf("云端响应异常（HTTP %d）", resp.StatusCode)}
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", &CloudTransientError{Err: fmt.Errorf("云端响应异常（HTTP %d）", resp.StatusCode)}
	}

	var wrapper struct {
		Code int             `json:"code"`
		Data cloudMeResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return "", "", &CloudTransientError{Err: err}
	}
	if wrapper.Code != 0 || wrapper.Data.UserID == "" {
		return "", "", errors.New("云端 token 校验业务错误")
	}

	s.putTokenCached(token, &cloudAccountEntry{
		userID:    wrapper.Data.UserID,
		vipLevel:  wrapper.Data.VipLevel,
		role:      wrapper.Data.Role,
		username:  wrapper.Data.Username,
		email:     wrapper.Data.Email,
		fetchedAt: time.Now(),
	})
	return wrapper.Data.UserID, wrapper.Data.Role, nil
}

// getTokenCached 返回指定 token 未过期的验证缓存
func (s *CloudAccountService) getTokenCached(token string) (*cloudAccountEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tokenCache[token]
	if !ok || time.Since(entry.fetchedAt) > cloudAccountCacheTTL {
		return nil, false
	}
	return entry, true
}

// putTokenCached 写入 token 验证缓存
func (s *CloudAccountService) putTokenCached(token string, entry *cloudAccountEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenCache[token] = entry
}

// getCached 返回未过期的缓存项
func (s *CloudAccountService) getCached(userID string) (*cloudAccountEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[userID]
	if !ok || time.Since(entry.fetchedAt) > cloudAccountCacheTTL {
		return nil, false
	}
	return entry, true
}

// putCached 写入缓存
func (s *CloudAccountService) putCached(userID string, entry *cloudAccountEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[userID] = entry
}
