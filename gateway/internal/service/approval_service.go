package service

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
)

// approvalTimeout 审批等待超时（兜底，防用户离开后循环永久挂起）。
// 客户端断连会先经 ctx.Done() 触发；此处仅作最终保险。
const approvalTimeout = 10 * time.Minute

// ApprovalRequest 审批请求负载（SSE approval_request 事件 data）。
type ApprovalRequest struct {
	SessionID    string                 `json:"session_id"`
	ToolCallID   string                 `json:"tool_call_id"`
	Tool         string                 `json:"tool"`
	Arguments    map[string]interface{} `json:"arguments"`
	ArgumentsRaw string                 `json:"arguments_raw"` // 原始 JSON 串，前端折叠展示
	RiskLevel    string                 `json:"risk_level"`    // low / medium / high
	Reason       string                 `json:"reason"`        // 为何需要审批
}

// ApprovalDecision 审批决策（POST /agent/approve 投递，或闸内默认生成）。
type ApprovalDecision struct {
	Decision    string `json:"decision"`     // allow / deny
	AlwaysAllow string `json:"always_allow"`  // Tool(spec)，用户选「总是允许」时非空，落 settings 持久化
	Reason      string `json:"reason"`
}

// Approver 交互式工具审批接口（注入 ToolEnv 供工具循环调用）。
// 云端（cmd/server）不装配（env.Approver=nil），审批闸直接放行，行为同现状。
type Approver interface {
	// RequestApproval 发送 approval_request SSE 事件并阻塞等待用户决策。
	// ctx 取消（用户 stop / 断连）或超时返回错误与 deny 决策。
	RequestApproval(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)
}

// approvalRegistry 待审批请求注册表（AgentService 持有，跨 execute 共享）。
// key = sessionID + ":" + toolCallID。循环注册+阻塞，/agent/approve 投递决策。
type approvalRegistry struct {
	mu      sync.Mutex
	pending map[string]chan ApprovalDecision
}

func newApprovalRegistry() *approvalRegistry {
	return &approvalRegistry{pending: make(map[string]chan ApprovalDecision)}
}

// register 注册一个待审批请求，返回决策投递 channel（缓冲 1，防投递方阻塞）。
func (r *approvalRegistry) register(key string) chan ApprovalDecision {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan ApprovalDecision, 1)
	r.pending[key] = ch
	return ch
}

// deliver 投递决策到待审批请求。返回是否命中（未命中=无待审批或已超时取消）。
func (r *approvalRegistry) deliver(key string, dec ApprovalDecision) bool {
	r.mu.Lock()
	ch, ok := r.pending[key]
	if ok {
		delete(r.pending, key)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- dec:
	default:
	}
	return true
}

// cancel 清理待审批请求（超时/取消/决策返回后调用，防内存泄漏）。
func (r *approvalRegistry) cancel(key string) {
	r.mu.Lock()
	delete(r.pending, key)
	r.mu.Unlock()
}

// sseApprover 基于 SSE 的审批实现：per-execute 持有 writer，
// 共享 AgentService 的 approvalRegistry 与 writeEvent。审批 key 用 ApprovalRequest.SessionID（env.SessionID）。
type sseApprover struct {
	svc    *AgentService
	writer io.Writer
}

// RequestApproval 发送 approval_request 事件并阻塞等待。
func (a *sseApprover) RequestApproval(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	key := req.SessionID + ":" + req.ToolCallID
	ch := a.svc.approvals.register(key)
	defer a.svc.approvals.cancel(key)

	// 下发审批请求事件，前端渲染审批卡
	a.svc.writeEvent(a.writer, "approval_request", req)

	timeout := time.NewTimer(approvalTimeout)
	defer timeout.Stop()
	select {
	case dec := <-ch:
		// 决策返回后下发 resolved 事件，前端立即收起卡片（不等工具执行）
		a.svc.writeEvent(a.writer, "approval_resolved", map[string]string{
			"tool_call_id": req.ToolCallID,
			"decision":     dec.Decision,
		})
		return dec, nil
	case <-ctx.Done():
		return ApprovalDecision{Decision: "deny", Reason: "执行已取消"}, ctx.Err()
	case <-timeout.C:
		return ApprovalDecision{Decision: "deny", Reason: "审批超时"}, fmt.Errorf("审批超时（%s）", approvalTimeout)
	}
}

// riskLevelFor 估算工具调用风险等级（供审批卡 UI 着色）。
func riskLevelFor(tool *Tool, name string) string {
	if tool.ReadOnly {
		return "low"
	}
	if name == "Shell" {
		return "high"
	}
	return "medium"
}

// approvalReason 生成人类可读的审批理由。
func approvalReason(tool *Tool, name string, mode model.PermissionMode) string {
	if !tool.ReadOnly {
		if name == "Shell" {
			return "Shell 命令可能修改系统，需确认"
		}
		return "文件写入/修改操作，需确认"
	}
	if mode == model.PermissionModePlan {
		return "plan 模式下此工具受限"
	}
	return "只读工具需确认"
}

// alwaysAllowSpec 构造「总是允许」规则的 Tool(spec) 文本，供用户选择「总是允许」时落库。
// 路径类工具带 path 通配；Shell 带 command 前缀；其余仅工具名。
func alwaysAllowSpec(name string, input map[string]interface{}) string {
	switch name {
	case "Shell":
		cmd, _ := input["command"].(string)
		if cmd == "" {
			return "Shell"
		}
		return fmt.Sprintf("Bash(%s *)", cmd)
	case "WriteFile", "StrReplaceFile", "ReadFile", "Grep", "OCR":
		path, _ := input["path"].(string)
		if path == "" {
			return name
		}
		// 仅取目录前缀作为通配，避免过度宽泛（如 src/a.go -> src/**）
		dir := path
		if idx := lastIndexSep(path); idx >= 0 {
			dir = path[:idx]
			return fmt.Sprintf("%s(%s/**)", name, dir)
		}
		return fmt.Sprintf("%s(%s)", name, path)
	default:
		return name
	}
}

// lastIndexSep 返回路径中最后一个分隔符位置（/ 或 \），无则 -1。
func lastIndexSep(p string) int {
	i := -1
	for j, r := range p {
		if r == '/' || r == '\\' {
			i = j
		}
	}
	return i
}
