package service

import (
	"context"
	"fmt"
	"time"

	"github.com/eleball/gateway/pkg/llm"
	"go.uber.org/zap"
)

// defaultUpstreamMaxAttempts 上游可重试错误的默认最大尝试次数（含首次）。
// 与 configs/config.yaml 的 llm.max_retries: 3 对齐；main.go 会通过 SetMaxRetries 注入实际配置。
const defaultUpstreamMaxAttempts = 3

// callWithUpstreamRetry 对可重试的上游错误做有限次重试（退避 600ms → 1.2s → 2.4s …）。
// 仅重试 llm.IsRetryableUpstreamErr 判定的错误（上游 5xx/429/网络层），4xx 等语义错误立即返回；
// ctx 已取消（客户端断连/超时）时不再重试。
func callWithUpstreamRetry(ctx context.Context, maxAttempts int, fn func() error) error {
	if maxAttempts <= 0 {
		maxAttempts = defaultUpstreamMaxAttempts
	}
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = fn()
		if err == nil || !llm.IsRetryableUpstreamErr(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(600*(1<<attempt)) * time.Millisecond):
		}
	}
	return err
}

// friendlyVisualUpstreamError 视觉生成上游错误的用户可读文案：
// 5xx/429 给出状态码提示，超时/网络异常给通用文案，其余保留原文（如参数错误）。
func friendlyVisualUpstreamError(err error) string {
	if code := llm.UpstreamStatusCode(err); code == 429 || code >= 500 {
		return fmt.Sprintf("上游生成服务暂时不可用（HTTP %d），请稍后重试", code)
	}
	if llm.IsRetryableUpstreamErr(err) {
		return "上游生成服务响应超时或网络异常，请稍后重试"
	}
	return err.Error()
}

// friendlyModelCallError 将模型调用错误转换为用户可读文案：
// 上游 5xx/429 隐藏原始报文（聚合商内部错误类名对用户无意义），完整错误记服务端日志；
// 其余错误（4xx、参数问题）保留原文，便于定位。
func friendlyModelCallError(prefix string, err error, logger *zap.Logger) error {
	if code := llm.UpstreamStatusCode(err); code == 429 || code >= 500 {
		if logger != nil {
			logger.Warn("上游模型服务不可用", zap.Int("status", code), zap.Error(err))
		}
		return fmt.Errorf("%s: 上游模型服务暂时不可用（HTTP %d），请稍后重试或切换其他模型", prefix, code)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}
