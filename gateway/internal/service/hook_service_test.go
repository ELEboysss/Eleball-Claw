package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
)

func writeTempHooks(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write hooks.json: %v", err)
	}
	return p
}

func TestHookService_LoadAndMatch(t *testing.T) {
	p := writeTempHooks(t, `[
		{"event":"pre_tool_use","matcher":"Shell|Bash","type":"command","command":"bash a.sh","name":"shell-guard"},
		{"event":"pre_tool_use","type":"command","command":"bash b.sh","name":"all-guard"},
		{"event":"post_tool_use","matcher":"WriteFile","type":"prompt","prompt":"prompt text"},
		{"event":"pre_tool_use","matcher":"[invalid","type":"command","command":"bash c.sh"}
	]`)

	svc, err := NewHookService(p, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer svc.Close()

	hooks := svc.MatchHooks(model.HookEventPreToolUse, "Shell")
	if len(hooks) != 2 {
		t.Fatalf("want 2 matched hooks for Shell, got %d", len(hooks))
	}
	if hooks[0].Name != "shell-guard" || hooks[1].Name != "all-guard" {
		t.Fatalf("order/names unexpected: %v", hooks)
	}

	if got := svc.MatchHooks(model.HookEventPreToolUse, "ReadFile"); len(got) != 1 {
		t.Fatalf("want 1 matched hook for ReadFile, got %d", len(got))
	}

	if got := svc.MatchHooks(model.HookEventPostToolUse, "WriteFile"); len(got) != 1 || got[0].Type != model.HookTypePrompt {
		t.Fatalf("post writefile hook unexpected: %v", got)
	}

	if svc.HasEvent(model.HookEventStop) {
		t.Fatalf("stop should have no hooks")
	}
}

func TestHookService_DefaultTimeout(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"command","command":"bash x.sh"}]`)
	svc, err := NewHookService(p, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer svc.Close()
	got := svc.MatchHooks(model.HookEventPreToolUse, "X")
	if len(got) != 1 || got[0].Timeout.Duration() != model.DefaultHookTimeout {
		t.Fatalf("want timeout %v, got %v", model.DefaultHookTimeout, got)
	}
}

func TestHookService_Reload(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"command","command":"bash old.sh"}]`)
	svc, err := NewHookService(p, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer svc.Close()
	if len(svc.MatchHooks(model.HookEventPreToolUse, "A")) != 1 {
		t.Fatalf("want 1 hook after init")
	}

	if err := os.WriteFile(p, []byte(`[{"event":"pre_tool_use","type":"command","command":"bash new.sh"},{"event":"pre_tool_use","type":"command","command":"bash new2.sh"}]`), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(svc.MatchHooks(model.HookEventPreToolUse, "A")) != 2 {
		t.Fatalf("want 2 hooks after reload, got %d", len(svc.MatchHooks(model.HookEventPreToolUse, "A")))
	}
}

func TestHookService_EmptyAndInvalidEvent(t *testing.T) {
	p := writeTempHooks(t, `[
		{"event":"","type":"command","command":"bash a.sh"},
		{"event":"pre_tool_use","type":"unknown","command":"bash a.sh"},
		{"event":"pre_tool_use","type":"command","command":""},
		{"event":"pre_tool_use","type":"prompt","prompt":""}
	]`)
	svc, err := NewHookService(p, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer svc.Close()
	if svc.HasEvent(model.HookEventPreToolUse) {
		t.Fatalf("all entries should be filtered out")
	}
}

func TestHookService_Context(t *testing.T) {
	ctx := ContextWithHookService(context.Background(), nil)
	if HookServiceFromContext(ctx) != nil {
		t.Fatalf("want nil")
	}
}

func TestHookInputOutputJSON(t *testing.T) {
	in := model.HookInput{SessionID: "s1", ToolName: "Shell", HookEventName: "pre_tool_use", ToolInput: map[string]interface{}{"command": "ls"}}
	b, err := CompileHookInput(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) == 0 {
		t.Fatalf("want non-empty json")
	}
	out, err := ParseHookOutput([]byte(`{"decision":"block","reason":"unsafe"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Decision != "block" || out.Reason != "unsafe" {
		t.Fatalf("unexpected output: %+v", out)
	}
	out2, err := ParseHookOutput(nil)
	if err != nil || out2.Decision != "" {
		t.Fatalf("empty output should be zero")
	}
}

func TestHookService_HotReloadDebounce(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"command","command":"bash v1.sh"}]`)
	svc, err := NewHookService(p, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer svc.Close()

	// 连续快速改写，验证显式 Reload 后最终生效即可；fsnotify 异步，单测不依赖它
	_ = os.WriteFile(p, []byte(`[{"event":"pre_tool_use","type":"command","command":"bash v2.sh"}]`), 0o600)
	_ = os.WriteFile(p, []byte(`[{"event":"pre_tool_use","type":"command","command":"bash v3.sh"}]`), 0o600)
	time.Sleep(50 * time.Millisecond)
	if err := svc.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(svc.MatchHooks(model.HookEventPreToolUse, "A")) != 1 {
		t.Fatalf("want 1 hook after reload")
	}
}

func TestHookDispatch_CommandBlockExit2(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","matcher":"Shell","type":"command","command":"exit 2"}]`)
	svc, _ := NewHookService(p, nil)
	defer svc.Close()

	out, err := svc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if out.Decision != model.PermissionDecisionDeny {
		t.Fatalf("want deny, got %s", out.Decision)
	}
	if out.IsAllow() {
		t.Fatalf("IsAllow should be false")
	}
}

func TestHookDispatch_CommandExit0Allow(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"command","command":"echo '{\"decision\":\"approve\"}'"}]`)
	svc, _ := NewHookService(p, nil)
	defer svc.Close()

	out, err := svc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "WriteFile"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !out.IsAllow() || out.Decision != model.PermissionDecisionAllow {
		t.Fatalf("want allow, got %s", out.Decision)
	}
}

func TestHookDispatch_NonBlockExitNonZero(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"command","command":"echo '{\"decision\":\"block\"}' && exit 1"}]`)
	svc, _ := NewHookService(p, nil)
	defer svc.Close()

	out, err := svc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !out.IsAllow() {
		t.Fatalf("non-2 exit should be non-blocking, got %s", out.Decision)
	}
	if len(out.NonBlockingErrors) == 0 {
		t.Fatalf("want non-blocking error recorded")
	}
}

func TestHookDispatch_MultipleAggregation(t *testing.T) {
	p := writeTempHooks(t, `[
		{"event":"pre_tool_use","type":"command","command":"echo '{\"updatedInput\":{\"path\":\"first\"}}'"},
		{"event":"pre_tool_use","type":"command","command":"exit 2"},
		{"event":"pre_tool_use","type":"command","command":"echo '{\"updatedInput\":{\"path\":\"last\"},\"setMode\":\"acceptEdits\"}'"}
	]`)
	svc, _ := NewHookService(p, nil)
	defer svc.Close()

	out, err := svc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "WriteFile"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if out.Decision != model.PermissionDecisionDeny {
		t.Fatalf("block priority: want deny, got %s", out.Decision)
	}
	if out.UpdatedInput["path"] != "last" {
		t.Fatalf("updatedInput should be last non-empty: %v", out.UpdatedInput)
	}
	if out.SetMode != "acceptEdits" {
		t.Fatalf("want setMode acceptEdits, got %s", out.SetMode)
	}
}

func TestHookDispatch_TimeoutNonBlocking(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"command","command":"while true; do echo x; done","timeout":"50ms"}]`)
	svc, _ := NewHookService(p, nil)
	defer svc.Close()

	out, err := svc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !out.IsAllow() {
		t.Fatalf("timeout should be non-blocking, got %s", out.Decision)
	}
	if len(out.NonBlockingErrors) == 0 || !strings.Contains(out.NonBlockingErrors[0], "timeout") {
		t.Fatalf("want timeout error, got %v", out.NonBlockingErrors)
	}
}

func TestHookDispatch_AskPriority(t *testing.T) {
	p := writeTempHooks(t, `[
		{"event":"pre_tool_use","type":"command","command":"echo '{\"decision\":\"ask\"}'"},
		{"event":"pre_tool_use","type":"command","command":"echo '{\"decision\":\"allow\"}'"}
	]`)
	svc, _ := NewHookService(p, nil)
	defer svc.Close()

	out, err := svc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if out.Decision != model.PermissionDecisionAsk {
		t.Fatalf("ask should beat allow, got %s", out.Decision)
	}
}

func TestHookDispatch_PromptNotImplemented(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"prompt","prompt":"decide"}]`)
	svc, _ := NewHookService(p, nil)
	defer svc.Close()

	out, err := svc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !out.IsAllow() || len(out.NonBlockingErrors) == 0 {
		t.Fatalf("prompt should be non-blocking not-implemented, got %+v", out)
	}
}

