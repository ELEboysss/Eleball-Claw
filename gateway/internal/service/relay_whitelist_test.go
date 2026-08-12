package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestIsRelayAllowedPath 白名单纯函数：App 对话链路放行，本地管理面与其余一律拒绝。
func TestIsRelayAllowedPath(t *testing.T) {
	allowed := []string{
		"/v1/chat/completions",
		"/v1/chat/completions?stream=true",
		"/v1/agent/execute",
		"/v1/agent/sessions",
		"/v1/agent/sessions?page=1&page_size=20", // 查询串不影响匹配
		"/v1/agent/sessions/s1/state",
		"/v1/agent/running/events",
		"/v1/agent/approve",
		"/v1/conversations",
		"/v1/conversations/c1/messages",
		"/v1/agents/skill-1/active",
		"/v1/agents/skill-1/credentials",
		"/v1/assistants",
		"/v1/assistants/a1",
		"/v1/assistants/a1/items",
		"/v1/sync/pull",
		"/v1/devices",
	}
	for _, p := range allowed {
		assert.True(t, isRelayAllowedPath(p), "应放行: %s", p)
	}

	denied := []string{
		"/v1/claw-console/modules",              // 本地管理面
		"/v1/claw-console/system/status",        // 本地管理面
		"/v1/agents",                            // 集市列表恒走云端
		"/v1/agents/skill-1",                    // agents 详情未放行
		"/v1/agents/skill-1/purchase",           // 购买不经 relay
		"/v1/agents/skill-1/active/extra",       // 深层路径混入
		"/v1/auth/login",                        // 认证面
		"/v1/orders",                            // 订单面
		"/v1/admin/users",                       // 管理面
		"/v1/market/modules",                    // 集市模块
		"/v1/chat/completions/extra",            // 精确匹配外的变体
		"/v1/agent",                             // 缺少子路径
		"/v1/agents//active",                    // 空 id
		"/v1/agent/../claw-console/modules",     // 路径穿越
		"agent/execute",                         // 非绝对路径
		"",
	}
	for _, p := range denied {
		assert.False(t, isRelayAllowedPath(p), "应拒绝: %s", p)
	}
}

// countingUpstream 统计被命中的本地网关请求数（断言白名单拦截后未触达本地）
type countingUpstream struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newCountingUpstream(t *testing.T) *countingUpstream {
	u := &countingUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"message":"ok"}`)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// TestRelayTunnel_PathWhitelistRejected 非白名单路径回 403 且不触达本地网关。
func TestRelayTunnel_PathWhitelistRejected(t *testing.T) {
	upstream := newCountingUpstream(t)
	rec := newWSRecorder(t)
	conn := rec.dial(t)

	tun := NewRelayTunnel("", "", "", upstream.srv.URL, zap.NewNop(), nil)
	tun.handleRequest(context.Background(), conn, 21, `{"method":"GET","path":"/v1/claw-console/modules"}`)

	f := rec.nextFrame(t)
	require.Equal(t, "data", f.Type)
	var resp relayResponse
	require.NoError(t, json.Unmarshal([]byte(f.Payload), &resp))
	assert.Equal(t, http.StatusForbidden, resp.Status)
	assert.Equal(t, int32(0), upstream.hits.Load(), "被拦截的请求不应触达本地网关")
	rec.assertNoMoreFrames(t)
}

// TestRelayTunnel_PathWhitelistAllowed 白名单路径正常透传到本地网关。
func TestRelayTunnel_PathWhitelistAllowed(t *testing.T) {
	upstream := newCountingUpstream(t)
	rec := newWSRecorder(t)
	conn := rec.dial(t)

	tun := NewRelayTunnel("", "", "", upstream.srv.URL, zap.NewNop(), nil)
	tun.handleRequest(context.Background(), conn, 22, `{"method":"GET","path":"/v1/conversations?page=1"}`)

	f := rec.nextFrame(t)
	require.Equal(t, "data", f.Type)
	var resp relayResponse
	require.NoError(t, json.Unmarshal([]byte(f.Payload), &resp))
	assert.Equal(t, http.StatusOK, resp.Status)
	assert.Equal(t, int32(1), upstream.hits.Load(), "白名单路径应透传到本地网关")
}

// TestRelayTunnel_RejectsPlaintextWhenCipherEnabled S09-C2d：cipher 非 nil 时收到
// 明文帧（未经 E2E 加密的 payload）拒绝处理——回 400 且不触达本地网关。
func TestRelayTunnel_RejectsPlaintextWhenCipherEnabled(t *testing.T) {
	upstream := newCountingUpstream(t)
	rec := newWSRecorder(t)
	conn := rec.dial(t)

	clawCipher, err := NewE2ECipher()
	require.NoError(t, err)
	tun := NewRelayTunnel("", "", "", upstream.srv.URL, zap.NewNop(), clawCipher)

	// 明文 data 帧（未加密）：cipher 非 nil 时解密必失败，拒绝处理
	tun.handleRequest(context.Background(), conn, 23, `{"method":"GET","path":"/v1/conversations"}`)

	f := rec.nextFrame(t)
	require.Equal(t, "data", f.Type)
	var resp relayResponse
	require.NoError(t, json.Unmarshal([]byte(f.Payload), &resp))
	assert.Equal(t, http.StatusBadRequest, resp.Status)
	assert.Equal(t, int32(0), upstream.hits.Load(), "明文帧不应触达本地网关")
	rec.assertNoMoreFrames(t)
}

// TestRelayTunnel_StartRefusesPlaintext S09-C2d：配置齐全但 cipher 为 nil 时
// Start 拒绝启动（不再静默回落明文）。
func TestRelayTunnel_StartRefusesPlaintext(t *testing.T) {
	tun := NewRelayTunnel("ws://127.0.0.1:1", "dev-1", "jwt", "http://127.0.0.1:1", zap.NewNop(), nil)
	tun.Start()
	assert.False(t, tun.started.Load(), "cipher 为 nil 时 relay 不应启动")
	tun.Stop() // 未启动时 Stop 应立即返回（不阻塞）

	// 对照：cipher 非 nil 时正常启动（连不上会重连，仅断言 started 标记）
	clawCipher, err := NewE2ECipher()
	require.NoError(t, err)
	tun2 := NewRelayTunnel("ws://127.0.0.1:1", "dev-1", "jwt", "http://127.0.0.1:1", zap.NewNop(), clawCipher)
	tun2.Start()
	assert.True(t, tun2.started.Load(), "cipher 就绪时 relay 应启动")
	tun2.Stop()
}
