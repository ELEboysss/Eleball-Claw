package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter 简易内存限流器（生产环境建议换 Redis）
type RateLimiter struct {
	requests map[string][]time.Time
	mu       sync.Mutex
	limit    int
	window   time.Duration
}

// NewRateLimiter 创建限流器
func NewRateLimiter(requestsPerMinute int) *RateLimiter {
	return &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    requestsPerMinute,
		window:   time.Minute,
	}
}

// Limit 返回限流阈值
func (r *RateLimiter) Limit() int {
	return r.limit
}

// Allow 检查是否允许通过，返回是否允许以及剩余配额
func (r *RateLimiter) Allow(key string) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	// 清理过期记录
	var valid []time.Time
	for _, t := range r.requests[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= r.limit {
		r.requests[key] = valid
		return false, 0
	}

	valid = append(valid, now)
	r.requests[key] = valid
	remaining := r.limit - len(valid)
	return true, remaining
}

// RateLimit 限流中间件
// readLimiter 用于 GET/HEAD/OPTIONS 读请求，writeLimiter 用于写请求
func RateLimit(readLimiter, writeLimiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		isRead := c.Request.Method == http.MethodGet ||
			c.Request.Method == http.MethodHead ||
			c.Request.Method == http.MethodOptions

		limiter := writeLimiter
		if isRead {
			limiter = readLimiter
		}

		allowed, remaining := limiter.Allow(c.ClientIP())

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.Limit()))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

		if !allowed {
			// 提示客户端 30 秒后再试，便于前端做退避
			c.Header("Retry-After", "30")
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    1003,
				"message": "请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
