package llm

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// UpstreamError 上游模型服务返回的 HTTP 错误。
// Error() 保持 "HTTP <code>: <body>" 格式，与历史字符串匹配逻辑兼容。
type UpstreamError struct {
	StatusCode int
	Body       string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// UpstreamStatusCode 提取上游 HTTP 状态码；非 UpstreamError 返回 0。
func UpstreamStatusCode(err error) int {
	var ue *UpstreamError
	if errors.As(err, &ue) {
		return ue.StatusCode
	}
	return 0
}

// IsRetryableUpstreamErr 判断是否为可安全重试的上游错误：
// 上游 5xx（如聚合商熔断 503）、429 限流、网络层错误（连接重置/EOF/超时）。
// 调用方应自行保证 ctx 未被取消（客户端断连不应重试）。
func IsRetryableUpstreamErr(err error) bool {
	if err == nil {
		return false
	}
	if code := UpstreamStatusCode(err); code != 0 {
		return code == 429 || code >= 500
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "eof") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "timeout")
}
