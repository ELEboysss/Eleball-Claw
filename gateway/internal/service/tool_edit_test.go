package service

import (
	"context"
	"strings"
	"testing"
)

// newEditEnv 构造 StrReplaceFile 测试用的 registry + env（无 SessionRepo，SaveOutput 走 warning 降级，
// 不影响 modified 断言）。所有 AR-E5 行为测试共用。
func newEditEnv(t *testing.T) (*ToolRegistry, *ToolEnv) {
	t.Helper()
	sandbox := NewFileSandbox(t.TempDir(), "")
	registry := NewToolRegistryWithDeps(&mockRunner{}, &mockSearchProvider{})
	env := &ToolEnv{UserID: "u1", ConversationID: "c1", Sandbox: sandbox}
	return registry, env
}

// TestStrReplace_ReadBeforeEdit 验收：未读文件改拒。
// 直接经沙箱写盘（不经 WriteFile 工具）-> readState 为空，模拟"文件存在但本循环未读"。
func TestStrReplace_ReadBeforeEdit(t *testing.T) {
	registry, env := newEditEnv(t)
	abs, err := env.ResolveFilePath("noread.txt")
	if err != nil {
		t.Fatalf("ResolveFilePath: %v", err)
	}
	if err := env.Sandbox.WriteFile(abs, []byte("foo bar baz")); err != nil {
		t.Fatalf("Sandbox.WriteFile: %v", err)
	}

	replaceTool, _ := registry.Get("StrReplaceFile")
	_, err = replaceTool.Func(context.Background(), map[string]interface{}{
		"path":       "noread.txt",
		"old_string": "bar",
		"new_string": "qux",
	}, env)
	if err == nil {
		t.Fatalf("未读取文件却修改成功，期望 read-before-edit 拒绝")
	}
	if !strings.Contains(err.Error(), "read-before-edit") && !strings.Contains(err.Error(), "未读取") {
		t.Fatalf("错误信息未提及 read-before-edit/未读取: %v", err)
	}
}

// TestStrReplace_MultipleMatch 验收：多匹配报错不静默改（未设 replace_all）。
func TestStrReplace_MultipleMatch(t *testing.T) {
	registry, env := newEditEnv(t)
	writeTool, _ := registry.Get("WriteFile")
	writeTool.Func(context.Background(), map[string]interface{}{"path": "dup.txt", "content": "a a a"}, env)

	replaceTool, _ := registry.Get("StrReplaceFile")
	_, err := replaceTool.Func(context.Background(), map[string]interface{}{
		"path":       "dup.txt",
		"old_string": "a",
		"new_string": "b",
	}, env)
	if err == nil {
		t.Fatalf("多处匹配却未报错，期望唯一匹配校验拒绝")
	}
	if !strings.Contains(err.Error(), "多处") {
		t.Fatalf("错误信息未提及多处匹配: %v", err)
	}

	// 确认未静默改动：磁盘内容仍为 "a a a"。
	readTool, _ := registry.Get("ReadFile")
	out, _ := readTool.Func(context.Background(), map[string]interface{}{"path": "dup.txt"}, env)
	if out["content"] != "a a a" {
		t.Fatalf("多匹配报错却改了文件，内容: %v", out["content"])
	}
}

// TestStrReplace_ReplaceAll 验收：replace_all=true 全替换。
func TestStrReplace_ReplaceAll(t *testing.T) {
	registry, env := newEditEnv(t)
	writeTool, _ := registry.Get("WriteFile")
	writeTool.Func(context.Background(), map[string]interface{}{"path": "dup.txt", "content": "a a a"}, env)

	replaceTool, _ := registry.Get("StrReplaceFile")
	out, err := replaceTool.Func(context.Background(), map[string]interface{}{
		"path":        "dup.txt",
		"old_string":  "a",
		"new_string":  "b",
		"replace_all": true,
	}, env)
	if err != nil {
		t.Fatalf("replace_all 替换失败: %v", err)
	}
	if out["modified"] != true {
		t.Fatalf("未返回 modified=true")
	}
	readTool, _ := registry.Get("ReadFile")
	out, _ = readTool.Func(context.Background(), map[string]interface{}{"path": "dup.txt"}, env)
	if out["content"] != "b b b" {
		t.Fatalf("replace_all 全替换内容不对: %v", out["content"])
	}
}

// TestStrReplace_Stale 验收：基于 stale 内容的改被拦截。
// WriteFile 建立快照 "foo"，随后外部改盘为 "CHANGED"（不经工具 -> 快照不变），编辑应被 stale 拒。
func TestStrReplace_Stale(t *testing.T) {
	registry, env := newEditEnv(t)
	writeTool, _ := registry.Get("WriteFile")
	writeTool.Func(context.Background(), map[string]interface{}{"path": "s.txt", "content": "foo"}, env)

	abs, err := env.ResolveFilePath("s.txt")
	if err != nil {
		t.Fatalf("ResolveFilePath: %v", err)
	}
	if err := env.Sandbox.WriteFile(abs, []byte("CHANGED")); err != nil {
		t.Fatalf("Sandbox.WriteFile: %v", err)
	}

	replaceTool, _ := registry.Get("StrReplaceFile")
	_, err = replaceTool.Func(context.Background(), map[string]interface{}{
		"path":       "s.txt",
		"old_string": "foo",
		"new_string": "bar",
	}, env)
	if err == nil {
		t.Fatalf("stale 内容却修改成功，期望 stale 校验拒绝")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("错误信息未提及 stale: %v", err)
	}

	// 确认未被覆盖：磁盘内容仍为 "CHANGED"。
	readTool, _ := registry.Get("ReadFile")
	out, _ := readTool.Func(context.Background(), map[string]interface{}{"path": "s.txt"}, env)
	if out["content"] != "CHANGED" {
		t.Fatalf("stale 拒改却改了文件，内容: %v", out["content"])
	}
}

// TestStrReplace_ReadThenEditOk 验收补充：先 ReadFile 再编辑（唯一匹配）成功，
// 且同循环内连续二次编辑通过快照更新（不误判 stale）。
func TestStrReplace_ReadThenEditOk(t *testing.T) {
	registry, env := newEditEnv(t)
	// 直接写盘后用 ReadFile 建立快照（而非 WriteFile），覆盖纯 read->edit 路径。
	abs, err := env.ResolveFilePath("r.txt")
	if err != nil {
		t.Fatalf("ResolveFilePath: %v", err)
	}
	if err := env.Sandbox.WriteFile(abs, []byte("one two three")); err != nil {
		t.Fatalf("Sandbox.WriteFile: %v", err)
	}
	readTool, _ := registry.Get("ReadFile")
	readTool.Func(context.Background(), map[string]interface{}{"path": "r.txt"}, env)

	replaceTool, _ := registry.Get("StrReplaceFile")
	if _, err := replaceTool.Func(context.Background(), map[string]interface{}{
		"path": "r.txt", "old_string": "two", "new_string": "TWO",
	}, env); err != nil {
		t.Fatalf("首次 read->edit 失败: %v", err)
	}
	// 第二次编辑同文件：快照已更新为 "one TWO three"，应通过 stale 校验。
	if _, err := replaceTool.Func(context.Background(), map[string]interface{}{
		"path": "r.txt", "old_string": "three", "new_string": "THREE",
	}, env); err != nil {
		t.Fatalf("连续二次编辑被误判 stale: %v", err)
	}
	out, _ := readTool.Func(context.Background(), map[string]interface{}{"path": "r.txt"}, env)
	if out["content"] != "one TWO THREE" {
		t.Fatalf("连续编辑结果不对: %v", out["content"])
	}
}
