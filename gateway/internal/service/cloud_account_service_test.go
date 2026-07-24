package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCloudMeServer 按状态码模拟云端 /auth/me
func newCloudMeServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status == http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"user_id":"u1","username":"a","role":"user","vip_level":0}}`))
			return
		}
		w.WriteHeader(status)
	}))
}

// TestValidateTokenErrorTyping 固定 401/503 区分语义：
// 云端明确 401 → 普通错误（按登录失效处理）；429/5xx/网络错误 → *CloudTransientError
// （按暂时性故障处理，不得登出用户）。该区分是「云端抖动导致误清会话」修复的核心契约。
func TestValidateTokenErrorTyping(t *testing.T) {
	ctx := context.Background()

	// 有效 token
	svc := NewCloudAccountService(newCloudMeServer(t, http.StatusOK).URL)
	uid, role, err := svc.ValidateToken(ctx, "good-token")
	require.NoError(t, err)
	assert.Equal(t, "u1", uid)
	assert.Equal(t, "user", role)

	// 云端 401 → 非暂时性（登录失效）
	svc401 := NewCloudAccountService(newCloudMeServer(t, http.StatusUnauthorized).URL)
	_, _, err = svc401.ValidateToken(ctx, "bad-token")
	require.Error(t, err)
	var transient *CloudTransientError
	assert.False(t, errors.As(err, &transient), "云端明确 401 不应归类为暂时性故障")

	// 云端 429 / 503 → 暂时性故障
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusInternalServerError} {
		svcX := NewCloudAccountService(newCloudMeServer(t, status).URL)
		_, _, err = svcX.ValidateToken(ctx, "any-token")
		require.Error(t, err)
		assert.True(t, errors.As(err, &transient), "HTTP %d 应归类为暂时性故障", status)
	}

	// 网络不可达 → 暂时性故障
	svcDown := NewCloudAccountService("http://127.0.0.1:1")
	_, _, err = svcDown.ValidateToken(ctx, "any-token")
	require.Error(t, err)
	assert.True(t, errors.As(err, &transient), "网络不可达应归类为暂时性故障")

	// 有效校验走缓存，不再重复打云端
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"user_id":"u1","username":"a","role":"user","vip_level":0}}`))
	}))
	defer srv.Close()
	svcCache := NewCloudAccountService(srv.URL)
	_, _, _ = svcCache.ValidateToken(ctx, "cached-token")
	_, _, _ = svcCache.ValidateToken(ctx, "cached-token")
	assert.Equal(t, 1, calls, "5 分钟缓存内不应重复请求云端")
}

// TestValidateTokenTimeout 云端超时（http client 10s 超时）归类为暂时性故障。
// 用短超时客户端模拟，避免测试等待 10 秒。
func TestValidateTokenTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	svc := NewCloudAccountService(srv.URL)
	svc.http.Timeout = 50 * time.Millisecond
	_, _, err := svc.ValidateToken(context.Background(), "any-token")
	require.Error(t, err)
	var transient *CloudTransientError
	assert.True(t, errors.As(err, &transient), "超时应归类为暂时性故障")
}