// mockApprover 测试用审批器。
type mockApprover struct {
	allow    bool
	requests []ApprovalRequest
}

func (a *mockApprover) RequestApproval(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	a.requests = append(a.requests, req)
	if a.allow {
		return ApprovalDecision{Decision: "allow"}, nil
	}
	return ApprovalDecision{Decision: "deny", Reason: "mock deny"}, nil
}

func (a *mockApprover) RequestPlanReview(ctx context.Context, req PlanReviewRequest) (ApprovalDecision, error) {
	return ApprovalDecision{Decision: "rejected"}, nil
}

func TestRequestToolApproval_HookDeny(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","matcher":"Shell","type":"command","command":"exit 2"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()

	ps := NewPermissionService("")
	approver := &mockApprover{allow: true}
	env := &ToolEnv{SessionID: "s1", PermissionMode: model.PermissionModeDefault, PermissionSvc: ps, Approver: approver, HookSvc: hookSvc}
	tool := permTool("Shell", false)
	input := map[string]interface{}{"command": "ls"}
	record := ToolCallRecord{Arguments: "{\"command\":\"ls\"}"}

	if requestToolApproval(context.Background(), env, tool, "Shell", input, "tc1", &record) {
		t.Fatalf("hook deny should return false")
	}
	if record.Error == "" {
		t.Fatalf("want record error")
	}
	if len(approver.requests) != 0 {
		t.Fatalf("approver should not be called when hook denies")
	}
}

