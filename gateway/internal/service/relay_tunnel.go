package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// relayRequest P5.3 明文载荷：app 经 relay 发给 claw 的本地 HTTP 请求封装。
// P5.4 将改为 E2E 加密（payload 为密文，claw tunnel 解密后还原为此结构）。
type relayRequest struct {
	Method  string            `json:"method"`            // HTTP 方法
	Path    string            `json:"path"`              // 本地 claw gateway 路径（如 /chat/completions）
	Headers map[string]string `json:"headers,omitempty"` // 透传请求头
	Body    json.RawMessage   `json:"body,omitempty"`    // 请求体（JSON）
}

// relayResponse P5.3 明文载荷：claw 本地 HTTP 响应封装回 app（非流式接口整包）。
type relayResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// relayStreamEnd 流式结束帧（stream_end）的明文载荷：携带最终 HTTP 状态与中途错误。
// 流式接口（响应 Content-Type: text/event-stream）的响应不再走整包 data 帧，
// 而是 0..N 个 stream_chunk（payload 为 SSE 原始文本块）+ 1 个 stream_end。
type relayStreamEnd struct {
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

// relayMsg 中继帧（与 relay 服务端 relayFrame、shared RelayFrame 三方对齐）。
// Type: data（整包请求/响应）/ stream_chunk（SSE 流式块）/ stream_end（流式结束）/ ping / bye。
// 一个 seq 对应一个请求；非流式接口仍回单帧 data（向后兼容）。
type relayMsg struct {
	Type    string `json:"type"`
	Seq     int64  `json:"seq,omitempty"`
	Payload string `json:"payload,omitempty"`
}

// RelayTunnel claw 出站隧道：连云端 relay，把 app 经中继转发的请求帧还原为本地 HTTP 请求。
//
// P5.3 明文：payload 为 relayRequest/relayResponse JSON。
// P5.4：payload 改 E2E 密文，tunnel 解密后处理。
//
// 详见 docs/marketing/claw-app-dualtrack-design.md §7.2。
type RelayTunnel struct {
	relayURL  string // relay WS 地址，如 ws://relay.eleball.cn 或 ws://localhost:8092
	deviceID  string
	jwtToken  string
	localBase string // 本地 claw gateway BaseURL，如 http://localhost:8090/v1
	logger    *zap.Logger
	// 统一请求客户端：无整体超时（http.Client.Timeout 覆盖整个 body 读取，对 SSE 长流致命），
	// 仅响应头 30s 兜底本地网关假死；非流式响应在读取阶段单独施加 120s 超时保持原语义。
	streamClient *http.Client
	cipher       *E2ECipher // P5.4 E2E 加密器（nil 时明文，向后兼容）

	conn *websocket.Conn
	mu   sync.Mutex // 保护 conn 写
	stop chan struct{}
	done chan struct{}
	// started 标记隧道是否真的启动过（Start 在缺配置时直接跳过）；
	// Stop 仅对已启动的隧道等待 done，否则立即返回，避免优雅关闭时永久阻塞。
	started atomic.Bool
	// stopOnce 防止重复 close(stop) panic
	stopOnce sync.Once
}

// NewRelayTunnel 创建隧道。relayURL/deviceID/jwtToken 任一为空时 Start 跳过（relay 不可用，仅 LAN）。
// cipher 非 nil 时启用 E2E 加密（P5.4）；nil 时明文（P5.3 兼容）。
func NewRelayTunnel(relayURL, deviceID, jwtToken, localBase string, logger *zap.Logger, cipher *E2ECipher) *RelayTunnel {
	// 统一客户端：SSE 长执行不适用 120s 整体超时（go http.Client.Timeout 覆盖整个 body 读取），
	// 仅保留响应头超时兜底本地网关假死；流期间靠请求 ctx（WS 断开即取消）与 WS 心跳清理。
	streamTransport := http.DefaultTransport.(*http.Transport).Clone()
	streamTransport.ResponseHeaderTimeout = 30 * time.Second
	return &RelayTunnel{
		relayURL:     relayURL,
		deviceID:     deviceID,
		jwtToken:     jwtToken,
		localBase:    localBase,
		logger:       logger,
		streamClient: &http.Client{Transport: streamTransport},
		cipher:       cipher,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
}

// Start 后台启动隧道：连 relay + 读循环 + 心跳 + 断线重连。
// S09-C2d 安全收敛：配置齐全但 cipher 为 nil（E2E 加密器初始化失败）时拒绝启动，
// 不再静默回落明文——relay 链路的请求面暴露到公网，明文透传不可接受。
func (t *RelayTunnel) Start() {
	if t.relayURL == "" || t.deviceID == "" || t.jwtToken == "" {
		t.logger.Info("relay 隧道未启用（缺 RELAY_URL/DEVICE_ID/JWT，仅 LAN 可用）")
		return
	}
	if t.cipher == nil {
		t.logger.Warn("relay 已配置但 E2E 加密器不可用，安全收敛：relay 不启用（拒绝明文回落）")
		return
	}
	t.started.Store(true)
	go t.run()
}

// Stop 停止隧道（未启动过 / 重复调用均安全）
func (t *RelayTunnel) Stop() {
	if !t.started.Load() {
		return
	}
	t.stopOnce.Do(func() { close(t.stop) })
	t.mu.Lock()
	if t.conn != nil {
		_ = t.conn.Close()
	}
	t.mu.Unlock()
	<-t.done
}

func (t *RelayTunnel) run() {
	defer close(t.done)
	for {
		select {
		case <-t.stop:
			return
		default:
		}
		if err := t.connectAndServe(); err != nil {
			t.logger.Warn("relay 隧道断开，10s 后重连", zap.Error(err))
		}
		select {
		case <-t.stop:
			return
		case <-time.After(10 * time.Second):
		}
	}
}

// connectAndServe 连接 relay，注册 claw，读循环处理 app 请求帧。
func (t *RelayTunnel) connectAndServe() error {
	// URL: ws://relay/ws?role=claw&device_id=X&token=JWT
	url := fmt.Sprintf("%s/ws?role=claw&device_id=%s&token=%s",
		t.relayURL, t.deviceID, t.jwtToken)
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("连接 relay 失败: %w", err)
	}
	t.mu.Lock()
	t.conn = conn
	t.mu.Unlock()
	t.logger.Info("relay 隧道已连接", zap.String("device_id", t.deviceID))

	// 连接级 ctx：本次 WS 会话内所有请求（尤其 SSE 长流）随连接断开统一取消，
	// 避免 relay 重连后遗留僵尸流持续占用本地网关资源。
	connCtx, cancelConn := context.WithCancel(context.Background())
	defer cancelConn()

	// 心跳 goroutine
	go t.heartbeat(conn)

	// 读循环
	defer conn.Close()
	for {
		select {
		case <-t.stop:
			return nil
		default:
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("relay 读失败: %w", err)
		}
		var msg relayMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue // 非法帧忽略
		}
		if msg.Type == "data" {
			go t.handleRequest(connCtx, conn, msg.Seq, msg.Payload)
		}
	}
}

