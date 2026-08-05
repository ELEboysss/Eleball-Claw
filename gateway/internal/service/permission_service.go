package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/eleball/gateway/internal/model"
)

// PermissionService 工具权限决策引擎（C1）。
// 按权限模式 + Tool(spec) 规则 + 工具只读标注判定执行决策（allow/deny/ask）。
// 用户「总是允许 / 总是拒绝」规则持久化到 storePath（claw：{basePath}/permissions.json）。
// 云端（cmd/server）不装配 Approver，此服务仅用于规则解析，不阻塞。
type PermissionService struct {
	mu          sync.RWMutex
	storePath   string
	alwaysRules []model.PermissionRule // 用户持久化规则（allow + deny）
}

// NewPermissionService 创建权限决策引擎，并从 storePath 加载已持久化的用户规则。
// storePath 为空时不持久化（纯内存，测试场景）。
func NewPermissionService(storePath string) *PermissionService {
	ps := &PermissionService{storePath: storePath}
	ps.load()
	return ps
}

// Decide 判定单个工具调用的执行决策。
//
// 匹配顺序（借鉴 claude-code，C2 hook 在此之前执行）：
//  1. 用户 deny 规则 -> deny（最高优先，覆盖只读自动放行）
//  2. plan 模式且非只读 -> deny（plan 是严格只读模式，allow 规则也不破例）
//  3. 只读工具（含 git 读操作）+ 模式允许只读 -> allow
//  4. 用户 allow 规则 -> allow
//  5. 模式默认：acceptEdits 文件编辑放行其余 ask；default/strict ask；auto 全放行；plan 非只读已 deny
func (s *PermissionService) Decide(mode model.PermissionMode, tool *Tool, input map[string]interface{}) model.PermissionDecision {
	toolName := tool.Name

	// E3：只读 shell 命令（git status/diff/log/blame/show）按只读工具对待，自动放行
	// AR-E6：BackgroundShell 同享 shellReadOnlyCommand；且其轮询（action=poll）为只读。
	effectiveReadOnly := tool.ReadOnly
	if isShellLikeTool(toolName) {
		effectiveReadOnly = effectiveReadOnly || shellReadOnlyCommand(input)
	}
	if toolName == "BackgroundShell" {
		effectiveReadOnly = effectiveReadOnly || backgroundShellReadOnly(input)
	}

	// 1. 用户 deny 规则优先（连只读工具也拦）
	if s.hasMatchingRule(model.PermissionDecisionDeny, toolName, input) {
		return model.PermissionDecisionDeny
	}

	// 2. plan 模式非只读直接拒绝
	if mode == model.PermissionModePlan && !effectiveReadOnly {
		return model.PermissionDecisionDeny
	}

	// 3. 只读工具（含 git 读操作）在 default/acceptEdits/plan 下放行（plan 仅放过只读）
	if effectiveReadOnly && mode.IsReadOnlyAllowed() {
		return model.PermissionDecisionAllow
	}

	// 4. 用户 allow 规则
	if s.hasMatchingRule(model.PermissionDecisionAllow, toolName, input) {
		return model.PermissionDecisionAllow
	}

	// 5. 模式默认
	switch mode {
	case model.PermissionModeAcceptEdits:
		if isFileEditTool(toolName) {
			return model.PermissionDecisionAllow
		}
		return model.PermissionDecisionAsk
	case model.PermissionModePlan:
		// 只读已放过、非只读已 deny，理论不到此；兜底 deny
		return model.PermissionDecisionDeny
	case model.PermissionModeAuto:
		// 全自动：所有工具放行（危险操作由 runner 黑名单 + 审批闸危险前置检查兜底）
		return model.PermissionDecisionAllow
	case model.PermissionModeStrict:
		// 全确认：所有工具均需审批（只读未在步骤 3 放行，落此 ask）
		return model.PermissionDecisionAsk
	default: // default
		return model.PermissionDecisionAsk
	}
}

// hasMatchingRule 检查是否存在指定决策的匹配规则（加锁读取）。
func (s *PermissionService) hasMatchingRule(decision model.PermissionDecision, toolName string, input map[string]interface{}) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rule := range s.alwaysRules {
		if rule.Decision == decision && matchRule(rule, toolName, input) {
			return true
		}
	}
	return false
}

// AddAlwaysAllow 新增一条用户规则并持久化。decision 为 allow/deny/ask。
// spec 形如 "Bash(git commit *)"、"WriteFile(src/**)"、"ReadFile(*)"、"Shell"。
func (s *PermissionService) AddAlwaysAllow(spec string, decision model.PermissionDecision) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return
	}
	s.mu.Lock()
	// 去重：同 spec 替换决策
	for i := range s.alwaysRules {
		if s.alwaysRules[i].Spec == spec {
			s.alwaysRules[i].Decision = decision
			s.mu.Unlock()
			s.save()
			return
		}
	}
	s.alwaysRules = append(s.alwaysRules, model.PermissionRule{Spec: spec, Decision: decision})
	s.mu.Unlock()
	s.save()
}

// ListRules 返回用户规则的副本（供 /claw-console 展示与管理）。
func (s *PermissionService) ListRules() []model.PermissionRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.PermissionRule, len(s.alwaysRules))
	copy(out, s.alwaysRules)
	return out
}