func TestRequestToolApproval_HookAllowBypassesAsk(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","matcher":"Shell","type":"command","command":"echo '{\"decision\":\"allow\"}'"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()

	ps := NewPermissionService("")
	approver := &mockApprover{allow: false}
	env := &ToolEnv{SessionID: "s1", PermissionMode: model.PermissionModeDefault, PermissionSvc: ps, Approver: approver, HookSvc: hookSvc}
	tool := permTool("Shell", false)
	input := map[string]interface{}{"command": "ls"}
	record := ToolCallRecord{Arguments: "{\"command\":\"ls\"}"}

	if !requestToolApproval(context.Background(), env, tool, "Shell", input, "tc1", &record) {
		t.Fatalf("hook allow should bypass ask and return true")
	}
	if len(approver.requests) != 0 {
		t.Fatalf("approver should not be called when hook allows")
	}
}

func TestRequestToolApproval_HookUpdatedInput(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","matcher":"WriteFile","type":"command","command":"echo '{\"updatedInput\":{\"path\":\"safe.txt\"}}'"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()

	ps := NewPermissionService("")
	approver := &mockApprover{allow: true}
	env := &ToolEnv{SessionID: "s1", PermissionMode: model.PermissionModeDefault, PermissionSvc: ps, Approver: approver, HookSvc: hookSvc}
	tool := permTool("WriteFile", false)
	input := map[string]interface{}{"path": "danger.txt"}
	record := ToolCallRecord{Arguments: "{\"path\":\"danger.txt\"}"}

	if !requestToolApproval(context.Background(), env, tool, "WriteFile", input, "tc1", &record) {
		t.Fatalf("ask with approval should return true")
	}
	if len(approver.requests) != 1 {
		t.Fatalf("want 1 approval request, got %d", len(approver.requests))
	}
	if approver.requests[0].Arguments["path"] != "safe.txt" {
		t.Fatalf("hook updated input not applied to approval: %v", approver.requests[0].Arguments)
	}
	if input["path"] != "safe.txt" {
		t.Fatalf("input not updated in place: %v", input)
	}
}

func TestRequestToolApproval_HookSetModePlan(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"command","command":"echo '{\"setMode\":\"plan\"}'"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()

	ps := NewPermissionService("")
	approver := &mockApprover{allow: true}
	env := &ToolEnv{SessionID: "s1", PermissionMode: model.PermissionModeDefault, PermissionSvc: ps, Approver: approver, HookSvc: hookSvc}
	tool := permTool("Shell", false)
	input := map[string]interface{}{"command": "ls"}
	record := ToolCallRecord{Arguments: "{\"command\":\"ls\"}"}

	if requestToolApproval(context.Background(), env, tool, "Shell", input, "tc1", &record) {
		t.Fatalf("plan mode should deny non-readonly tool")
	}
	if env.PermissionMode != model.PermissionModePlan {
		t.Fatalf("want permission mode switched to plan, got %s", env.PermissionMode)
	}
}

func TestRunPostToolUseHooks(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "post_hook.json")
	cmd := fmt.Sprintf("cat > '%s'", marker)
	p := writeTempHooks(t, fmt.Sprintf(`[{"event":"post_tool_use","matcher":"ReadFile","type":"command","command":%q}]`, cmd))
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()

	env := &ToolEnv{SessionID: "s1", HookSvc: hookSvc}
	record := ToolCallRecord{Step: 1, Output: map[string]interface{}{"content": "hello"}, Arguments: "{\"path\":\"a.txt\"}"}
	input := map[string]interface{}{"path": "a.txt"}

	runPostToolUseHooks(context.Background(), env, "ReadFile", input, &record)

	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("post hook did not write marker: %v", err)
	}
	if !strings.Contains(string(b), `"tool_name":"ReadFile"`) {
		t.Fatalf("marker missing expected content: %s", string(b))
	}
	if !strings.Contains(string(b), `"tool_result"`) {
		t.Fatalf("marker missing tool_result: %s", string(b))
	}
}

