package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/eleball/gateway/pkg/util"
	"github.com/gin-gonic/gin"
)

// JWTAuth JWT 认证中间件
func JWTAuth(jwtUtil *util.JWTUtil) gin.HandlerFunc {
	return JWTAuthCloudFallback(jwtUtil, nil)
}

// JWTAuthCloudFallback claw 专用 JWT 认证：先按本地密钥验签，失败时回退云端验证。
//
// 背景：安装脚本为本地生成随机 JWT 密钥（不可能也不应与每个用户设备共享云端签名密钥），
// 云端签发的 token 本地验签必然失败；不回落云端验证时，脚本安装的用户登录后所有
// 本地接口一律 401，表现为「登录后闪一下又回到登录页」。validate 为云端验证函数
// （通常 CloudAccountService.ValidateToken），为 nil 时退化为仅本地验签。
func JWTAuthCloudFallback(jwtUtil *util.JWTUtil, validate func(ctx context.Context, token string) (userID, role string, err error)) gin.HandlerFunc {
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
		token := parts[1]

		// 快路径：本地密钥验签（与云端共享密钥的部署）
		if claims, err := jwtUtil.ParseToken(token); err == nil {
			if claims.TokenType != "access" {
				c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "登录信息异常，请重新登录"})
				c.Abort()
				return
			}
			c.Set("user_id", claims.UserID)
			c.Set("device_id", claims.DeviceID)
			c.Set("role", claims.Role)
			// 注入调用方 token，供云端代理配置自动凭证使用
			c.Request = c.Request.WithContext(util.WithAuthToken(c.Request.Context(), token))
			c.Next()
			return
		}

		// 回退：云端 /auth/me 验证（本地随机密钥的安装方式）
		if validate != nil {
			if userID, role, err := validate(c.Request.Context(), token); err == nil {
				c.Set("user_id", userID)
				c.Set("role", role)
				// 注入调用方 token，供云端代理配置自动凭证使用
				c.Request = c.Request.WithContext(util.WithAuthToken(c.Request.Context(), token))
				c.Next()
				return
			}
		}

		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "登录已过期，请重新登录"})
		c.Abort()
	}
}
