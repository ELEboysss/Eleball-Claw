package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func setupCompactor(t *testing.T) (*ContextCompactor, *repository.ChatConversationRepo) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.ChatConversation{}, &model.ChatMessage{}))
	repo := repository.NewChatConversationRepo(db)
	cfg := config.CompactionConfig{
		Enabled:          true,
		ThresholdTokens:  50,
		KeepRecentTokens: 30,
	}
	compactor := NewContextCompactor(repo, cfg, zap.NewNop())
	return compactor, repo
}

func TestContextCompactor_estimateTokens(t *testing.T) {
	c, _ := setupCompactor(t)
	msgs := []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello world"},
	}
	// 每条 +4 开销，content 按字符 / 4
	tok := c.estimateTokens(msgs)
	require.Greater(t, tok, 0)
}

func TestContextCompactor_findCutPoint(t *testing.T) {
	c, _ := setupCompactor(t)
	c.cfg.KeepRecentTokens = 10

	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
	}
	cut, tail, err := c.findCutPoint(msgs)
	require.NoError(t, err)
	require.Greater(t, cut, 0)
	require.Equal(t, msgs[cut:], tail)
	// 保留尾部应包含最后一条 user
	require.Equal(t, "user", tail[len(tail)-1].Role)
}

func TestContextCompactor_findCutPoint_ToolBoundary(t *testing.T) {
	c, _ := setupCompactor(t)
	c.cfg.KeepRecentTokens = 10
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "run"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "tc1", Function: llm.ToolCallFunction{Name: "Shell", Arguments: `{}`}}}},
		{Role: "tool", Content: `{"ok":true}`, ToolCallID: "tc1"},
		{Role: "user", Content: "next"},
	}
	cut, tail, err := c.findCutPoint(msgs)
	require.NoError(t, err)
	require.Greater(t, cut, 0)
	// 切点不能落在 tool 与 assistant tool_call 之间
	if cut == 3 {
		t.Fatalf("切点不能落在 tool 结果与 tool_call 之间: cut=%d", cut)
	}
	// 保留尾部最后一条应为 user
	require.Equal(t, "user", tail[len(tail)-1].Role)
}

func TestContextCompactor_parseSummaryJSON(t *testing.T) {
	raw := `{
	"goal": "test",
	"constraints": ["c1"],
	"progress_done": ["done"],
	"progress_in_progress": [],
	"progress_blocked": [],
	"decisions": ["d1"],
	"next_steps": ["n1"],
	"critical_context": "critical",
	"read_files": ["a.txt"],
	"modified_files": ["b.txt"]
}`
	s, err := parseSummaryJSON(raw)
	require.NoError(t, err)
	assert.Equal(t, "test", s.Goal)
	assert.Equal(t, []string{"c1"}, s.Constraints)

	wrapped := "```json\n" + raw + "\n```"
	s2, err := parseSummaryJSON(wrapped)
	require.NoError(t, err)
	assert.Equal(t, s.Goal, s2.Goal)
}

func TestContextCompactor_fallbackTruncate(t *testing.T) {
	c, _ := setupCompactor(t)
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
	}
	out := c.fallbackTruncate(msgs, 2)
	// 保留系统提示 + 截断提示 + 保留尾部
	require.GreaterOrEqual(t, len(out), 3)
	assert.Equal(t, "system", out[0].Role)
	assert.Equal(t, "system", out[1].Role)
	assert.Contains(t, out[1].Content.(string), "截断")
	assert.Equal(t, "assistant", out[2].Role)
	assert.Equal(t, "user", out[3].Role)
}

func TestContextCompactor_ThresholdNotMet(t *testing.T) {
	c, _ := setupCompactor(t)
	c.cfg.ThresholdTokens = 1000
	client := &mockAgentLLM{responses: []llm.ChatChunk{{Delta: "{}"}}}
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}
	res, newMsgs, newIDs, err := c.CompactDuringLoop(context.Background(), client, "m", "conv", "u", "s", msgs, []string{"", ""}, "", model.PermissionModeDefault, "", "")
	require.NoError(t, err)
	require.Nil(t, res)
	require.Equal(t, msgs, newMsgs)
	require.Equal(t, []string{"", ""}, newIDs)
}