// RemoveRule 按 spec 删除一条规则并持久化。
func (s *PermissionService) RemoveRule(spec string) {
	s.mu.Lock()
	for i, r := range s.alwaysRules {
		if r.Spec == spec {
			s.alwaysRules = append(s.alwaysRules[:i], s.alwaysRules[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	s.save()
}

// load 从 storePath 加载规则，失败静默（文件不存在或损坏时从空开始）。
func (s *PermissionService) load() {
	if s.storePath == "" {
		return
	}
	b, err := os.ReadFile(s.storePath)
	if err != nil {
		return
	}
	var rules []model.PermissionRule
	if json.Unmarshal(b, &rules) == nil {
		s.alwaysRules = rules
	}
}

// save 持久化规则到 storePath，失败仅忽略（权限规则非关键数据，不应阻断执行）。
func (s *PermissionService) save() {
	if s.storePath == "" {
		return
	}
	s.mu.RLock()
	rules := make([]model.PermissionRule, len(s.alwaysRules))
	copy(rules, s.alwaysRules)
	s.mu.RUnlock()
	b, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.storePath), 0o750)
	_ = os.WriteFile(s.storePath, b, 0o600)
}

// isFileEditTool 判定是否文件编辑类工具（acceptEdits 模式自动放行）。
func isFileEditTool(name string) bool {
	return name == "WriteFile" || name == "StrReplaceFile"
}

// matchRule 判定规则是否匹配当前工具调用。
func matchRule(rule model.PermissionRule, toolName string, input map[string]interface{}) bool {
	spec := strings.TrimSpace(rule.Spec)
	if spec == "" {
		return false
	}
	ruleTool, pattern := parseToolSpec(spec)
	if !toolNameMatches(ruleTool, toolName) {
		return false
	}
	if pattern == "" {
		return true // 无 pattern，匹配整工具
	}
	value := specMatchValue(toolName, input)
	if value == "" {
		return false
	}
	return matchGlob(pattern, value)
}

// parseToolSpec 解析 "Tool(pattern)" -> (tool, pattern)。
// 无括号时 pattern 为空（匹配整工具）；剥去尾部右括号。
func parseToolSpec(spec string) (tool, pattern string) {
	idx := strings.IndexByte(spec, '(')
	if idx < 0 {
		return spec, ""
	}
	tool = strings.TrimSpace(spec[:idx])
	pattern = strings.TrimSpace(spec[idx+1:])
	pattern = strings.TrimSuffix(pattern, ")")
	return tool, pattern
}

// toolNameMatches 规则工具名匹配（大小写不敏感 + Bash/Shell 别名兼容）。
// claude-code 用 Bash(...)，claw 工具名是 Shell，故 Bash 规则也匹配 Shell。
func toolNameMatches(ruleTool, actual string) bool {
	if strings.EqualFold(ruleTool, actual) {
		return true
	}
	aliases := map[string][]string{
		"bash":            {"shell"},
		"shell":           {"bash"},
		"bashbg":          {"backgroundshell"},
		"backgroundshell": {"bashbg"},
		"read":            {"readfile"},
		"edit":            {"writefile", "strreplacefile"},
	}
	for _, a := range aliases[strings.ToLower(ruleTool)] {
		if strings.EqualFold(a, actual) {
			return true
		}
	}
	return false
}

// specMatchValue 按工具取用于 pattern 匹配的参数值。
// 路径类工具取 path；Shell 取 "command args..."；FetchURL/SearchWeb 取 url/query。
func specMatchValue(toolName string, input map[string]interface{}) string {
	switch toolName {
	case "Shell", "BackgroundShell":
		cmd, _ := input["command"].(string)
		var sb strings.Builder
		sb.WriteString(cmd)
		if rawArgs, ok := input["args"].([]interface{}); ok {
			for _, a := range rawArgs {
				if s, ok := a.(string); ok {
					sb.WriteByte(' ')
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	case "FetchURL":
		v, _ := input["url"].(string)
		return v
	case "SearchWeb":
		v, _ := input["query"].(string)
		return v
	default:
		// WriteFile/StrReplaceFile/ReadFile/Grep/OCR 均用 path
		v, _ := input["path"].(string)
		return v
	}
}

// matchGlob 通配匹配，支持 * / ** / ?。
// ** 跨目录匹配任意字符（含分隔符）；* 匹配除路径分隔符外任意字符；? 单字符。
// 非 glob 元字符按字面量匹配。
func matchGlob(pattern, value string) bool {
	if pattern == "*" || pattern == "**" {
		return true
	}
	var sb strings.Builder
	sb.WriteString("^")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '*':
			// 前看检测 **（跨目录分隔符）
			if i+1 < len(runes) && runes[i+1] == '*' {
				sb.WriteString(".*")
				i++ // 跳过第二个 *
			} else {
				sb.WriteString("[^/]*")
			}
		case '?':
			sb.WriteString("[^/]")
		case '.', '+', '(', ')', '{', '}', '[', ']', '^', '$', '\\', '|':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return strings.EqualFold(pattern, value)
	}
	return re.MatchString(value)
}
