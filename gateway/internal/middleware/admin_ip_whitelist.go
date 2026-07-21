package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/eleball/gateway/internal/config"
	"github.com/gin-gonic/gin"
)

// AdminIPWhitelist 管理后台接口 IP 白名单中间件
// 当配置 admin.ip_whitelist_enabled=true 时，仅允许 admin.allowed_ips 中指定的
// 单个 IP 或 CIDR 网段访问管理后台路由组。
func AdminIPWhitelist(cfg *config.AdminConfig) gin.HandlerFunc {
	if cfg == nil || !cfg.IPWhitelistEnabled {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	allowedNets := make([]*net.IPNet, 0, len(cfg.AllowedIPs))
	allowedIPs := make([]string, 0, len(cfg.AllowedIPs))
	for _, item := range cfg.AllowedIPs {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			_, ipNet, err := net.ParseCIDR(item)
			if err == nil {
				allowedNets = append(allowedNets, ipNet)
			}
		} else {
			allowedIPs = append(allowedIPs, item)
		}
	}

	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		if clientIP == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 4003, "message": "无法获取客户端 IP，拒绝访问"})
			return
		}

		ip := net.ParseIP(clientIP)
		if ip == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 4003, "message": "无法解析客户端 IP，拒绝访问"})
			return
		}

		// 匹配单个 IP
		for _, allowed := range allowedIPs {
			if allowed == clientIP {
				c.Next()
				return
			}
		}

		// 匹配 CIDR
		for _, ipNet := range allowedNets {
			if ipNet.Contains(ip) {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 4003, "message": "当前 IP 不在管理后台白名单内"})
	}
}
