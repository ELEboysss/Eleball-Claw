package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/middleware"
)

// AdminGateService 管理后台前置闸门服务（Pre-Auth Gate）
//
// 在用户进入 admin-web 登录页 / 调用 /v1/admin/* 接口之前，先校验一道密钥，
// 实现隐藏登录页防扫描 + per-IP 限速防暴力破解。叠加在 JWT / IP 白名单之上。
//
// 安全设计要点：
//   - 密钥使用 32 字节随机（openssl rand -hex 32），禁止 MD5 / 短口令
//   - 主备双 token，支持无停机轮换
//   - token 比较使用 crypto/subtle.ConstantTimeCompare，防时序侧信道
//   - 校验失败固定 sleep 300ms，拉长爆破成本（对单用户无感，对脚本致命）
//   - cookie 仅存随机 sessionId，不存 raw token；泄露不等于密钥泄露
//   - session 存内存 map（gateway 当前单实例），重启后需重新输入 token
type AdminGateService struct {
	enabled bool
	tokens  [][]byte // 主 + 备，常量时间比较

	cookieName string
	cookieTTL  time.Duration
	secure     bool // cookie Secure 属性（生产 HTTPS 必须 true）

	limiter         *middleware.RateLimiter // per-IP 闸门尝试限速
	failLockCount   int
	failLockDur     time.Duration

	mu          sync.Mutex
	failCounts  map[string]int        // per-IP 连续失败计数
	lockedUntil map[string]time.Time  // per-IP 锁定到期时间
	sessions    map[string]time.Time  // sessionId -> 到期时间（sliding 续期）
}

// NewAdminGateService 根据配置构造闸门服务；enabled=false 时返回禁用态实例。
func NewAdminGateService(cfg *config.AdminGateConfig, secure bool) *AdminGateService {
	s := &AdminGateService{
		enabled:      cfg.Enabled,
		cookieName:   cfg.CookieName,
		cookieTTL:    time.Duration(cfg.CookieTTL) * time.Second,
		secure:       secure,
		limiter:      middleware.NewRateLimiter(cfg.RatePerMinute),
		failLockCount: cfg.FailLockCount,
		failLockDur:   time.Duration(cfg.FailLockMinutes) * time.Minute,
		failCounts:   make(map[string]int),
		lockedUntil:  make(map[string]time.Time),
		sessions:     make(map[string]time.Time),
	}
	// 收集非空 token（主 + 备）作为常量时间比较集
	if cfg.Token != "" {
		s.tokens = append(s.tokens, []byte(cfg.Token))
	}
	if cfg.TokenPrev != "" {
		s.tokens = append(s.tokens, []byte(cfg.TokenPrev))
	}
	if cfg.CookieName == "" {
		s.cookieName = "eleball_admin_gate"
	}
	return s
}

// Enabled 返回闸门是否启用。
func (s *AdminGateService) Enabled() bool { return s.enabled }

// CookieName 返回闸门 cookie 名。
func (s *AdminGateService) CookieName() string { return s.cookieName }

// VerifyToken 常量时间校验密钥，命中主或备即放行；失败固定 sleep 300ms。
// 无论命中与否都比较全部 token，避免泄漏"命中了第几个"。
func (s *AdminGateService) VerifyToken(token string) bool {
	if !s.enabled {
		return true
	}
	t := []byte(token)
	match := 0
	for _, tk := range s.tokens {
		match |= subtle.ConstantTimeCompare(t, tk)
	}
	if match == 1 {
		return true
	}
	time.Sleep(300 * time.Millisecond)
	return false
}

// CheckRateLimit per-IP 闸门尝试限速，复用 middleware.RateLimiter。
// 返回是否允许及建议的重试秒数。
func (s *AdminGateService) CheckRateLimit(ip string) (allowed bool, retryAfterSec int) {
	if !s.enabled {
		return true, 0
	}
	allowed, _ = s.limiter.Allow(ip)
	if !allowed {
		return false, 30
	}
	return true, 0
}

// IsLocked 返回某 IP 是否处于失败锁定状态。
func (s *AdminGateService) IsLocked(ip string) bool {
	if !s.enabled {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.lockedUntil[ip]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		// 锁定过期，清零失败计数
		delete(s.lockedUntil, ip)
		delete(s.failCounts, ip)
		return false
	}
	return true
}

// RecordFail 记录一次失败，达阈值后锁定该 IP。
func (s *AdminGateService) RecordFail(ip string) {
	if !s.enabled || ip == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCounts[ip]++
	if s.failCounts[ip] >= s.failLockCount {
		s.lockedUntil[ip] = time.Now().Add(s.failLockDur)
	}
}

// ResetFail 校验成功后清零该 IP 的失败计数。
func (s *AdminGateService) ResetFail(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failCounts, ip)
	delete(s.lockedUntil, ip)
}

// IssueSession 颁发随机 sessionId 并写入内存会话表，返回 sessionId。
// cookie 只存 sessionId，不存 raw token；吊销只需清 sessions map。
func (s *AdminGateService) IssueSession() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	sid := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked()
	s.sessions[sid] = time.Now().Add(s.cookieTTL)
	return sid, nil
}

// VerifySession 校验 sessionId 是否有效，命中则 sliding 续期。
func (s *AdminGateService) VerifySession(sid string) bool {
	if !s.enabled || sid == "" {
		return !s.enabled // 禁用闸门时一律放行
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.sessions[sid]
	if !ok || time.Now().After(expiry) {
		if ok {
			delete(s.sessions, sid)
		}
		return false
	}
	// sliding 续期
	s.sessions[sid] = time.Now().Add(s.cookieTTL)
	return true
}

// RevokeSession 吊销指定会话。
func (s *AdminGateService) RevokeSession(sid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sid)
}

// purgeExpiredLocked 清理过期会话（调用方需持锁）。lazy 清理，避免单独 goroutine。
func (s *AdminGateService) purgeExpiredLocked() {
	now := time.Now()
	for sid, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, sid)
		}
	}
}

// SetCookie 向响应写入闸门 cookie（HttpOnly + Secure + SameSite=Strict + Path=/admin）。
func (s *AdminGateService) SetCookie(w http.ResponseWriter, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    sid,
		Path:     "/admin",
		MaxAge:   int(s.cookieTTL / time.Second),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearCookie 清除闸门 cookie（登出/吊销时用）。
func (s *AdminGateService) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ReadCookie 从请求读取闸门 sessionId。
func (s *AdminGateService) ReadCookie(r *http.Request) string {
	c, err := r.Cookie(s.cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
