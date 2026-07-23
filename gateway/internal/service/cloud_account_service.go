package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 云端账户缓存 TTL
const cloudAccountCacheTTL = 5 * time.Minute

// cloudAccountEntry 云端账户信息缓存项
type cloudAccountEntry struct {
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
	cloudBase string
	http      *http.Client
	cache     map[string]*cloudAccountEntry
	mu        sync.Mutex
}

// NewCloudAccountService 创建云端账户缓存服务。cloudBase 形如 https://api.eleball.cn/v1。
func NewCloudAccountService(cloudBase string) *CloudAccountService {
	if cloudBase != "" && !strings.HasSuffix(cloudBase, "/") {
		cloudBase += "/"
	}
	return &CloudAccountService{
		cloudBase: cloudBase,
		http:      &http.Client{Timeout: 10 * time.Second},
		cache:     make(map[string]*cloudAccountEntry),
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
	if s.cloudBase == "" {
		return 0, errors.New("云端 API Base 未配置")
	}
	if entry, ok := s.getCached(userID); ok {
		return entry.vipLevel, nil
	}
	if token == "" {
		return 0, errors.New("未提供云端 token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cloudBase+"auth/me", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, errors.New("云端 /auth/me 返回非 200")
	}

	var wrapper struct {
		Code int             `json:"code"`
		Data cloudMeResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return 0, err
	}
	if wrapper.Code != 0 {
		return 0, errors.New("云端 /auth/me 业务错误")
	}

	s.putCached(userID, &cloudAccountEntry{
		vipLevel:  wrapper.Data.VipLevel,
		role:      wrapper.Data.Role,
		username:  wrapper.Data.Username,
		email:     wrapper.Data.Email,
		fetchedAt: time.Now(),
	})
	return wrapper.Data.VipLevel, nil
}

// RequireVIP1 校验用户云端 VIP >= 1，不满足返回 error。token 不含 "Bearer " 前缀。
// 云端不可达时返回 error（安全侧降级：云端秘技门控一律拦）。
func (s *CloudAccountService) RequireVIP1(ctx context.Context, userID, token string) error {
	level, err := s.GetVipLevel(ctx, userID, token)
	if err != nil {
		return errors.New("无法校验云端会员等级，请稍后重试")
	}
	if level < 1 {
		return errors.New("该云端秘技需 VIP1 及以上")
	}
	return nil
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
