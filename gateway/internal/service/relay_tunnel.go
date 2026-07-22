package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// relayRequest P5.3 明文载荷：app 经 relay 发给 claw 的本地 HTTP 请求封装。
// P5.4 将改为 E2E 加密（payload 为密文，claw tunnel 解密后还原为此结构）。
type relayRequest struct {
	Method  string            `json:"method"`           // HTTP 方法
	Path    string            `json:"path"`             // 本地 claw gateway 路径（如 /chat/completions）
	Headers map[string]string `json:"headers,omitempty"` // 透传请求头
	Body    json.RawMessage   `json:"body,omitempty"`    // 请求体（JSON）
}

// relayResponse P5.3 明文载荷：claw 本地 HTTP 响应封装回 app。
type relayResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// relayMsg 中继帧（与 relay 服务端 relayFrame 对齐）
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
	relayURL    string // relay WS 地址，如 ws://relay.eleball.cn 或 ws://localhost:8092
	deviceID    string
	jwtToken    string
	localBase   string // 本地 claw gateway BaseURL，如 http://localhost:8090/v1
	logger      *zap.Logger
	httpClient  *http.Client

	conn  *websocket.Conn
	mu    sync.Mutex // 保护 conn 写
	stop  chan struct{}
	done  chan struct{}
}

// NewRelayTunnel 创建隧道。relayURL/deviceID/jwtToken 任一为空时 Start 跳过（relay 不可用，仅 LAN）。
func NewRelayTunnel(relayURL, deviceID, jwtToken, localBase string, logger *zap.Logger) *RelayTunnel {
	return &RelayTunnel{
		relayURL:   relayURL,
		deviceID:   deviceID,
		jwtToken:   jwtToken,
		localBase:  localBase,
		logger:     logger,
		httpClient: &http.Client{Timeout: 120 * time.Second},
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
}

// Start 后台启动隧道：连 relay + 读循环 + 心跳 + 断线重连。
func (t *RelayTunnel) Start() {
	if t.relayURL == "" || t.deviceID == "" || t.jwtToken == "" {
		t.logger.Info("relay 隧道未启用（缺 RELAY_URL/DEVICE_ID/JWT，仅 LAN 可用）")
		return
	}
	go t.run()
}

// Stop 停止隧道
func (t *RelayTunnel) Stop() {
	close(t.stop)
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
			go t.handleRequest(conn, msg.Seq, msg.Payload)
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
func (t *RelayTunnel) handleRequest(conn *websocket.Conn, seq int64, payload string) {
	var req relayRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		t.logger.Warn("relay 请求解析失败", zap.Int64("seq", seq), zap.Error(err))
		t.sendResponse(conn, seq, relayResponse{Status: 400, Body: json.RawMessage(`{"error":"请求格式错误"}`)})
		return
	}
	t.logger.Info("relay 处理请求", zap.Int64("seq", seq), zap.String("method", req.Method), zap.String("path", req.Path))

	// 本地 HTTP 请求
	url := t.localBase + req.Path
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(context.Background(), req.Method, url, body)
	if err != nil {
		t.sendResponse(conn, seq, relayResponse{Status: 400, Body: json.RawMessage(`{"error":"` + err.Error() + `"}`)})
		return
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if len(req.Body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		t.sendResponse(conn, seq, relayResponse{Status: 502, Body: json.RawMessage(`{"error":"本地网关不可达: ` + err.Error() + `"}`)})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	t.sendResponse(conn, seq, relayResponse{
		Status: resp.StatusCode,
		Body:   json.RawMessage(respBody),
	})
}

// sendResponse 回写响应帧到 relay（经 relay 转发给 app）
func (t *RelayTunnel) sendResponse(conn *websocket.Conn, seq int64, resp relayResponse) {
	payload, _ := json.Marshal(resp)
	msg := relayMsg{Type: "data", Seq: seq, Payload: string(payload)}
	data, _ := json.Marshal(msg)
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = conn.WriteMessage(websocket.TextMessage, data)
}
