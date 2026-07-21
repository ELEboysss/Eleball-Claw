package middleware

import (
	"testing"
	"time"
)

func TestRateLimiterAllowAndReject(t *testing.T) {
	limiter := NewRateLimiter(3)

	for i := 0; i < 3; i++ {
		allowed, _ := limiter.Allow("ip1")
		if !allowed {
			t.Fatalf("第 %d 个请求应被允许", i+1)
		}
	}

	allowed, remaining := limiter.Allow("ip1")
	if allowed {
		t.Fatal("第 4 个请求应被拒绝")
	}
	if remaining != 0 {
		t.Fatalf("被拒绝时剩余配额应为 0，实际 %d", remaining)
	}
}

func TestRateLimiterWindow(t *testing.T) {
	limiter := NewRateLimiter(1)
	limiter.window = 100 * time.Millisecond

	allowed, _ := limiter.Allow("ip2")
	if !allowed {
		t.Fatal("首个请求应被允许")
	}

	allowed, _ = limiter.Allow("ip2")
	if allowed {
		t.Fatal("同窗口内第 2 个请求应被拒绝")
	}

	time.Sleep(150 * time.Millisecond)

	allowed, _ = limiter.Allow("ip2")
	if !allowed {
		t.Fatal("窗口过期后请求应被允许")
	}
}

func TestRateLimiterIndependentBuckets(t *testing.T) {
	writeLimiter := NewRateLimiter(2)
	readLimiter := NewRateLimiter(5)

	// 写请求耗尽写桶
	for i := 0; i < 2; i++ {
		allowed, _ := writeLimiter.Allow("ip3")
		if !allowed {
			t.Fatalf("第 %d 个写请求应被允许", i+1)
		}
	}
	allowed, _ := writeLimiter.Allow("ip3")
	if allowed {
		t.Fatal("写请求应被拒绝")
	}

	// 读请求使用独立桶，仍允许
	for i := 0; i < 5; i++ {
		allowed, _ := readLimiter.Allow("ip3")
		if !allowed {
			t.Fatalf("第 %d 个读请求应被允许", i+1)
		}
	}
}
