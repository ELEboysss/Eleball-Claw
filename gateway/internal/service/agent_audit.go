package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteAuditEntry 文件写审计条目（AR-06 P0-2 写审计，落 session metadata.json）。
// 保留修改前后内容与 unified diff，供 claw-console/admin 审计展示。
type WriteAuditEntry struct {
	Timestamp int64  `json:"timestamp"` // unix seconds
	Tool      string `json:"tool"`      // WriteFile / StrReplaceFile
	Path      string `json:"path"`      // 工具传入的相对路径（cwd 相对或会话相对）
	Diff      string `json:"diff"`      // old->new unified diff（空表示新增文件或无变化）
	OldSize   int    `json:"old_size"`
	NewSize   int    `json:"new_size"`
}

// SessionAudit 统一会话审计视图（AR-08，供 claw-console/admin 展示）。
// 聚合工具调用记录（latency/output_size）与文件写审计（unified diff）。
type SessionAudit struct {
	SessionID   string            `json:"session_id"`
	ToolCalls   []ToolCallRecord  `json:"tool_calls"`
	WriteAudits []WriteAuditEntry `json:"write_audits"`
}

// ReadSessionAudit 读取会话审计视图（AR-08 统一审计）。
// toolCalls 由调用方传入（RunResult.Records 持久化后的快照），write_audits 从 metadata.json 读取。
func (fs *FileSandbox) ReadSessionAudit(userID, sessionID string, toolCalls []ToolCallRecord) (SessionAudit, error) {
	audit := SessionAudit{
		SessionID: sessionID,
		ToolCalls: toolCalls,
	}
	if fs.basePath == "" {
		return audit, nil
	}
	dir, err := fs.SessionDir(userID, sessionID)
	if err != nil {
		return audit, err
	}
	metaPath := filepath.Join(dir, "metadata.json")
	if data, err := os.ReadFile(metaPath); err == nil {
		var meta writeAuditMeta
		if jErr := json.Unmarshal(data, &meta); jErr == nil {
			audit.WriteAudits = meta.WriteAudits
		}
	}
	return audit, nil
}

// writeAuditMeta metadata.json 结构
type writeAuditMeta struct {
	WriteAudits []WriteAuditEntry `json:"write_audits"`
}

// AppendWriteAudit 追加文件写审计条目到 session 的 metadata.json（AR-06 写审计）。
// 元数据落在会话私有目录（{basePath}/{userID}/sessions/{sessionID}/metadata.json），
// 与被写文件位置无关（cwd 写场景也在会话目录留痕）。失败仅返回 error，不阻断主流程（调用方可忽略）。
func (fs *FileSandbox) AppendWriteAudit(userID, sessionID, tool, path, oldContent, newContent string) error {
	if fs.basePath == "" {
		return nil // 无会话沙箱（如纯 cwd 场景缺 basePath），跳过审计
	}
	dir, err := fs.SessionDir(userID, sessionID)
	if err != nil {
		return err
	}
	metaPath := filepath.Join(dir, "metadata.json")
	var meta writeAuditMeta
	if data, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(data, &meta) // 解析失败则从空开始
	}
	meta.WriteAudits = append(meta.WriteAudits, WriteAuditEntry{
		Timestamp: time.Now().Unix(),
		Tool:      tool,
		Path:      path,
		Diff:      unifiedDiff(oldContent, newContent),
		OldSize:   len(oldContent),
		NewSize:   len(newContent),
	})
	// 上限保护：审计条目过多时丢弃最早（保留最近 200 条）
	if len(meta.WriteAudits) > 200 {
		meta.WriteAudits = meta.WriteAudits[len(meta.WriteAudits)-200:]
	}
	data, err := json.MarshalIndent(&meta, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化审计元数据失败: %w", err)
	}
	return os.WriteFile(metaPath, data, 0640)
}

// unifiedDiff 生成 old->new 的 unified diff（AR-06 写审计）。
// 基于 LCS 的行级 diff，输出 ---/+++ 头 + 空格(不变)/-（删除）/+（新增）行前缀。
// 大文件（合计 > 512KB）跳过 diff 仅返回占位，避免 LCS DP 内存膨胀。
func unifiedDiff(old, new string) string {
	if old == new {
		return ""
	}
	if len(old)+len(new) > 512*1024 {
		return "(文件过大，diff 已省略)"
	}
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")
	m, n := len(oldLines), len(newLines)
	// LCS DP（从右下向左上填）
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var b strings.Builder
	b.WriteString("--- old\n+++ new\n")
	i, j := 0, 0
	for i < m && j < n {
		if oldLines[i] == newLines[j] {
			b.WriteString(" " + oldLines[i] + "\n")
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			b.WriteString("-" + oldLines[i] + "\n")
			i++
		} else {
			b.WriteString("+" + newLines[j] + "\n")
			j++
		}
	}
	for i < m {
		b.WriteString("-" + oldLines[i] + "\n")
		i++
	}
	for j < n {
		b.WriteString("+" + newLines[j] + "\n")
		j++
	}
	return b.String()
}
