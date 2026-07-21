package llm

import (
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestUpstreamErrorFormat(t *testing.T) {
	err := &UpstreamError{StatusCode: 503, Body: `{"error":{"message":"circuits open"}}`}
	if got := err.Error(); got != `HTTP 503: {"error":{"message":"circuits open"}}` {
		t.Fatalf("Error() 格式不兼容: %s", got)
	}
}

func TestUpstreamStatusCode(t *testing.T) {
	var ue *UpstreamError
	wrapped := fmt.Errorf("模型调用失败: %w", &UpstreamError{StatusCode: 503, Body: "x"})
	if !errors.As(wrapped, &ue) || UpstreamStatusCode(wrapped) != 503 {
		t.Fatal("应能从包装链提取状态码")
	}
	if UpstreamStatusCode(errors.New("other")) != 0 {
		t.Fatal("非 UpstreamError 应返回 0")
	}
}

func TestIsRetryableUpstreamErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"503 熔断", &UpstreamError{StatusCode: 503, Body: "all circuits open"}, true},
		{"500", &UpstreamError{StatusCode: 500, Body: "x"}, true},
		{"429 限流", &UpstreamError{StatusCode: 429, Body: "x"}, true},
		{"400 参数错误", &UpstreamError{StatusCode: 400, Body: "x"}, false},
		{"401 未授权", &UpstreamError{StatusCode: 401, Body: "x"}, false},
		{"包装后的 503", fmt.Errorf("调用失败: %w", &UpstreamError{StatusCode: 503, Body: "x"}), true},
		{"EOF 文本", errors.New("post https://api.x/v1: EOF"), true},
		{"连接重置", errors.New("read: connection reset by peer"), true},
		{"普通错误", errors.New("模型不存在"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := IsRetryableUpstreamErr(c.err); got != c.want {
			t.Errorf("%s: 期望 %v，实际 %v", c.name, c.want, got)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestIsRetryableUpstreamErrNetError(t *testing.T) {
	var err error = timeoutErr{}
	var ne net.Error
	if !errors.As(err, &ne) {
		t.Fatal("timeoutErr 应实现 net.Error")
	}
	if !IsRetryableUpstreamErr(err) {
		t.Fatal("net.Error 应判定为可重试")
	}
}
