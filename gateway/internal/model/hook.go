package model

import (
	"encoding/json"
	"time"
)

// HookEvent 生命周期钩子事件类型。
// 一期支持：PreToolUse / PostToolUse / Stop / PreCompact。
type HookEvent string

const (
	// HookEventPreToolUse 工具执行前触发。可决定 allow/deny/ask、改写 tool_input、切换模式。
	HookEventPreToolUse HookEvent = "pre_tool_use"
	// HookEventPostToolUse 工具执行后触发。可审计、补充输出。
	HookEventPostToolUse HookEvent = "post_tool_use"
	// HookEventStop 用户/系统发起停止时触发，可拦截继续迭代。
	HookEventStop HookEvent = "stop"
	// HookEventPreCompact 对话级压缩前触发，可注入必须保留的上下文或取消压缩。
	HookEventPreCompact HookEvent = "pre_compact"
)

// JSONDuration 兼容 JSON 字符串（如 "10s"）与纳秒整数的 duration 类型。
type JSONDuration time.Duration

// UnmarshalJSON 支持 "10s" 字符串或整数纳秒。
func (d *JSONDuration) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		dur, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = JSONDuration(dur)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*d = JSONDuration(n)
	return nil
}

// Duration 转换为标准 time.Duration。
func (d JSONDuration) Duration() time.Duration { return time.Duration(d) }

// MarshalJSON 序列化为字符串表示。
func (d JSONDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// HookType 钩子实现类型：command 为外部 shell 脚本；prompt 为 LLM 判定。
type HookType string

const (
	HookTypeCommand HookType = "command"
	HookTypePrompt  HookType = "prompt"
)

// HookConfig 单个钩子配置。多个钩子可匹配同一事件+工具，并行执行。
// 配置示例：
//   {
//     "event": "pre_tool_use",
//     "matcher": "Shell|Bash",
//     "type": "command",
//     "command": "bash ~/.claw/hooks/shell_validator.sh",
//     "timeout": "10s"
//   }
type HookConfig struct {
	// Event 必填，绑定到 HookEvent 之一。
	Event HookEvent `json:"event"`
	// Matcher 工具名正则；空字符串表示匹配所有工具。
	Matcher string `json:"matcher"`
	// Type 钩子类型：command / prompt。
	Type HookType `json:"type"`
	// Command 外部命令（type=command 时必填）。由 shell 执行，stdin 注入 HookInput JSON。
	Command string `json:"command,omitempty"`
	// Prompt 判定提示词（type=prompt 时必填）。由 LLM 根据 HookInput 输出 HookOutput JSON。
	Prompt string `json:"prompt,omitempty"`
	// Timeout 单钩执行超时，默认 10s。字符串如 "10s" / "1m"。
	Timeout JSONDuration `json:"timeout,omitempty"`
	// Name 可选人类可读名称，用于日志与错误定位。
	Name string `json:"name,omitempty"`
}

// DefaultHookTimeout 未指定时的默认钩子超时。
const DefaultHookTimeout = 10 * time.Second

// HookInput 注入到钩子 stdin 的上下文 JSON。
// 各字段按需填充：PreToolUse 时 tool_result 为空；PostToolUse 时 tool_result 为原始/压缩后的结果。
type HookInput struct {
	SessionID       string                 `json:"session_id"`
	ConversationID  string                 `json:"conversation_id,omitempty"`
	ToolName        string                 `json:"tool_name"`
	ToolInput       map[string]interface{} `json:"tool_input"`
	ToolResult      map[string]interface{} `json:"tool_result,omitempty"`
	ToolError       string                 `json:"tool_error,omitempty"`
	Cwd             string                 `json:"cwd,omitempty"`
	PermissionMode  string                 `json:"permission_mode,omitempty"`
	HookEventName   string                 `json:"hook_event_name"`
	TranscriptPath  string                 `json:"transcript_path,omitempty"`
	// Step 工具调用步数，用于日志与调试。
	Step int `json:"step,omitempty"`
}

// HookOutput 钩子 stdout 返回的 JSON 结构（exit 0 时解析）。
// 全部字段可选；空对象表示「放行，不做任何改动」。
type HookOutput struct {
	// Decision 与 PermissionDecision 语义兼容：approve / block / ask。
	Decision           string                 `json:"decision,omitempty"`
	PermissionDecision string                 `json:"permissionDecision,omitempty"`
	// UpdatedInput 非空时覆盖 tool_input 后再执行/再喂模型。
	UpdatedInput map[string]interface{} `json:"updatedInput,omitempty"`
	// SystemMessage 非空时以 system 角色注入当前对话（一次性）。
	SystemMessage string `json:"systemMessage,omitempty"`
	// SetMode 非空时切换会话权限模式（如 "default" / "acceptEdits" / "plan"）。
	SetMode string `json:"setMode,omitempty"`
	// Reason 阻断或询问时附带的原因，回喂模型或展示给用户。
	Reason string `json:"reason,omitempty"`
}

// IsBlockExitCode 返回 exit code 是否为阻断语义（claude-code 契约：2=block）。
func IsBlockExitCode(code int) bool { return code == 2 }

// NormalizeHookEvent 校验并归一化事件名；非法值返回空字符串。
func NormalizeHookEvent(s string) HookEvent {
	switch HookEvent(s) {
	case HookEventPreToolUse, HookEventPostToolUse, HookEventStop, HookEventPreCompact:
		return HookEvent(s)
	default:
		return ""
	}
}