// heartbeat 定期发 ping 保活（relay ReadMessage 自动回 pong 续 deadline）
func (t *RelayTunnel) heartbeat(conn *websocket.Conn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-ticker.C:
			_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
		}
	}
}

// handleRequest 把 app 请求帧还原为本地 claw gateway HTTP 请求，响应帧回写 relay。
// P5.4：cipher 非 nil 时 payload 为 E2E 密文，先解密还原为 relayRequest；响应加密回传。
// S09-C2c：响应为 SSE（Content-Type: text/event-stream）时改流式分帧回写
// （stream_chunk* + stream_end），非 SSE 保持整包单帧 data（向后兼容）。
// ctx 为连接级上下文：WS 断开即取消在途请求/流。
func (t *RelayTunnel) handleRequest(ctx context.Context, conn *websocket.Conn, seq int64, payload string) {
	// P5.4 E2E 解密：cipher 非 nil 时 payload 是 encryptedPayload JSON
	plainPayload := payload
	var ephPubForResp string
	if t.cipher != nil {
		decrypted, err := t.cipher.Decrypt(payload)
		if err != nil {
			t.logger.Warn("E2E 解密失败", zap.Int64("seq", seq), zap.Error(err))
			t.sendResponse(conn, seq, relayResponse{Status: 400, Body: json.RawMessage(`{"error":"解密失败"}`)}, "")
			return
		}
		plainPayload = string(decrypted)
		// 从加密载荷提取 eph_pub 供响应加密复用同一会话密钥
		var ep struct {
			EphPub string `json:"eph_pub"`
		}
		if json.Unmarshal([]byte(payload), &ep) == nil {
			ephPubForResp = ep.EphPub
		}
	}

	var req relayRequest
	if err := json.Unmarshal([]byte(plainPayload), &req); err != nil {
		t.logger.Warn("relay 请求解析失败", zap.Int64("seq", seq), zap.Error(err))
		t.sendResponse(conn, seq, relayResponse{Status: 400, Body: json.RawMessage(`{"error":"请求格式错误"}`)}, ephPubForResp)
		return
	}
	// S09-C2d 安全收敛：路径白名单——仅放行 App 对话链路所需 API 面，
	// /v1/claw-console/* 等本地管理面与其余接口一律 403，不经 relay 触达本地网关。
	if !isRelayAllowedPath(req.Path) {
		t.logger.Warn("relay 路径不在白名单，拒绝", zap.Int64("seq", seq), zap.String("path", req.Path))
		t.sendResponse(conn, seq, relayResponse{Status: 403, Body: json.RawMessage(`{"error":"路径不允许经 relay 访问"}`)}, ephPubForResp)
		return
	}
	t.logger.Info("relay 处理请求", zap.Int64("seq", seq), zap.String("method", req.Method), zap.String("path", req.Path), zap.Bool("e2e", t.cipher != nil))

	// 本地 HTTP 请求（ctx 随 WS 连接断开取消）
	url := t.localBase + req.Path
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, url, body)
	if err != nil {
		t.sendResponse(conn, seq, relayResponse{Status: 400, Body: json.RawMessage(`{"error":"` + err.Error() + `"}`)}, ephPubForResp)
		return
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if len(req.Body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// 统一用无整体超时的 streamClient 发起（http.Client.Timeout 会覆盖整个 body 读取，
	// 对 SSE 长流致命）；非流式响应在读取阶段单独施加 120s 超时保持原语义。
	resp, err := t.streamClient.Do(httpReq)
	if err != nil {
		t.sendResponse(conn, seq, relayResponse{Status: 502, Body: json.RawMessage(`{"error":"本地网关不可达: ` + err.Error() + `"}`)}, ephPubForResp)
		return
	}
	defer resp.Body.Close()

	// SSE 响应：逐事件分帧回写（stream_chunk* + stream_end），不做整体 deadline
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.streamSSEResponse(conn, seq, resp, ephPubForResp)
		return
	}

	// 非流式：整包读取（保留原 120s 超时语义，超时关 Body 中断读取）
	respBody, err := readBodyWithTimeout(resp.Body, 120*time.Second)
	if err != nil {
		t.sendResponse(conn, seq, relayResponse{Status: 504, Body: json.RawMessage(`{"error":"` + err.Error() + `"}`)}, ephPubForResp)
		return
	}
	t.sendResponse(conn, seq, relayResponse{
		Status: resp.StatusCode,
		Body:   json.RawMessage(respBody),
	}, ephPubForResp)
}

