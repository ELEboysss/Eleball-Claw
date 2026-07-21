package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/eleball/gateway/pkg/llm"
)

// TestCallWithUpstreamRetrySuccessAfterRetry 前两次 503，第三次成功
func TestCallWithUpstreamRetrySuccessAfterRetry(t *testing.T) {
	calls := 0
	err := callWithUpstreamRetry(context.Background(), 3, func() error {
		calls++
		if calls < 3 {
			return &llm.UpstreamError{StatusCode: 503, Body: "circuits open"}
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("应在第三次成功: calls=%d err=%v", calls, err)
	}
}

// TestCallWithUpstreamRetryNonRetryable 4xx 语义错误立即返回不重试
func TestCallWithUpstreamRetryNonRetryable(t *testing.T) {
	calls := 0
	err := callWithUpstreamRetry(context.Background(), 3, func() error {
		calls++
		return &llm.UpstreamError{StatusCode: 400, Body: "bad request"}
	})
	if err == nil || calls != 1 {
		t.Fatalf("4xx 不应重试: calls=%d err=%v", calls, err)
	}
}

// TestCallWithUpstreamRetryExhausted 重试耗尽返回最后一次错误
func TestCallWithUpstreamRetryExhausted(t *testing.T) {
	calls := 0
	err := callWithUpstreamRetry(context.Background(), 2, func() error {
		calls++
		return &llm.UpstreamError{StatusCode: 503, Body: "circuits open"}
	})
	if err == nil || calls != 2 {
		t.Fatalf("应按 maxAttempts 停止: calls=%d err=%v", calls, err)
	}
	if llm.UpstreamStatusCode(err) != 503 {
		t.Fatalf("应保留原始上游错误: %v", err)
	}
}

// TestCallWithUpstreamRetryContextCancelled ctx 取消时不再重试
func TestCallWithUpstreamRetryContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	start := time.Now()
	_ = callWithUpstreamRetry(ctx, 5, func() error {
		calls++
		cancel() // 首次失败后取消
		return &llm.UpstreamError{StatusCode: 503, Body: "x"}
	})
	if calls != 1 {
		t.Fatalf("ctx 取消后不应重试: calls=%d", calls)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("ctx 取消后应立即返回")
	}
}

// TestCallWithUpstreamRetryZeroAttempts maxAttempts<=0 使用默认次数
func TestCallWithUpstreamRetryZeroAttempts(t *testing.T) {
	calls := 0
	_ = callWithUpstreamRetry(context.Background(), 0, func() error {
		calls++
		return &llm.UpstreamError{StatusCode: 503, Body: "x"}
	})
	if calls != defaultUpstreamMaxAttempts {
		t.Fatalf("应使用默认 %d 次: calls=%d", defaultUpstreamMaxAttempts, calls)
	}
}

// TestFriendlyVisualUpstreamError 视觉生成上游错误文案
func TestFriendlyVisualUpstreamError(t *testing.T) {
	// 5xx → 带状态码提示
	if got := friendlyVisualUpstreamError(&llm.UpstreamError{StatusCode: 503, Body: "circuits"}); got != "上游生成服务暂时不可用（HTTP 503），请稍后重试" {
		t.Fatalf("503 文案不对: %s", got)
	}
	// 超时/网络错误 → 通用超时文案（真实复现 Agnes Image 的 Client.Timeout 错误链：
	// fmt.Errorf("Agnes Image 请求失败: %w", &url.Error{Err: context.DeadlineExceeded})）
	timeoutErr := fmt.Errorf("Agnes Image 请求失败: %w", &url.Error{
		Op:  "Post",
		URL: "https://apihub.agnes-ai.com/v1/images/generations",
		Err: context.DeadlineExceeded,
	})
	if got := friendlyVisualUpstreamError(timeoutErr); got != "上游生成服务响应超时或网络异常，请稍后重试" {
		t.Fatalf("超时文案不对: %s", got)
	}
	// 4xx 语义错误 → 保留原文
	if got := friendlyVisualUpstreamError(&llm.UpstreamError{StatusCode: 400, Body: "bad prompt"}); got != "HTTP 400: bad prompt" {
		t.Fatalf("4xx 应保留原文: %s", got)
	}
}

func TestFriendlyModelCallError(t *testing.T) {
	err503 := friendlyModelCallError("模型调用失败", &llm.UpstreamError{StatusCode: 503, Body: `{"error":{"message":"internal class name"}}`}, nil)
	if err503 == nil || !strings.Contains(err503.Error(), "上游模型服务暂时不可用（HTTP 503）") {
		t.Fatalf("503 文案不对: %v", err503)
	}
	if strings.Contains(err503.Error(), "internal class name") {
		t.Fatalf("不应向用户暴露原始报文: %v", err503)
	}
	if !strings.HasPrefix(err503.Error(), "模型调用失败") {
		t.Fatalf("应保留前缀: %v", err503)
	}

	err400 := friendlyModelCallError("模型调用失败", &llm.UpstreamError{StatusCode: 400, Body: "model not found"}, nil)
	if err400 == nil || !strings.Contains(err400.Error(), "model not found") {
		t.Fatalf("4xx 应保留原文: %v", err400)
	}

	other := friendlyModelCallError("模型调用失败", errors.New("普通错误"), nil)
	if other == nil || !strings.Contains(other.Error(), "普通错误") {
		t.Fatalf("非上游错误应原样包装: %v", other)
	}
}
