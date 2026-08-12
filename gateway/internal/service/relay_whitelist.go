package service

import "strings"

// relay_whitelist.go S09-C2d 安全收敛：relay 透传路径白名单。
//
// relay 通道面向「App 远程访问 claw」，只应放行 App 对话/Agent 所需的 API 面；
// 本地管理面（/v1/claw-console/* 等）与其余接口一律拒绝（403），
// 防止持有效账户 JWT 的远端经中继触达 claw 的本地管理能力。

// relayAllowedPrefixes 前缀放行的路径（命中前缀即放行，含子路径与查询串）。
// 均为 App 对话链路所需（契约见 specs/api-schema.yml）：
//   - /v1/agent/       Agent 工作流（execute/sessions/approve/running 等，SSE 分帧）
//   - /v1/conversations 会话 CRUD 与消息读写
//   - /v1/assistants   助手 CRUD 与 items 设置
//   - /v1/sync/        增量同步
//   - /v1/devices      设备列表（配对辅助；claw 本地亦实现）
var relayAllowedPrefixes = []string{
	"/v1/agent/",
	"/v1/conversations",
	"/v1/assistants",
	"/v1/sync/",
	"/v1/devices",
}

// relayAllowedSuffixes /v1/agents/:id 下仅放行 active/credentials 两个动作
// （秘技激活与凭证配置），agents 集市浏览/购买等其余接口恒走云端不经 relay。
var relayAllowedSuffixes = []string{
	"/active",
	"/credentials",
}

// isRelayAllowedPath 纯函数：判定 relay 透传的请求路径是否放行。
// path 形如 /v1/agent/sessions?page=1（可带查询串，匹配前先剥离）。
// 仅精确白名单放行；/v1/claw-console/* 及其余一律拒绝。
func isRelayAllowedPath(path string) bool {
	// 剥离查询串，只按路径部分匹配
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	// 拒绝路径穿越与非绝对路径（防止 ../ 或相对路径绕过前缀匹配）
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return false
	}
	// 对话补全：精确匹配（含尾斜杠变体）
	if path == "/v1/chat/completions" {
		return true
	}
	for _, prefix := range relayAllowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	// /v1/agents/:id/active|credentials：恰为 4 段（agents/{id}/{action}），
	// 防止 /v1/agents/{id}/active/anything 之类的深层路径混入
	if strings.HasPrefix(path, "/v1/agents/") {
		rest := strings.TrimPrefix(path, "/v1/agents/")
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && parts[0] != "" {
			for _, suffix := range relayAllowedSuffixes {
				if "/"+parts[1] == suffix {
					return true
				}
			}
		}
	}
	return false
}