func TestRunPostToolUseHooks_Error(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "post_hook_err.txt")
	cmd := fmt.Sprintf("cat > '%s'", marker)
	p := writeTempHooks(t, fmt.Sprintf(`[{"event":"post_tool_use","type":"command","command":%q}]`, cmd))
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()

	env := &ToolEnv{SessionID: "s1", HookSvc: hookSvc}
	record := ToolCallRecord{Step: 2, Error: "not found"}
	input := map[string]interface{}{"path": "a.txt"}

	runPostToolUseHooks(context.Background(), env, "ReadFile", input, &record)

	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("post hook did not write marker: %v", err)
	}
	if !strings.Contains(string(b), `"tool_error":"not found"`) {
		t.Fatalf("marker missing tool_error: %s", string(b))
	}
}

func TestAgentService_RunStopHook(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"stop","type":"command","command":"exit 2"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()

	svc := &AgentService{}
	env := &ToolEnv{SessionID: "s1", HookSvc: hookSvc}
	out, err := svc.runStopHook(context.Background(), env, &RunResult{})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if out.Decision != model.PermissionDecisionDeny {
		t.Fatalf("want stop hook deny, got %s", out.Decision)
	}
}

// mockLLMClient 已定义在 chat_proxy_service_test.go，此处直接复用。

func TestHookDispatch_PromptBlock(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"prompt","prompt":"decide whether to block"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()
	hookSvc.SetLLMClient(&mockLLMClient{response: `{"decision":"block","reason":"unsafe"}`})

	out, err := hookSvc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if out.Decision != model.PermissionDecisionDeny || out.BlockReason != "unsafe" {
		t.Fatalf("want prompt block with reason, got %+v", out)
	}
}

func TestHookDispatch_PromptAllow(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"prompt","prompt":"decide"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()
	hookSvc.SetLLMClient(&mockLLMClient{response: `{"decision":"allow"}`})

	out, err := hookSvc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !out.IsAllow() || out.Decision != model.PermissionDecisionAllow {
		t.Fatalf("want prompt allow, got %+v", out)
	}
}

func TestHookDispatch_PromptNoLLMClient(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"prompt","prompt":"decide"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()

	out, err := hookSvc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !out.IsAllow() || len(out.NonBlockingErrors) == 0 {
		t.Fatalf("want non-blocking no-LLM error, got %+v", out)
	}
}

func TestHookDispatch_PromptTimeout(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"prompt","prompt":"decide","timeout":"50ms"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()
	hookSvc.SetLLMClient(&mockLLMClient{err: context.DeadlineExceeded})

	out, err := hookSvc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !out.IsAllow() || len(out.NonBlockingErrors) == 0 || !strings.Contains(out.NonBlockingErrors[0], "timeout") {
		t.Fatalf("want timeout non-blocking, got %+v", out)
	}
}

func TestHookDispatch_PromptUpdatedInput(t *testing.T) {
	p := writeTempHooks(t, `[{"event":"pre_tool_use","type":"prompt","prompt":"decide"}]`)
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()
	hookSvc.SetLLMClient(&mockLLMClient{response: `{"updatedInput":{"command":"ls"},"setMode":"acceptEdits"}`})

	out, err := hookSvc.Dispatch(context.Background(), model.HookEventPreToolUse, model.HookInput{ToolName: "Shell"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if out.UpdatedInput["command"] != "ls" || out.SetMode != "acceptEdits" {
		t.Fatalf("want updatedInput and setMode, got %+v", out)
	}
}

func TestDispatchPreCompact(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "precompact.json")
	cmd := fmt.Sprintf("cat > '%s'", marker)
	p := writeTempHooks(t, fmt.Sprintf(`[{"event":"pre_compact","type":"command","command":%q}]`, cmd))
	hookSvc, _ := NewHookService(p, nil)
	defer hookSvc.Close()

	out, err := hookSvc.DispatchPreCompact(context.Background(), "s1", "c1", "/tmp", "summary text")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !out.IsAllow() {
		t.Fatalf("precompact hook should be non-blocking by default, got %s", out.Decision)
	}

	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("precompact hook did not write marker: %v", err)
	}
	if !strings.Contains(string(b), `"hook_event_name":"pre_compact"`) || !strings.Contains(string(b), `"summary":"summary text"`) {
		t.Fatalf("marker missing expected content: %s", string(b))
	}
}