func TestContextCompactor_CompactDuringLoop(t *testing.T) {
	c, repo := setupCompactor(t)
	c.cfg.KeepRecentTokens = 10
	c.cfg.ThresholdTokens = 1
	require.NoError(t, repo.Create(&model.ChatConversation{ID: "conv", UserID: "u", Title: "t"}))

	jsonSummary := `{"goal":"g","constraints":["c"],"progress_done":["d"],"progress_in_progress":[],"progress_blocked":[],"decisions":["dec"],"next_steps":["n"],"critical_context":"ctx","read_files":["a.txt"],"modified_files":["b.txt"]}`
	client := &mockAgentLLM{responses: []llm.ChatChunk{{Delta: jsonSummary}}}

	msgs := []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "what is next"},
	}
	ids := []string{"sys", "u1", "a1", "u2"}
	res, newMsgs, newIDs, err := c.CompactDuringLoop(context.Background(), client, "m", "conv", "u", "s", msgs, ids, "", model.PermissionModeDefault, "", "")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "threshold", res.Reason)
	assert.Equal(t, "a1", res.FirstKeptEntryID)
	assert.Equal(t, "a.txt", res.ReadFiles[0])
	assert.Equal(t, "b.txt", res.ModifiedFiles[0])

	// 新消息列表：system + compaction summary + 保留尾部
	require.GreaterOrEqual(t, len(newMsgs), 2)
	assert.Equal(t, "system", newMsgs[0].Role)
	assert.Equal(t, "system", newMsgs[1].Role)
	assert.Contains(t, newMsgs[1].Content.(string), "对话历史摘要")
	require.Len(t, newIDs, len(newMsgs))
	assert.Equal(t, "sys", newIDs[0])
	assert.Equal(t, "", newIDs[1])
	assert.Equal(t, "a1", newIDs[2])

	// 持久化：数据库应存在 compaction 条目
	all, total, err := repo.ListMessages("conv", 1, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "compaction", all[0].Role)
	assert.Contains(t, all[0].ToolResults, "first_kept_entry_id")
}

func TestContextCompactor_CircuitBreaker_AutoPause(t *testing.T) {
	c, _ := setupCompactor(t)
	c.cfg.KeepRecentTokens = 10
	c.cfg.ThresholdTokens = 1
	client := &mockAgentLLM{responses: []llm.ChatChunk{}} // 触发空摘要解析失败

	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "long content to exceed threshold"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "again"},
	}
	ids := []string{"", "", "", ""}
	for i := 0; i < 3; i++ {
		_, _, _, err := c.CompactDuringLoop(context.Background(), client, "m", "conv", "u", "s", msgs, ids, "", model.PermissionModeDefault, "", "")
		require.Error(t, err)
	}
	// 第四次自动压缩应被熔断
	_, _, _, err := c.CompactDuringLoop(context.Background(), client, "m", "conv", "u", "s", msgs, ids, "", model.PermissionModeDefault, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "自动压缩已熔断")
}

// fixedSummaryLLM 固定返回同一份结构化摘要，用于测试抖动熔断。
type fixedSummaryLLM struct {
	delta string
}

func (f *fixedSummaryLLM) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
	return &llm.ChatChunk{Delta: f.delta}, nil
}

func (f *fixedSummaryLLM) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk, 1)
	ch <- llm.ChatChunk{Delta: f.delta}
	close(ch)
	return ch, nil
}

func TestContextCompactor_ImmediateRehitPause(t *testing.T) {
	c, repo := setupCompactor(t)
	c.cfg.KeepRecentTokens = 1
	require.NoError(t, repo.Create(&model.ChatConversation{ID: "conv", UserID: "u", Title: "t"}))

	// LLM 返回极长摘要，导致压缩后 token 仍 >= threshold，连续 3 次触发抖动熔断
	longSummary := fmt.Sprintf(`{"goal":"%s","constraints":[],"progress_done":[],"progress_in_progress":[],"progress_blocked":[],"decisions":[],"next_steps":[],"critical_context":"%s","read_files":[],"modified_files":[]}`,
		repeatString("goal", 500), repeatString("ctx", 1000))
	client := &fixedSummaryLLM{delta: longSummary}

	big := repeatString("word", 2000)
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: big},
		{Role: "assistant", Content: big},
		{Role: "user", Content: "tail"},
	}
	ids := []string{"", "", "", ""}
	for i := 0; i < 3; i++ {
		res, _, _, err := c.CompactDuringLoop(context.Background(), client, "m", "conv", "u", "s", msgs, ids, "", model.PermissionModeDefault, "", "")
		require.NoError(t, err)
		require.NotNil(t, res)
		require.GreaterOrEqual(t, res.AfterTokens, c.cfg.ThresholdTokens)
		// 后续以上次压缩后的新消息列表继续循环，模拟多次自动触发
		msgs = newMessagesAfterCompact(res, msgs)
	}
	// 第四次自动压缩应因抖动熔断
	_, _, _, err := c.CompactDuringLoop(context.Background(), client, "m", "conv", "u", "s", msgs, ids, "", model.PermissionModeDefault, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "自动压缩已熔断")
}

