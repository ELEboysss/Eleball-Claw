package main

// e2e 管理后台前置闸门（Pre-Auth Gate）—— 纯标准库实现
//
// 镜像 internal/service.AdminGateService 的行为（双 token 常量时间校验 + per-IP 限速 +
// 失败锁定 + 内存 session + sliding cookie），供 E2E 测试与前端联调使用。
// e2e 不依赖 gin/gorm，故独立实现，保持轻量。
//
// 与正式网关的差异（环境适配，非行为差异）：
//   - cookie Path 用 "/"：e2e 无 nginx，/v1/admin/* 与 /admin/knock 不在同一路径前缀，
//     需 cookie 覆盖两者；正式网关经 nginx auth_request 子请求带原始 /admin/ cookie，故 Path="/admin"。
//   - 闸门默认关闭（ADMIN_GATE_ENABLED 默认 false）：保证现有 E2E 测试不破；
//     Playwright 闸门用例通过 env 显式启用。

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type e2eAdminGate struct {
	enabled bool
	tokens  [][]byte

	cookieName string
	cookieTTL  time.Duration

	ratePerMinute int
	rateMu        sync.Mutex
	rateMap       map[string][]time.Time

	failLockCount int
	failLockDur   time.Duration
	failMu        sync.Mutex
	failCounts    map[string]int
	lockedUntil   map[string]time.Time

	sessMu   sync.Mutex
	sessions map[string]time.Time
}

func newE2EAdminGate() *e2eAdminGate {
	g := &e2eAdminGate{
		enabled:       os.Getenv("ADMIN_GATE_ENABLED") == "true",
		cookieName:    getenvDefault("ADMIN_GATE_COOKIE_NAME", "eleball_admin_gate"),
		cookieTTL:     7 * 24 * time.Hour,
		ratePerMinute: 5,
		rateMap:       make(map[string][]time.Time),
		failLockCount: 20,
		failLockDur:   30 * time.Minute,
		failCounts:    make(map[string]int),
		lockedUntil:   make(map[string]time.Time),
		sessions:      make(map[string]time.Time),
	}
	if t := os.Getenv("ADMIN_GATE_TOKEN"); t != "" {
		g.tokens = append(g.tokens, []byte(t))
	}
	if t := os.Getenv("ADMIN_GATE_TOKEN_PREV"); t != "" {
		g.tokens = append(g.tokens, []byte(t))
	}
	return g
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (g *e2eAdminGate) Enabled() bool { return g.enabled }

// VerifyToken 常量时间校验主/备 token，失败 sleep 300ms。
func (g *e2eAdminGate) VerifyToken(token string) bool {
	if !g.enabled {
		return true
	}
	t := []byte(token)
	match := 0
	for _, tk := range g.tokens {
		match |= subtle.ConstantTimeCompare(t, tk)
	}
	if match == 1 {
		return true
	}
	time.Sleep(300 * time.Millisecond)
	return false
}

// checkRateLimit per-IP 滑动窗口限速（镜像 middleware.RateLimiter）。
func (g *e2eAdminGate) checkRateLimit(ip string) bool {
	g.rateMu.Lock()
	defer g.rateMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	valid := g.rateMap[ip][:0]
	for _, t := range g.rateMap[ip] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= g.ratePerMinute {
		g.rateMap[ip] = valid
		return false
	}
	valid = append(valid, now)
	g.rateMap[ip] = valid
	return true
}

func (g *e2eAdminGate) isLocked(ip string) bool {
	g.failMu.Lock()
	defer g.failMu.Unlock()
	until, ok := g.lockedUntil[ip]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(g.lockedUntil, ip)
		delete(g.failCounts, ip)
		return false
	}
	return true
}

func (g *e2eAdminGate) recordFail(ip string) {
	if !g.enabled || ip == "" {
		return
	}
	g.failMu.Lock()
	defer g.failMu.Unlock()
	g.failCounts[ip]++
	if g.failCounts[ip] >= g.failLockCount {
		g.lockedUntil[ip] = time.Now().Add(g.failLockDur)
	}
}

func (g *e2eAdminGate) resetFail(ip string) {
	g.failMu.Lock()
	defer g.failMu.Unlock()
	delete(g.failCounts, ip)
	delete(g.lockedUntil, ip)
}

func (g *e2eAdminGate) issueSession() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	sid := hex.EncodeToString(b)
	g.sessMu.Lock()
	defer g.sessMu.Unlock()
	g.sessions[sid] = time.Now().Add(g.cookieTTL)
	return sid, nil
}

func (g *e2eAdminGate) verifySession(sid string) bool {
	if !g.enabled {
		return true
	}
	g.sessMu.Lock()
	defer g.sessMu.Unlock()
	exp, ok := g.sessions[sid]
	if !ok || time.Now().After(exp) {
		if ok {
			delete(g.sessions, sid)
		}
		return false
	}
	g.sessions[sid] = time.Now().Add(g.cookieTTL) // sliding 续期
	return true
}

