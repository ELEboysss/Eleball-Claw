package handler

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

//go:embed admin_gate_assets/knock.html
var knockHTML []byte

// AdminGateHandler 管理后台前置闸门 HTTP 处理器
//
// 端点：
//   - GET  /admin/knock        返回品牌化闸门输入页（embed 内嵌，无外部依赖）
//   - POST /_admin_gate        校验密钥，成功颁发 session cookie
//   - GET  /_admin_gate_check  nginx auth_request 子请求，校验 cookie 200/401
type AdminGateHandler struct {
	svc *service.AdminGateService
}

// NewAdminGateHandler 构造闸门处理器。
func NewAdminGateHandler(svc *service.AdminGateService) *AdminGateHandler {
	return &AdminGateHandler{svc: svc}
}

// KnockPage GET /admin/knock 返回闸门输入页 HTML。
// 该页不回显任何系统信息，防止探测。
func (h *AdminGateHandler) KnockPage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", knockHTML)
}

// Verify POST /_admin_gate 校验访问口令。
// 流程：限速 -> 锁定检查 -> 常量时间校验 -> 成功颁发 session cookie。
// 成功返回 JSON（前端 location.href 跳转），失败返回 401 并记录失败计数。
func (h *AdminGateHandler) Verify(c *gin.Context) {
	if !h.svc.Enabled() {
		// 闸门未启用，直接放行进入登录页
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"redirect": "/admin/"}})
		return
	}

	ip := c.ClientIP()

	// 1. per-IP 限速
	if allowed, retry := h.svc.CheckRateLimit(ip); !allowed {
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{"code": 1003, "message": "请求过于频繁，请稍后再试"})
		return
	}

	// 2. 失败锁定检查
	if h.svc.IsLocked(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"code": 1003, "message": "尝试次数过多，已锁定，请稍后再试"})
		return
	}

	// 3. 解析口令（兼容 JSON body 与表单）
	var req struct {
		Token string `json:"token"`
	}
	_ = c.ShouldBindJSON(&req)
	token := req.Token
	if token == "" {
		token = c.PostForm("token")
	}

	// 4. 常量时间校验
	if !h.svc.VerifyToken(token) {
		h.svc.RecordFail(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"code": 1001, "message": "口令错误"})
		return
	}

	// 5. 成功：颁发 session cookie
	sid, err := h.svc.IssueSession()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": "会话颁发失败"})
		return
	}
	h.svc.ResetFail(ip)
	h.svc.SetCookie(c.Writer, sid)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"redirect": "/admin/"}})
}

// Check GET /_admin_gate_check nginx auth_request 子请求。
// 仅返回状态码：200 放行（cookie 有效），401 拦截（nginx 据此 302 到 knock 页）。
func (h *AdminGateHandler) Check(c *gin.Context) {
	if !h.svc.Enabled() {
		c.Status(http.StatusOK)
		return
	}
	sid := h.svc.ReadCookie(c.Request)
	if !h.svc.VerifySession(sid) {
		c.Status(http.StatusUnauthorized)
		return
	}
	c.Status(http.StatusOK)
}

// Enforce gin 中间件：gateway 侧纵深防御。
// 即便 nginx 配错直连 gateway:8080 暴露 /v1/admin/*，此中间件仍会拦截无闸门 cookie 的请求。
// 闸门禁用时直接放行（退回 JWT+IP 白名单）。
func (h *AdminGateHandler) Enforce() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.svc.Enabled() {
			c.Next()
			return
		}
		sid := h.svc.ReadCookie(c.Request)
		if !h.svc.VerifySession(sid) {
			// 浏览器访问：重定向到 knock 页；API 调用：返回 401 JSON
			accept := c.GetHeader("Accept")
			if isBrowserRequest(accept) {
				c.Redirect(http.StatusFound, "/admin/knock")
				c.Abort()
				return
			}
			c.JSON(http.StatusUnauthorized, gin.H{"code": 1001, "message": "访问口令未验证，请先完成闸门验证"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// isBrowserRequest 判断是否为浏览器导航请求（Accept 含 text/html）。
// 浏览器访问重定向到 knock 页；API 调用返回 401 JSON。
func isBrowserRequest(accept string) bool {
	return strings.Contains(accept, "text/html")
}
