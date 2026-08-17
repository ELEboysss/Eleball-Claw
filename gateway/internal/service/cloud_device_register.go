package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// RegisterCloudDevice claw 启动时向云端注册 device_id + E2E 静态公钥（S09-C2b）。
//
// POST {cloudBase}/devices 为幂等 upsert：重复启动/重装不会产生重复记录。
// token 为用户云端 JWT（CLAW_RELAY_TOKEN 同一凭据）；调用方应把错误降级为告警，
// 不阻塞本地服务（云端不可达时 LAN/relay 明文链路仍可用）。
func RegisterCloudDevice(ctx context.Context, cloudBase, token, deviceID, name, clawPubKey string) error {
	if cloudBase == "" {
		return errors.New("云端 API Base 未配置")
	}
	if token == "" {
		return errors.New("未提供云端 token")
	}
	if deviceID == "" {
		return errors.New("device_id 为空")
	}
	if !strings.HasSuffix(cloudBase, "/") {
		cloudBase += "/"
	}
	payload, err := json.Marshal(map[string]string{
		"device_id":    deviceID,
		"name":         name,
		"device_type":  "claw",
		"claw_pub_key": clawPubKey,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cloudBase+"devices", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("云端设备注册请求失败: %w", err)
	}
	defer resp.Body.Close()
	var wrapper struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return fmt.Errorf("云端设备注册响应解析失败（HTTP %d）: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || wrapper.Code != 0 {
		return fmt.Errorf("云端设备注册被拒（HTTP %d, code=%d）: %s", resp.StatusCode, wrapper.Code, wrapper.Message)
	}
	return nil
}
