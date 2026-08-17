package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateE2ECipher_PersistAndReuse(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "sub", "e2e_key")

	// 首启：生成并落盘
	c1, err := LoadOrCreateE2ECipher(keyPath)
	require.NoError(t, err)
	info, err := os.Stat(keyPath)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		// Windows 无 Unix 权限位语义，仅类 Unix 校验 0600
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "私钥文件权限应为 0600")
	}

	// 重启：复用同一密钥对（公钥稳定，APP 已获取的公钥仍可协商）
	c2, err := LoadOrCreateE2ECipher(keyPath)
	require.NoError(t, err)
	assert.Equal(t, c1.PublicKeyBase64(), c2.PublicKeyBase64())

	// 文件损坏：重新生成覆盖（不报错）
	require.NoError(t, os.WriteFile(keyPath, []byte("corrupted"), 0600))
	c3, err := LoadOrCreateE2ECipher(keyPath)
	require.NoError(t, err)
	assert.NotEmpty(t, c3.PublicKeyBase64())
	// 再次加载应与 c3 一致（覆盖成功）
	c4, err := LoadOrCreateE2ECipher(keyPath)
	require.NoError(t, err)
	assert.Equal(t, c3.PublicKeyBase64(), c4.PublicKeyBase64())
}

func TestRegisterCloudDevice(t *testing.T) {
	// 模拟云端 /v1/devices：校验请求字段并回包成功
	var gotBody map[string]string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "message": "success", "data": map[string]string{"id": "rec-1"}})
	}))
	defer srv.Close()

	err := RegisterCloudDevice(context.Background(), srv.URL, "tok-1", "claw-1", "我的电脑", "pubkey-a")
	require.NoError(t, err)
	assert.Equal(t, "Bearer tok-1", gotAuth)
	assert.Equal(t, "claw-1", gotBody["device_id"])
	assert.Equal(t, "claw", gotBody["device_type"])
	assert.Equal(t, "pubkey-a", gotBody["claw_pub_key"])

	// 云端业务拒绝（如 device_id 被他人注册）→ 返回错误（调用方降级告警）
	rejectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 3001, "message": "device_id 已被其他账户注册"})
	}))
	defer rejectSrv.Close()
	assert.Error(t, RegisterCloudDevice(context.Background(), rejectSrv.URL, "tok-1", "claw-1", "", "pub"))

	// 参数缺失 → 直接报错不发请求
	assert.Error(t, RegisterCloudDevice(context.Background(), srv.URL, "", "claw-1", "", "pub"))
	assert.Error(t, RegisterCloudDevice(context.Background(), "", "tok-1", "claw-1", "", "pub"))
	assert.Error(t, RegisterCloudDevice(context.Background(), srv.URL, "tok-1", "", "", "pub"))
}