// readBodyWithTimeout 整包读取非流式响应体；超时即关闭 Body 中断读取（goroutine 随 ReadAll 报错退出）。
func readBodyWithTimeout(body io.ReadCloser, timeout time.Duration) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(body)
		ch <- result{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-time.After(timeout):
		_ = body.Close()
		return nil, fmt.Errorf("读取本地响应超时(%s)", timeout)
	}
}

// streamSSEResponse 流式回写 SSE 响应：按事件边界（空行）聚合为一个 stream_chunk 帧，
// 流结束（EOF/读错/ctx 取消）发 stream_end 携带最终状态与错误。
// 逐行读保证 UTF-8 字符不被切断（换行字节不可能出现在多字节序列内部），
// app 侧 SseParser 本身容忍任意块边界，分帧策略可调。
func (t *RelayTunnel) streamSSEResponse(conn *websocket.Conn, seq int64, resp *http.Response, ephPubForResp string) {
	reader := bufio.NewReader(resp.Body)
	var chunk strings.Builder
	flush := func() {
		if chunk.Len() > 0 {
			t.sendFrame(conn, "stream_chunk", seq, t.encryptIfNeeded([]byte(chunk.String()), ephPubForResp))
			chunk.Reset()
		}
	}
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			chunk.WriteString(line)
			if strings.TrimRight(line, "\r\n") == "" {
				flush() // SSE 事件以空行分隔：凑满一个事件即发一帧
			}
		}
		if err != nil {
			flush() // 尾部无空行收尾的残留事件也发出
			if err == io.EOF {
				t.sendStreamEnd(conn, seq, resp.StatusCode, "", ephPubForResp)
			} else {
				t.logger.Warn("SSE 流读取中断", zap.Int64("seq", seq), zap.Error(err))
				t.sendStreamEnd(conn, seq, resp.StatusCode, err.Error(), ephPubForResp)
			}
			return
		}
	}
}

