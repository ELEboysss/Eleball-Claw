package util

import "context"

// authTokenKey 请求上下文中的调用方 token 键（未导出，避免外部直接构造）
type authTokenKey struct{}

// WithAuthToken 把调用方的原始 token（不含 "Bearer " 前缀）注入请求上下文。
// 供下游服务在需要代表调用方访问云端时使用（如 claw 云端代理配置的自动凭证）。
func WithAuthToken(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, authTokenKey{}, token)
}

// AuthTokenFrom 取出注入的调用方 token；未注入时返回空串。
func AuthTokenFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	token, _ := ctx.Value(authTokenKey{}).(string)
	return token
}
