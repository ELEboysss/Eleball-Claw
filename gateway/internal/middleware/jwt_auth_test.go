package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eleball/gateway/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// fakeTransient 仿造 service.CloudTransientError（middleware 不能导入 service，会成导入环）
type fakeTransient struct{}

func (fakeTransient) Error() string   { return "cloud unreachable" }
func (fakeTransient) Transient() bool { return true }

// setupFallbackRouter 构造仅走云端回退验证的路由（本地密钥必然验签失败）
func setupFallbackRouter(validate func(ctx context.Context, token string) (string, string, error)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	jwtUtil := util.NewJWTUtil("local-secret", 2, 720)
	r.GET("/ping", JWTAuthCloudFallback(jwtUtil, validate), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})
	return r
}

func bearerRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestJWTAuthFallbackTransient503 云端暂时性故障返回 503 而非 401，
// 前端据此不清理会话（修复「云端抖动导致误登出、重登也无效」）。
func TestJWTAuthFallbackTransient503(t *testing.T) {
	r := setupFallbackRouter(func(ctx context.Context, token string) (string, string, error) {
		return "", "", fakeTransient{}
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, bearerRequest("any-token"))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "云端登录校验暂不可用")
}

// TestJWTAuthFallbackInvalid401 云端明确拒绝（token 失效）返回 401，前端按重新登录处理。
func TestJWTAuthFallbackInvalid401(t *testing.T) {
	r := setupFallbackRouter(func(ctx context.Context, token string) (string, string, error) {
		return "", "", errors.New("云端 token 校验未通过")
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, bearerRequest("expired-token"))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestJWTAuthFallbackSuccess 回退验证通过时注入用户上下文。
func TestJWTAuthFallbackSuccess(t *testing.T) {
	r := setupFallbackRouter(func(ctx context.Context, token string) (string, string, error) {
		return "u1", "user", nil
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, bearerRequest("good-token"))
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestJWTAuthLocalSecretFastPath 本地密钥可验的 token 不走云端回退。
func TestJWTAuthLocalSecretFastPath(t *testing.T) {
	called := false
	r := setupFallbackRouter(func(ctx context.Context, token string) (string, string, error) {
		called = true
		return "", "", errors.New("should not be called")
	})
	jwtUtil := util.NewJWTUtil("local-secret", 2, 720)
	token, _ := jwtUtil.GenerateAccessToken("u1", "d1", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, bearerRequest(token))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, called, "本地验签通过不应触发云端回退")
}
