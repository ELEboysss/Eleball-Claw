package model

// PermissionMode 权限模式（会话级，控制工具执行的审批策略）。
// 借鉴 claude-code 的五模式体系，claw 一期实现 default/acceptEdits/plan，auto 留二期。
type PermissionMode string

const (
	// PermissionModeDefault 危险动作逐个问：WriteFile/StrReplaceFile/Shell/非只读工具需用户审批。
	PermissionModeDefault PermissionMode = "default"
	// PermissionModeAcceptEdits 文件编辑自动放行，shell/构建仍问。适合信任项目内批量改文件。
	PermissionModeAcceptEdits PermissionMode = "acceptEdits"
	// PermissionModePlan 只读工具集（Read/Grep/Glob/只读 Shell）+ plan 流程；ExitPlanMode 接受后切 acceptEdits。
	PermissionModePlan PermissionMode = "plan"
	// PermissionModeAuto LLM 分类器判定（二期，涉及额外 LLM 调用与延迟，一期不消费）。
	PermissionModeAuto PermissionMode = "auto"
)

// NormalizePermissionMode 归一化前端传入的模式值：空值或非法值回落 default。
func NormalizePermissionMode(s string) PermissionMode {
	switch PermissionMode(s) {
	case PermissionModeDefault, PermissionModeAcceptEdits, PermissionModePlan, PermissionModeAuto:
		return PermissionMode(s)
	default:
		return PermissionModeDefault
	}
}

// IsReadOnlyAllowedMode 判断当前模式是否允许执行只读工具而无需审批。
// plan 模式仅放行只读工具（非只读直接拒绝）；default/acceptEdits 放行只读。
func (m PermissionMode) IsReadOnlyAllowed() bool {
	return m == PermissionModeDefault || m == PermissionModeAcceptEdits || m == PermissionModePlan
}

// PermissionDecision 工具执行决策（审批闸的判定结果）。
type PermissionDecision string

const (
	// PermissionDecisionAllow 放行执行。
	PermissionDecisionAllow PermissionDecision = "allow"
	// PermissionDecisionDeny 拒绝执行（规则 deny 或 plan 模式下非只读）。
	PermissionDecisionDeny PermissionDecision = "deny"
	// PermissionDecisionAsk 需用户审批（发 approval_request SSE 后阻塞）。
	PermissionDecisionAsk PermissionDecision = "ask"
)

// PermissionRule Tool(spec) 规则：用户配置的「总是允许/总是问/拒绝」规则。
// spec 形如 "Bash(git commit *)"、"WriteFile(src/**)"、"ReadFile(*)"、"mcp__server__tool"。
// 解析与通配匹配逻辑在 service.permission_service.go。
type PermissionRule struct {
	Spec     string           `json:"spec"`     // Tool(spec) 文本，如 Bash(git commit *)
	Decision PermissionDecision `json:"decision"` // allow / deny / ask
}
