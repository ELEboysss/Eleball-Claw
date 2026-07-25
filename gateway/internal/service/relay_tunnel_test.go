package service

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestRelayTunnel_StopWithoutStart 未启动过的隧道 Stop 必须立即返回。
// 回归：claw 优雅关闭时 relay 未启用（缺 RELAY_URL 等配置），Stop 永久阻塞进程退出。
func TestRelayTunnel_StopWithoutStart(t *testing.T) {
	tun := NewRelayTunnel("", "", "", "http://localhost:8090/v1", zap.NewNop(), nil)
	tun.Start() // 缺配置，实际不会启动 run()

	done := make(chan struct{})
	go func() {
		tun.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 在未启动的隧道上阻塞超过 2s")
	}

	// 重复 Stop 也必须是安全的（不 panic、不阻塞）
	done2 := make(chan struct{})
	go func() {
		tun.Stop()
		close(done2)
	}()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("重复 Stop 阻塞超过 2s")
	}
}
