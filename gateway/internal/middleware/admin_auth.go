package middleware

import (
	"net/http"
	"strings"

	"github.com/eleball/gateway/pkg/util"
	"github.com/gin-gonic/gin"
)

// AdminAuth 管理员权限校验中间件
// 依赖 JWTAuth 先执行，从上下文中读取 claims
func AdminAuth(jwtUtil *util.JWTUtil) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 先执行 JWT 认证
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "登录状态为空，请先登录"})
			c.Abort()
			return
		}

		// 与 JWTAuth 一致先校验 Bearer 前缀，避免直接切片导致越界 panic
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

		if claims.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"code": 3001, "message": "权限不足，需要管理员权限"})
			c.Abort()
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("role", claims.Role)
		c.Next()
	}
}
