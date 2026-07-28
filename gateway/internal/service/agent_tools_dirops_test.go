package service

import (
	"context"
	"testing"
)

func TestToolRegistry_DirOps(t *testing.T) {
	base := t.TempDir()
	sandbox := NewFileSandbox(base, "")
	runner := &mockRunner{}
	search := &mockSearchProvider{}
	registry := NewToolRegistryWithDeps(runner, search)
	env := &ToolEnv{UserID: "u1", ConversationID: "c1", SessionID: "s1", Sandbox: sandbox}

	// CreateDir（递归建父目录）
	createTool, _ := registry.Get("CreateDir")
	out, err := createTool.Func(context.Background(), map[string]interface{}{"path": "sub/deep"}, env)
	if err != nil {
		t.Fatalf("CreateDir 失败: %v", err)
	}
	if out["created"] != true {
		t.Fatalf("CreateDir 未返回 created=true")
	}

	// WriteFile 到新目录下
	writeTool, _ := registry.Get("WriteFile")
	writeTool.Func(context.Background(), map[string]interface{}{"path": "sub/deep/a.txt", "content": "data"}, env)

	// ListDir 看到 a.txt
	listTool, _ := registry.Get("ListDir")
	out, err = listTool.Func(context.Background(), map[string]interface{}{"path": "sub/deep"}, env)
	if err != nil {
		t.Fatalf("ListDir 失败: %v", err)
	}
	entries, ok := out["entries"].([]FileEntry)
	if !ok {
		t.Fatalf("ListDir 返回 entries 类型错: %T", out["entries"])
	}
	if len(entries) != 1 || entries[0].Name != "a.txt" {
		t.Fatalf("ListDir 期望 [a.txt]，实际 %v", entries)
	}

	// Move 重命名
	moveTool, _ := registry.Get("Move")
	if _, err = moveTool.Func(context.Background(), map[string]interface{}{"src": "sub/deep/a.txt", "dst": "sub/deep/b.txt"}, env); err != nil {
		t.Fatalf("Move 失败: %v", err)
	}

	// DeleteFile
	delTool, _ := registry.Get("DeleteFile")
	if _, err = delTool.Func(context.Background(), map[string]interface{}{"path": "sub/deep/b.txt"}, env); err != nil {
		t.Fatalf("DeleteFile 失败: %v", err)
	}

	// DeleteDir（递归）
	delDirTool, _ := registry.Get("DeleteDir")
	if _, err = delDirTool.Func(context.Background(), map[string]interface{}{"path": "sub"}, env); err != nil {
		t.Fatalf("DeleteDir 失败: %v", err)
	}

	// 删根应拒绝
	if _, err = delDirTool.Func(context.Background(), map[string]interface{}{"path": "."}, env); err == nil {
		t.Fatal(`DeleteDir 应拒绝删根 path="."`)
	}
}

func TestNormalizeToolName(t *testing.T) {
	cases := map[string]string{
		"WriteFile":            "WriteFile",
		"write_file":           "WriteFile",
		"WRITE_FILE":           "WriteFile",
		"read_file":            "ReadFile",
		"str_replace_file":     "StrReplaceFile",
		"functions.write_file": "WriteFile",
		"list_dir":             "ListDir",
		"fetch_url":            "FetchURL",
		"UnknownTool":          "UnknownTool",
	}
	for in, want := range cases {
		if got := normalizeToolName(in); got != want {
			t.Errorf("normalizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}