func newMessagesAfterCompact(res *CompactionResult, old []llm.Message) []llm.Message {
	// 模拟 tool loop 中：保留 system + 新 summary + 尾部，但用 dummy 内容让 token 仍高
	out := []llm.Message{old[0], {Role: "system", Content: res.SummaryMarkdown}}
	// 追加一条新的 user/assistant 对，保证上下文仍超过阈值
	out = append(out, llm.Message{Role: "user", Content: repeatString("tail", 2000)})
	out = append(out, llm.Message{Role: "assistant", Content: repeatString("tail", 2000)})
	return out
}

func TestContextCompactor_extractFileLists(t *testing.T) {
	readSet := make(map[string]struct{})
	modifiedSet := make(map[string]struct{})
	prefix := []llm.Message{
		{Role: "tool", Content: `{"path":"/tmp/read.txt","content":"ok"}`},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{Function: llm.ToolCallFunction{Name: "WriteFile", Arguments: `{"path":"/tmp/write.txt","content":"x"}`}}}},
	}
	extractFileLists(prefix, readSet, modifiedSet)
	assert.Contains(t, mapKeysSorted(readSet), "/tmp/read.txt")
	assert.Contains(t, mapKeysSorted(modifiedSet), "/tmp/write.txt")
}

func repeatString(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

var _ AgentLLMClient = (*mockAgentLLM)(nil)
var _ AgentLLMClient = (*fixedSummaryLLM)(nil)

func TestContextCompactor_PlanModePreservesPlanPath(t *testing.T) {
	c, repo := setupCompactor(t)
	c.cfg.KeepRecentTokens = 10
	c.cfg.ThresholdTokens = 1
	require.NoError(t, repo.Create(&model.ChatConversation{ID: "conv-plan", UserID: "u", Title: "t"}))

	summaryJSON := `{"goal":"plan goal","constraints":[],"progress_done":[],"progress_in_progress":[],"progress_blocked":[],"decisions":[],"next_steps":[],"critical_context":"","read_files":[],"modified_files":[]}`
	client := &mockAgentLLM{responses: []llm.ChatChunk{{Delta: summaryJSON}}}

	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "what is next"},
	}
	ids := []string{"sys", "u1", "a1", "u2"}
	res, _, _, err := c.CompactDuringLoop(context.Background(), client, "m", "conv-plan", "u", "s", msgs, ids, "", model.PermissionModePlan, "/tmp/plan.md", "")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.PlanMode)
	assert.Equal(t, "/tmp/plan.md", res.PlanPath)
	assert.Contains(t, res.SummaryMarkdown, "Plan 模式")

	// 持久化元数据应包含 plan_mode/plan_path
	all, _, err := repo.ListMessages("conv-plan", 1, 100)
	require.NoError(t, err)
	var compactMsg *model.ChatMessage
	for i := range all {
		if all[i].Role == "compaction" {
			compactMsg = &all[i]
			break
		}
	}
	require.NotNil(t, compactMsg)
	assert.Contains(t, compactMsg.ToolResults, `"plan_mode":true`)
	assert.Contains(t, compactMsg.ToolResults, `"plan_path":"/tmp/plan.md"`)
}

func TestContextCompactor_PreCompactHookBlocks(t *testing.T) {
	c, repo := setupCompactor(t)
	c.cfg.KeepRecentTokens = 10
	c.cfg.ThresholdTokens = 1
	require.NoError(t, repo.Create(&model.ChatConversation{ID: "conv-hook", UserID: "u", Title: "t"}))

	// 构造一个总是阻断的 PreCompact hook（exit 2）
	tmpDir := t.TempDir()
	hookPath := filepath.Join(tmpDir, "hooks.json")
	require.NoError(t, os.WriteFile(hookPath, []byte(`[{"event":"pre_compact","type":"command","command":"exit 2","name":"blocker"}]`), 0o644))
	hookSvc, err := NewHookService(hookPath, zap.NewNop())
	require.NoError(t, err)
	c.SetHookService(hookSvc)

	client := &mockAgentLLM{responses: []llm.ChatChunk{{Delta: "{}"}}}
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "what is next"},
	}
	ids := []string{"sys", "u1", "a1", "u2"}
	res, newMsgs, newIDs, err := c.CompactDuringLoop(context.Background(), client, "m", "conv-hook", "u", "s", msgs, ids, "", model.PermissionModeDefault, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PreCompact hook 拒绝压缩")
	assert.Nil(t, res)
	assert.GreaterOrEqual(t, len(newMsgs), 3)
	assert.Equal(t, len(newMsgs), len(newIDs))
}
