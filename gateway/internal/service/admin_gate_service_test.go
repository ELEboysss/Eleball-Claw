package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/stretchr/testify/assert"
)

// newTestGate 构造测试用闸门服务：短 TTL / 低阈值，便于测试过期与锁定。
func newTestGate(t *testing.T, token, tokenPrev string) *AdminGateService {
	t.Helper()
	cfg := &config.AdminGateConfig{
		Enabled:         true,
		Token:           token,
		TokenPrev:       tokenPrev,
		CookieName:      "test_gate",
		CookieTTL:       1, // 1 秒，便于测过期
		RatePerMinute:   3,
		FailLockCount:   3,
		FailLockMinutes: 30,
	}
	return NewAdminGateService(cfg, false)
}

func randToken(c byte) string { return strings.Repeat(string(c), 64) }

// TestVerifyToken 主/备 token 命中、错误 token 拒绝。
func TestVerifyToken(t *testing.T) {
	svc := newTestGate(t, randToken('a'), randToken('b'))

	assert.True(t, svc.VerifyToken(randToken('a')), "主 token 应命中")
	assert.True(t, svc.VerifyToken(randToken('b')), "备 token 应命中")
	assert.False(t, svc.VerifyToken(randToken('x')), "错误 token 应拒绝")
	assert.False(t, svc.VerifyToken(""), "空 token 应拒绝")
}

// TestCheckRateLimit per-IP 限速：达阈值后拒绝。
func TestCheckRateLimit(t *testing.T) {
	svc := newTestGate(t, randToken('a'), "")
	ip := "1.2.3.4"

	for i := 0; i < 3; i++ {
		ok, _ := svc.CheckRateLimit(ip)
		assert.True(t, ok, "第 %d 次应允许", i+1)
	}
	ok, retry := svc.CheckRateLimit(ip)
	assert.False(t, ok, "第 4 次应限速拒绝")
	assert.Equal(t, 30, retry, "Retry-After 应为 30 秒")

	// 不同 IP 不受限
	ok2, _ := svc.CheckRateLimit("5.6.7.8")
	assert.True(t, ok2, "不同 IP 应允许")
}

// TestFailLock 连续失败达阈值后锁定，成功后解锁。
func TestFailLock(t *testing.T) {
	svc := newTestGate(t, randToken('a'), "")
	ip := "9.9.9.9"

	assert.False(t, svc.IsLocked(ip), "初始未锁定")
	svc.RecordFail(ip)
	svc.RecordFail(ip)
	assert.False(t, svc.IsLocked(ip), "2 次失败未达阈值")
	svc.RecordFail(ip)
	assert.True(t, svc.IsLocked(ip), "3 次失败应锁定")

	// 成功后清零
	svc.ResetFail(ip)
	assert.False(t, svc.IsLocked(ip), "ResetFail 后应解锁")
}

// TestSessionIssueVerify 颁发与校验会话。
func TestSessionIssueVerify(t *testing.T) {
	svc := newTestGate(t, randToken('a'), "")

	sid, err := svc.IssueSession()
	assert.NoError(t, err)
	assert.Len(t, sid, 64, "sessionId 应为 32 字节 hex(64 字符)")

	assert.True(t, svc.VerifySession(sid), "有效 sessionId 应通过")
	assert.False(t, svc.VerifySession("invalid-sid"), "无效 sessionId 应拒绝")
	assert.False(t, svc.VerifySession(""), "空 sessionId 应拒绝")

	// sliding 续期：多次校验仍有效
	assert.True(t, svc.VerifySession(sid), "第二次校验仍应通过")
}

// TestSessionExpire 会话过期后失效。
func TestSessionExpire(t *testing.T) {
	svc := newTestGate(t, randToken('a'), "") // TTL=1s

	sid, _ := svc.IssueSession()
	assert.True(t, svc.VerifySession(sid), "立即校验应通过")
	time.Sleep(1500 * time.Millisecond) // 等 TTL 过期
	assert.False(t, svc.VerifySession(sid), "过期后应失效")
}

// TestSessionRevoke 吊销会话后失效。
func TestSessionRevoke(t *testing.T) {
	svc := newTestGate(t, randToken('a'), randToken('b'))
	sid, _ := svc.IssueSession()
	assert.True(t, svc.VerifySession(sid))
	svc.RevokeSession(sid)
	assert.False(t, svc.VerifySession(sid), "吊销后应失效")
}

// TestCookieReadWrite cookie 设置与读取。
func TestCookieReadWrite(t *testing.T) {
	svc := newTestGate(t, randToken('a'), "")

	w := httptest.NewRecorder()
	svc.SetCookie(w, "session-abc")
	r := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	r.AddCookie(&http.Cookie{Name: "test_gate", Value: "session-abc"})

	assert.Equal(t, "session-abc", svc.ReadCookie(r), "应读回 cookie 值")
	// 验证 cookie 属性
	cookies := w.Result().Cookies()
	assert.Len(t, cookies, 1)
	c := cookies[0]
	assert.Equal(t, "test_gate", c.Name)
	assert.True(t, c.HttpOnly, "cookie 应 HttpOnly")
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite, "cookie 应 SameSite=Strict")
	assert.Equal(t, "/admin", c.Path, "正式网关 cookie Path 应为 /admin")
}

// TestDisabledGate 闸门禁用时一律放行。
func TestDisabledGate(t *testing.T) {
	cfg := &config.AdminGateConfig{Enabled: false, CookieName: "g", CookieTTL: 3600, RatePerMinute: 5, FailLockCount: 20, FailLockMinutes: 30}
	svc := NewAdminGateService(cfg, false)

	assert.False(t, svc.Enabled())
	assert.True(t, svc.VerifyToken("anything"), "禁用时 token 校验放行")
	assert.True(t, svc.VerifySession("anything"), "禁时会话校验放行")
	assert.False(t, svc.IsLocked("1.1.1.1"), "禁时不锁定")
	ok, _ := svc.CheckRateLimit("1.1.1.1")
	assert.True(t, ok, "禁时限速放行")
}
