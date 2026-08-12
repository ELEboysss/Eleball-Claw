package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// wsRecorder 模拟 relay 服务端：升级 WS 后把收到的帧全部收入 channel 供断言。
type wsRecorder struct {
	srv    *httptest.Server
	frames chan relayMsg
}

func newWSRecorder(t *testing.T) *wsRecorder {
	t.Helper()
	r := &wsRecorder{frames: make(chan relayMsg, 100)}
	up := websocket.Upgrader{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c, err := up.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m relayMsg
			if json.Unmarshal(data, &m) == nil {
				r.frames <- m
			}
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *wsRecorder) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(r.srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// nextFrame 收一帧，100ms 内未到则失败
func (r *wsRecorder) nextFrame(t *testing.T) relayMsg {
	t.Helper()
	select {
	case f := <-r.frames:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("等待 relay 帧超时")
		return relayMsg{}
	}
}

// assertNoMoreFrames 断言短时间内不再有帧（防止多发）
func (r *wsRecorder) assertNoMoreFrames(t *testing.T) {
	t.Helper()
	select {
	case f := <-r.frames:
		t.Fatalf("预期无更多帧，却收到 type=%s", f.Type)
	case <-time.After(150 * time.Millisecond):
	}
}

// sseBody 测试用 SSE 流（含一行心跳注释，验证按事件边界分帧且内容完整透传）
const sseBody = ": ping\n\n" +
	"event: reasoning\ndata: {\"delta\":\"你\"}\n\n" +
	"data: {\"delta\":\"好\"}\n\n" +
	"event: done\ndata: {}\n\n"

// newSSEUpstream 模拟本地 claw gateway 的 SSE 端点（分多次写 + Flush，模拟真实流式）
func newSSEUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, part := range []string{": ping\n\n", "event: reasoning\ndata: {\"delta\":\"你\"}\n\n", "data: {\"delta\":\"好\"}\n\n", "event: done\ndata: {}\n\n"} {
			_, _ = io.WriteString(w, part)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRelayTunnel_SSEStreamFraming SSE 响应应拆为 stream_chunk* + stream_end，不再整包 data。
func TestRelayTunnel_SSEStreamFraming(t *testing.T) {
	upstream := newSSEUpstream(t)
	rec := newWSRecorder(t)
	conn := rec.dial(t)

	tun := NewRelayTunnel("", "", "", upstream.URL, zap.NewNop(), nil)
	payload := `{"method":"POST","path":"/v1/agent/execute","body":{"message":"hi"}}`
	tun.handleRequest(context.Background(), conn, 7, payload)

	// 4 个事件（含心跳注释）各一帧 stream_chunk，最后一帧 stream_end
	var chunks strings.Builder
	for i := 0; i < 4; i++ {
		f := rec.nextFrame(t)
		require.Equal(t, "stream_chunk", f.Type, "第 %d 帧应为 stream_chunk", i)
		assert.Equal(t, int64(7), f.Seq)
		chunks.WriteString(f.Payload)
	}
	assert.Equal(t, sseBody, chunks.String(), "全部 chunk 拼接应等于完整 SSE 流")

	end := rec.nextFrame(t)
	require.Equal(t, "stream_end", end.Type)
	assert.Equal(t, int64(7), end.Seq)
	var se relayStreamEnd
	require.NoError(t, json.Unmarshal([]byte(end.Payload), &se))
	assert.Equal(t, http.StatusOK, se.Status)
	assert.Empty(t, se.Error)

	rec.assertNoMoreFrames(t)
}

// TestRelayTunnel_NonSSERemainsSingleFrame 非流式接口保持整包单帧 data（向后兼容）。
func TestRelayTunnel_NonSSERemainsSingleFrame(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"message":"ok","data":{"running":true}}`)
	}))
	t.Cleanup(upstream.Close)
	rec := newWSRecorder(t)
	conn := rec.dial(t)

	tun := NewRelayTunnel("", "", "", upstream.URL, zap.NewNop(), nil)
	payload := `{"method":"GET","path":"/v1/agent/sessions/s1/state"}`
	tun.handleRequest(context.Background(), conn, 9, payload)

	f := rec.nextFrame(t)
	require.Equal(t, "data", f.Type)
	assert.Equal(t, int64(9), f.Seq)
	var resp relayResponse
	require.NoError(t, json.Unmarshal([]byte(f.Payload), &resp))
	assert.Equal(t, http.StatusOK, resp.Status)
	assert.Contains(t, string(resp.Body), `"running":true`)

	rec.assertNoMoreFrames(t)
}

// appSideE2E 模拟 APP 侧 E2E：临时 P-256 密钥对与 claw 静态公钥 ECDH 派生会话密钥。
type appSideE2E struct {
	shared []byte
	pubB64 string
}

func newAppSideE2E(t *testing.T, clawPubB64 string) *appSideE2E {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	clawPubBytes, err := base64.StdEncoding.DecodeString(clawPubB64)
	require.NoError(t, err)
	clawPub, err := ecdh.P256().NewPublicKey(clawPubBytes)
	require.NoError(t, err)
	shared, err := priv.ECDH(clawPub)
	require.NoError(t, err)
	return &appSideE2E{shared: shared, pubB64: base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())}
}

func (a *appSideE2E) gcm(t *testing.T) cipher.AEAD {
	t.Helper()
	block, err := aes.NewCipher(a.shared)
	require.NoError(t, err)
	g, err := cipher.NewGCM(block)
	require.NoError(t, err)
	return g
}

// encrypt 模拟 APP 加密请求帧（eph_pub 为 APP 临时公钥）
func (a *appSideE2E) encrypt(t *testing.T, plaintext string) string {
	t.Helper()
	g := a.gcm(t)
	nonce := make([]byte, g.NonceSize())
	_, err := io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	ep := encryptedPayload{
		EphPub: a.pubB64,
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		CT:     base64.StdEncoding.EncodeToString(g.Seal(nil, nonce, []byte(plaintext), nil)),
	}
	out, err := json.Marshal(ep)
	require.NoError(t, err)
	return string(out)
}

// decrypt 模拟 APP 解密 claw 响应帧（同一会话密钥，nonce/ct 取自载荷）
func (a *appSideE2E) decrypt(t *testing.T, payloadStr string) string {
	t.Helper()
	var ep encryptedPayload
	require.NoError(t, json.Unmarshal([]byte(payloadStr), &ep), "响应载荷应为加密 JSON")
	g := a.gcm(t)
	nonce, err := base64.StdEncoding.DecodeString(ep.Nonce)
	require.NoError(t, err)
	ct, err := base64.StdEncoding.DecodeString(ep.CT)
	require.NoError(t, err)
	plain, err := g.Open(nil, nonce, ct, nil)
	require.NoError(t, err)
	return string(plain)
}

// TestRelayTunnel_SSEStreamEncrypted E2E 开启时 chunk/end 帧同样加密，APP 侧可解密还原完整流。
func TestRelayTunnel_SSEStreamEncrypted(t *testing.T) {
	upstream := newSSEUpstream(t)
	rec := newWSRecorder(t)
	conn := rec.dial(t)

	clawCipher, err := NewE2ECipher()
	require.NoError(t, err)
	app := newAppSideE2E(t, clawCipher.PublicKeyBase64())

	tun := NewRelayTunnel("", "", "", upstream.URL, zap.NewNop(), clawCipher)
	payload := app.encrypt(t, `{"method":"POST","path":"/v1/agent/execute","body":{"message":"hi"}}`)
	tun.handleRequest(context.Background(), conn, 11, payload)

	var chunks strings.Builder
	for i := 0; i < 4; i++ {
		f := rec.nextFrame(t)
		require.Equal(t, "stream_chunk", f.Type, "第 %d 帧应为 stream_chunk", i)
		assert.NotContains(t, f.Payload, "data:", "密文帧不应泄漏明文 SSE 内容")
		chunks.WriteString(app.decrypt(t, f.Payload))
	}
	assert.Equal(t, sseBody, chunks.String())

	end := rec.nextFrame(t)
	require.Equal(t, "stream_end", end.Type)
	var se relayStreamEnd
	require.NoError(t, json.Unmarshal([]byte(app.decrypt(t, end.Payload)), &se))
	assert.Equal(t, http.StatusOK, se.Status)

	rec.assertNoMoreFrames(t)
}

// TestRelayTunnel_StreamCancelOnContext ctx 取消（WS 断开）时在途 SSE 流中断并回 stream_end。
func TestRelayTunnel_StreamCancelOnContext(t *testing.T) {
	// 上游先发一个事件后挂起，直到请求 ctx 取消
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: reasoning\ndata: {\"delta\":\"你\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // 挂起直到 claw 侧取消请求
	}))
	t.Cleanup(upstream.Close)
	rec := newWSRecorder(t)
	conn := rec.dial(t)

	tun := NewRelayTunnel("", "", "", upstream.URL, zap.NewNop(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.handleRequest(ctx, conn, 13, `{"method":"POST","path":"/v1/agent/execute"}`)
		close(done)
	}()

	// 先收到一个 chunk，随后取消 ctx（模拟 WS 断开）
	f := rec.nextFrame(t)
	require.Equal(t, "stream_chunk", f.Type)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 handleRequest 未退出")
	}
	end := rec.nextFrame(t)
	require.Equal(t, "stream_end", end.Type)
	var se relayStreamEnd
	require.NoError(t, json.Unmarshal([]byte(end.Payload), &se))
	assert.NotEmpty(t, se.Error, "中途取消应携带错误信息")
	fmt.Println("stream_end error:", se.Error)
}