// sendStreamEnd 回写流式结束帧（payload 为 relayStreamEnd JSON，E2E 时加密）。
func (t *RelayTunnel) sendStreamEnd(conn *websocket.Conn, seq int64, status int, errMsg string, ephPubForResp string) {
	payload, _ := json.Marshal(relayStreamEnd{Status: status, Error: errMsg})
	t.sendFrame(conn, "stream_end", seq, t.encryptIfNeeded(payload, ephPubForResp))
}

// encryptIfNeeded E2E 开启且拿到对端 eph_pub 时加密 payload，否则原文返回（向后兼容明文模式）。
func (t *RelayTunnel) encryptIfNeeded(plain []byte, ephPubForResp string) string {
	if t.cipher != nil && ephPubForResp != "" {
		if encrypted, err := t.cipher.EncryptResponse(ephPubForResp, plain); err == nil {
			return encrypted
		} else {
			t.logger.Warn("E2E 加密响应失败，回退明文", zap.Error(err))
		}
	}
	return string(plain)
}

// sendResponse 回写响应帧到 relay（经 relay 转发给 app）。
// P5.4：cipher 非 nil 且 ephPubForResp 非空时，响应加密为 encryptedPayload。
func (t *RelayTunnel) sendResponse(conn *websocket.Conn, seq int64, resp relayResponse, ephPubForResp string) {
	payload, _ := json.Marshal(resp)
	t.sendFrame(conn, "data", seq, t.encryptIfNeeded(payload, ephPubForResp))
}

// sendFrame 统一的帧出口：加写锁单点写 WS（data / stream_chunk / stream_end 共用）。
func (t *RelayTunnel) sendFrame(conn *websocket.Conn, frameType string, seq int64, payloadStr string) {
	msg := relayMsg{Type: frameType, Seq: seq, Payload: payloadStr}
	data, _ := json.Marshal(msg)
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = conn.WriteMessage(websocket.TextMessage, data)
}
