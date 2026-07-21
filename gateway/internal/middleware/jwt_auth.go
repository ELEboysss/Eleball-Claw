package middleware

import (
	"net/http"
	"strings"

	"github.com/eleball/gateway/pkg/util"
	"github.com/gin-gonic/gin"
)

// JWTAuth JWT 认证中间件
func JWTAuth(jwtUtil *util.JWTUtil) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "登录状态为空，请先登录"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "登录信息格式错误，请重新登录"})
			c.Abort()
			return
		}

		claims, err := jwtUtil.ParseToken(parts[1])
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "登录已过期，请重新登录"})
			c.Abort()
			return
		}

		if claims.TokenType != "access" {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "登录信息异常，请重新登录"})
			c.Abort()
			return
		}

		// 将用户信息注入上下文
		c.Set("user_id", claims.UserID)
		c.Set("device_id", claims.DeviceID)
		c.Set("role", claims.Role)
		c.Next()
	}
}
