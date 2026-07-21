package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// LocalhostOnly 仅允许本机 127.0.0.1 或 ::1 访问。
// 用于 /_internal/* 等内部管理接口，避免通过网络被外部调用。
func LocalhostOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if ip != "127.0.0.1" && ip != "::1" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 4003, "message": "forbidden"})
			return
		}
		c.Next()
	}
}

// IsLocalhost 判断给定 IP 是否为本机地址（支持 IPv4/IPv6 常见形式）。
func IsLocalhost(ip string) bool {
	ip = strings.TrimSpace(ip)
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost"
}
