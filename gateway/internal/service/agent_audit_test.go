package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnifiedDiff AR-06：unified diff 生成
func TestUnifiedDiff(t *testing.T) {
	// 相同内容无 diff
	assert.Equal(t, "", unifiedDiff("abc", "abc"))

	// 新增文件（old 空）全为 +
	diff := unifiedDiff("", "line1\nline2")
	assert.Contains(t, diff, "--- old")
	assert.Contains(t, diff, "+++ new")
	assert.Contains(t, diff, "+line1")
	assert.Contains(t, diff, "+line2")

	// 修改：保留公共行，标记增删
	diff = unifiedDiff("a\nb\nc", "a\nx\nc")
	assert.Contains(t, diff, " a") // 公共行
	assert.Contains(t, diff, "-b") // 删除
	assert.Contains(t, diff, "+x") // 新增
}

// TestAppendWriteAudit AR-06：写审计落 session metadata.json
func TestAppendWriteAudit(t *testing.T) {
	base := t.TempDir()
	fs := NewFileSandbox(base, "")
	dir, err := fs.SessionDir("u1", "s1")
	require.NoError(t, err)

	// 新增文件审计
	require.NoError(t, fs.AppendWriteAudit("u1", "s1", "WriteFile", "a.txt", "", "hello"))
	// 修改文件审计
	require.NoError(t, fs.AppendWriteAudit("u1", "s1", "StrReplaceFile", "a.txt", "hello", "hello world"))

	metaPath := filepath.Join(dir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "write_audits")
	assert.Contains(t, string(data), "WriteFile")
	assert.Contains(t, string(data), "StrReplaceFile")
	assert.Contains(t, string(data), "+hello")       // 新增 diff
	assert.Contains(t, string(data), "-hello")       // 修改删除旧行
	assert.Contains(t, string(data), "+hello world") // 修改新增新行
}

// TestReadSessionAudit AR-08：统一审计视图聚合工具调用记录 + 写审计
func TestReadSessionAudit(t *testing.T) {
	base := t.TempDir()
	fs := NewFileSandbox(base, "")
	_, err := fs.SessionDir("u1", "s1")
	require.NoError(t, err)

	// 写两条文件审计
	require.NoError(t, fs.AppendWriteAudit("u1", "s1", "WriteFile", "a.txt", "", "hello"))
	require.NoError(t, fs.AppendWriteAudit("u1", "s1", "StrReplaceFile", "a.txt", "hello", "hello world"))

	// 调用方传入的工具调用记录（含 AR-08 latency/output_size）
	toolCalls := []ToolCallRecord{
		{Step: 1, Tool: "WriteFile", Arguments: `{"path":"a.txt"}`, LatencyMs: 12, OutputSize: 28},
		{Step: 2, Tool: "StrReplaceFile", Arguments: `{"path":"a.txt"}`, LatencyMs: 8, OutputSize: 35},
	}

	audit, err := fs.ReadSessionAudit("u1", "s1", toolCalls)
	require.NoError(t, err)
	assert.Equal(t, "s1", audit.SessionID)
	require.Len(t, audit.ToolCalls, 2)
	assert.Equal(t, "WriteFile", audit.ToolCalls[0].Tool)
	assert.Equal(t, int64(12), audit.ToolCalls[0].LatencyMs)
	assert.Equal(t, 28, audit.ToolCalls[0].OutputSize)
	require.Len(t, audit.WriteAudits, 2)
	assert.Equal(t, "WriteFile", audit.WriteAudits[0].Tool)
	assert.Equal(t, "StrReplaceFile", audit.WriteAudits[1].Tool)

	// 无 basePath（纯 cwd 场景缺会话沙箱）应返回空写审计但不报错
	fsNoBase := NewFileSandbox("", "")
	audit2, err := fsNoBase.ReadSessionAudit("u1", "s1", toolCalls)
	require.NoError(t, err)
	require.Len(t, audit2.WriteAudits, 0)
	require.Len(t, audit2.ToolCalls, 2) // toolCalls 仍原样返回
}
