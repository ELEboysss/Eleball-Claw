package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/pkg/crypto"
	"github.com/eleball/gateway/pkg/llm"
)

// TestClassifyKeyError 验证按上游错误类型分类
func TestClassifyKeyError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantType string
		coolable bool
	}{
		{"429限流", &llm.UpstreamError{StatusCode: 429, Body: "rate limited"}, "rate_limit", true},
		{"500服务错误", &llm.UpstreamError{StatusCode: 503, Body: "unavailable"}, "server_error", true},
		{"401鉴权失败", &llm.UpstreamError{StatusCode: 401, Body: "unauthorized"}, "auth", false},
		{"403禁止", &llm.UpstreamError{StatusCode: 403, Body: "forbidden"}, "auth", false},
		{"400参数错误", &llm.UpstreamError{StatusCode: 400, Body: "bad request"}, "client_error", false},
		{"网络层", errors.New("connection reset by peer"), "network", true},
		{"未知", errors.New("some other error"), "unknown", false},
		{"nil", nil, "unknown", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotType, base, _ := classifyKeyError(c.err)
			if gotType != c.wantType {
				t.Fatalf("errorType = %q, want %q", gotType, c.wantType)
			}
			if c.coolable && base <= 0 {
				t.Fatalf("可冷却错误 baseCooldown 应 > 0，got %v", base)
			}
			if !c.coolable && base > 0 {
				t.Fatalf("不可冷却错误 baseCooldown 应 = 0，got %v", base)
			}
		})
	}
}

// TestExponentialCooldown 验证指数退避计算与封顶
func TestExponentialCooldown(t *testing.T) {
	base := 1 * time.Second
	max := 5 * time.Minute
	// level=0: base * 1
	if got := exponentialCooldown(base, 0, max); got != 1*time.Second {
		t.Fatalf("level=0 got %v, want 1s", got)
	}
	// level=3: base * 8 = 8s
	if got := exponentialCooldown(base, 3, max); got != 8*time.Second {
		t.Fatalf("level=3 got %v, want 8s", got)
	}
	// 超过 max 封顶（level 被 cap 到 2^6=64s，超过 30s max 则封顶）
	if got := exponentialCooldown(base, 20, 30*time.Second); got != 30*time.Second {
		t.Fatalf("超 max 应封顶，got %v, want 30s", got)
	}
	// 负 level 归零
	if got := exponentialCooldown(base, -1, max); got != 1*time.Second {
		t.Fatalf("负 level 归零，got %v, want 1s", got)
	}
}

// TestSelectKey_SkipsRateLimited 验证冷却中的 Key 被跳过
func TestSelectKey_SkipsRateLimited(t *testing.T) {
	svc := NewNoOpKeyManager()
	// 注入 encrypt 以便 SelectKey 能解密返回的 Key（NoOpKeyManager 默认无 encrypt）
	ke, err := crypto.NewKeyEncryption(strings.Repeat("0123456789abcdef", 4))
	if err != nil {
		t.Fatalf("构造 KeyEncryption 失败: %v", err)
	}
	svc.encrypt = ke
	ct, nonce, _, err := ke.Encrypt("dummy-key")
	if err != nil {
		t.Fatalf("加密 dummy key 失败: %v", err)
	}
	// 手动注入两个 Key：一个冷却中（未来时间），一个可用（冷却已过期）
	future := time.Now().Add(10 * time.Minute)
	past := time.Now().Add(-1 * time.Minute)
	svc.mu.Lock()
	svc.keys["test"] = []*model.ProviderApiKey{
		{ID: "cooled", Provider: "test", IsEnabled: true, RateLimitedUntil: &future, EncryptedKey: ct, Nonce: nonce},
		{ID: "available", Provider: "test", IsEnabled: true, RateLimitedUntil: &past, EncryptedKey: ct, Nonce: nonce},
	}
	svc.mu.Unlock()

	sel, err := svc.SelectKey("test")
	if err != nil {
		t.Fatalf("应选中可用 Key，got err: %v", err)
	}
	if sel.Key.ID != "available" {
		t.Fatalf("应跳过冷却 Key，选中 available，got %s", sel.Key.ID)
	}
}
