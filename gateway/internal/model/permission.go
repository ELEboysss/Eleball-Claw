package model

// PermissionMode 权限模式（会话级，控制工具执行的审批策略）。
// 借鉴 claude-code 的模式体系，claw 实现 default/acceptEdits/plan/auto/strict。
type PermissionMode string

const (
	// PermissionModeDefault 危险动作逐个问：WriteFile/StrReplaceFile/Shell/非只读工具需用户审批，只读自动放行。
	PermissionModeDefault PermissionMode = "default"
	// PermissionModeAcceptEdits 文件编辑自动放行，shell/构建仍问。适合信任项目内批量改文件。
	PermissionModeAcceptEdits PermissionMode = "acceptEdits"
	// PermissionModePlan 只读工具集（Read/Grep/Glob/只读 Shell）+ plan 流程；ExitPlanMode 接受后切 acceptEdits。
	PermissionModePlan PermissionMode = "plan"
	// PermissionModeAuto 全自动：所有工具自动放行（含 Shell/写），仅危险操作黑名单与用户 deny 规则拦截。
	// 适合完全信任的自动化场景；危险命令（rm -rf /、sudo、mkfs 等）仍被 runner 黑名单 + 审批闸前置检查拒绝。
	PermissionModeAuto PermissionMode = "auto"
	// PermissionModeStrict 全确认：所有工具调用均需用户审批（含只读），always-allow 规则仍可放行。
	// 最严格模式，适合敏感项目逐条审查。
	PermissionModeStrict PermissionMode = "strict"
)

// NormalizePermissionMode 归一化前端传入的模式值：空值或非法值回落 default。
func NormalizePermissionMode(s string) PermissionMode {
	switch PermissionMode(s) {
	case PermissionModeDefault, PermissionModeAcceptEdits, PermissionModePlan, PermissionModeAuto, PermissionModeStrict:
		return PermissionMode(s)
	default:
		return PermissionModeDefault
	}
}

// IsReadOnlyAllowedMode 判断当前模式是否允许执行只读工具而无需审批。
// plan 模式仅放行只读工具（非只读直接拒绝）；default/acceptEdits 放行只读；
// auto 经模式默认分支放行（不依赖此方法）；strict 不放行（只读也需确认）。
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