func (g *e2eAdminGate) readCookie(r *http.Request) string {
	c, err := r.Cookie(g.cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// setCookie e2e 用 Path="/"（见文件头注释）。
func (g *e2eAdminGate) setCookie(w http.ResponseWriter, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     g.cookieName,
		Value:    sid,
		Path:     "/",
		MaxAge:   int(g.cookieTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// Middleware 全局中间件：对 /v1/admin/* 应用闸门（enabled=false 时放行）。
func (g *e2eAdminGate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.enabled && strings.HasPrefix(r.URL.Path, "/v1/admin/") {
			if !g.verifySession(g.readCookie(r)) {
				writeGateJSON(w, http.StatusUnauthorized, map[string]interface{}{
					"code": 1001, "message": "访问口令未验证，请先完成闸门验证",
				})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// 闸门三端点
func (g *e2eAdminGate) knockPageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(e2eKnockHTML))
}

func (g *e2eAdminGate) verifyHandler(w http.ResponseWriter, r *http.Request) {
	if !g.enabled {
		writeGateJSON(w, http.StatusOK, map[string]interface{}{
			"code": 0, "message": "success", "data": map[string]string{"redirect": "/admin/"},
		})
		return
	}
	ip := gateClientIP(r)
	if !g.checkRateLimit(ip) {
		w.Header().Set("Retry-After", "30")
		writeGateJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"code": 1003, "message": "请求过于频繁，请稍后再试",
		})
		return
	}
	if g.isLocked(ip) {
		writeGateJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"code": 1003, "message": "尝试次数过多，已锁定，请稍后再试",
		})
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Token == "" {
		req.Token = r.FormValue("token")
	}
	if !g.VerifyToken(req.Token) {
		g.recordFail(ip)
		writeGateJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"code": 1001, "message": "口令错误",
		})
		return
	}
	sid, err := g.issueSession()
	if err != nil {
		writeGateJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"code": 1000, "message": "会话颁发失败",
		})
		return
	}
	g.resetFail(ip)
	g.setCookie(w, sid)
	writeGateJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0, "message": "success", "data": map[string]string{"redirect": "/admin/"},
	})
}

func (g *e2eAdminGate) checkHandler(w http.ResponseWriter, r *http.Request) {
	if !g.enabled || g.verifySession(g.readCookie(r)) {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusUnauthorized)
}

// writeGateJSON 写 JSON 响应（先状态码后 body）。
func writeGateJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

// gateClientIP 取真实客户端 IP（X-Real-IP / X-Forwarded-For / RemoteAddr）。
func gateClientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// e2eKnockHTML 闸门输入页（简化版，功能与正式网关一致：输入口令 -> POST /_admin_gate）。
const e2eKnockHTML = `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Eleball 管理后台 · 访问验证（E2E）</title>
<style>body{font-family:system-ui,sans-serif;background:#0f172a;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.c{background:#1e293b;border:1px solid #334155;border-radius:12px;padding:2rem;max-width:340px;width:90%}
h1{font-size:1.4rem;margin:0 0 .25rem}h1 b{color:#6366f1}
.s{color:#94a3b8;font-size:.8rem;margin-bottom:1.5rem}
input{width:100%;padding:.65rem;margin:.3rem 0;border:1px solid #475569;background:#0f172a;color:#e2e8f0;border-radius:8px;box-sizing:border-box;font-family:monospace}
button{width:100%;padding:.7rem;background:#6366f1;color:#fff;border:0;border-radius:8px;font-size:.9rem;cursor:pointer}
.m{min-height:1rem;text-align:center;color:#f87171;font-size:.8rem;margin-top:.5rem}</style></head>
<body><div class="c"><h1>Ele<b>ball</b></h1><div class="s">管理后台 · 访问验证（E2E）</div>
<form id="f" autocomplete="off"><input id="t" type="password" placeholder="访问口令" autofocus>
<button type="submit">验证并进入</button><div class="m" id="m"></div></form></div>
<script>document.getElementById('f').addEventListener('submit',function(e){
e.preventDefault();var t=document.getElementById('t').value.trim();var m=document.getElementById('m');
if(!t){m.textContent='请输入访问口令';return;}
fetch('/_admin_gate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:t})})
.then(function(r){return r.json().then(function(d){return{status:r.status,data:d};});})
.then(function(r){if(r.status===200&&r.data&&r.data.code===0){window.location.href='/admin/';}
else{m.textContent=(r.data&&r.data.message)||'口令错误';}}).catch(function(){m.textContent='网络异常';});});
</script></body></html>`
