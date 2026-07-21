package service

import (
	"github.com/hashicorp/mdns"
	"go.uber.org/zap"
)

// MdnsBroadcaster claw 侧 mDNS 广播器。
//
// P5.2：claw 启动时广播 `_eleball-claw._tcp` 服务，供同局域网 APP 经 NSD 发现
// （AndroidNsdDiscovery 匹配同一服务类型）。APP 发现后直连 claw LAN HTTP。
//
// 失败不阻断 claw 启动（LAN 发现为可选优化；APP 找不到可降级中继/手动端点）。
// 详见 docs/marketing/claw-app-dualtrack-design.md §7.1。
type MdnsBroadcaster struct {
	server *mdns.Server
	logger *zap.Logger
}

// NewMdnsBroadcaster 创建并启动 mDNS 广播。
//
// deviceID 写入 TXT 记录 _device_id，APP 据此精确匹配已配对设备。
// port 为 claw gateway 监听端口（APP 经此端口访问 /v1）。
func NewMdnsBroadcaster(deviceID string, port int, logger *zap.Logger) (*MdnsBroadcaster, error) {
	instance := "eleball-claw"
	if deviceID != "" {
		instance = "eleball-claw-" + deviceID
	}

	// service 类型 "_eleball-claw._tcp"（库内部组装 FQDN；Android NSD 用 "_eleball-claw._tcp."）
	service, err := mdns.NewMDNSService(
		instance,                // instance name
		"_eleball-claw",         // service type（不含 _tcp，库补）
		"",                      // domain -> local.
		"",                      // hostname -> auto
		port,                    // claw gateway port
		nil,                     // IPs -> auto detect
		[]string{"_device_id=" + deviceID, "_version=1.0.0"}, // TXT records
	)
	if err != nil {
		return nil, err
	}

	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return nil, err
	}

	logger.Info("mDNS 广播已启动",
		zap.String("service", "_eleball-claw._tcp"),
		zap.String("device_id", deviceID),
		zap.Int("port", port),
	)
	return &MdnsBroadcaster{server: server, logger: logger}, nil
}

// Stop 停止广播
func (b *MdnsBroadcaster) Stop() {
	if b.server != nil {
		_ = b.server.Shutdown()
	}
}
