package service

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

// testPNG1x1 是一个 1x1 透明 PNG 的 base64（复用自 c7_multimodal_test.go）。
const testPNG1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

// newReadEnv 构造 ReadFile 测试所需的 registry + env（无 SessionRepo，资源登记走 note 分支）。
func newReadEnv(t *testing.T) (*ToolRegistry, *ToolEnv) {
	t.Helper()
	base := t.TempDir()
	sandbox := NewFileSandbox(base, "")
	registry := NewToolRegistryWithDeps(&mockRunner{}, &mockSearchProvider{})
	env := &ToolEnv{UserID: "u1", ConversationID: "c1", SessionID: "s1", Sandbox: sandbox}
	return registry, env
}

// writeFileDirect 直接写字节到沙箱（绕过 WriteFile 工具的 string content 限制，用于图片/PDF/二进制）。
func writeFileDirect(t *testing.T, env *ToolEnv, path string, data []byte) {
	t.Helper()
	abs, err := env.ResolveFilePath(path)
	if err != nil {
		t.Fatalf("ResolveFilePath %s: %v", path, err)
	}
	if err := env.Sandbox.WriteFile(abs, data); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func callRead(t *testing.T, registry *ToolRegistry, env *ToolEnv, input map[string]interface{}) map[string]interface{} {
	t.Helper()
	tool, _ := registry.Get("ReadFile")
	out, err := tool.Func(context.Background(), input, env)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	return out
}

// TestRead_DefaultPreservesContent 验收：小文件整读原样返回（零行为变更）。
func TestRead_DefaultPreservesContent(t *testing.T) {
	registry, env := newReadEnv(t)
	writeFileDirect(t, env, "small.txt", []byte("hello world"))
	out := callRead(t, registry, env, map[string]interface{}{"path": "small.txt"})
	if out["content"] != "hello world" {
		t.Fatalf("content=%v, want hello world", out["content"])
	}
	if out["total_lines"] != 1 {
		t.Fatalf("total_lines=%v, want 1", out["total_lines"])
	}
	if out["more_available"] != false {
		t.Fatalf("小文件 more_available=%v, want false", out["more_available"])
	}
}

// TestRead_Pagination 验收：大文件 offset/limit 分段读 + more_available。
func TestRead_Pagination(t *testing.T) {
	registry, env := newReadEnv(t)
	// 30 行，每行 "n1".."n30"
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		b.WriteString("n")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	writeFileDirect(t, env, "big.txt", []byte(b.String()))

	// limit=5 取前 5 行
	out := callRead(t, registry, env, map[string]interface{}{"path": "big.txt", "limit": float64(5)})
	if out["total_lines"] != 30 {
		t.Fatalf("total_lines=%v, want 30", out["total_lines"])
	}
	if out["returned_lines"] != 5 {
		t.Fatalf("returned_lines=%v, want 5", out["returned_lines"])
	}
	if out["more_available"] != true {
		t.Fatalf("more_available=%v, want true", out["more_available"])
	}
	content := out["content"].(string)
	if lines := strings.Split(strings.TrimRight(content, "\n"), "\n"); len(lines) != 5 {
		t.Fatalf("返回行数=%d, want 5 (content=%q)", len(lines), content)
	}
	if !strings.Contains(content, "n1") || strings.Contains(content, "n6") {
		t.Fatalf("前5行内容异常: %q", content)
	}

	// offset=6 limit=5 取第 6-10 行
	out = callRead(t, registry, env, map[string]interface{}{"path": "big.txt", "offset": float64(6), "limit": float64(5)})
	content = out["content"].(string)
	if !strings.Contains(content, "n6") || !strings.Contains(content, "n10") || strings.Contains(content, "n5") || strings.Contains(content, "n11") {
		t.Fatalf("offset=6 区间内容异常: %q", content)
	}

	// offset 超出末尾 -> 空 content, returned=0, more=false
	out = callRead(t, registry, env, map[string]interface{}{"path": "big.txt", "offset": float64(100)})
	if out["content"] != "" {
		t.Fatalf("offset 越界 content=%v, want 空", out["content"])
	}
	if out["returned_lines"] != 0 {
		t.Fatalf("offset 越界 returned_lines=%v, want 0", out["returned_lines"])
	}
	if out["more_available"] != false {
		t.Fatalf("offset 越界 more_available=%v, want false", out["more_available"])
	}
}

// itoa 简易整数转字符串（避免引入 strconv 仅为此）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var sign string
	if n < 0 {
		sign = "-"
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return sign + string(buf[i:])
}

// TestRead_Image 验收：PNG 检测 -> 宽高/大小描述符，不 dump 字节。
func TestRead_Image(t *testing.T) {
	registry, env := newReadEnv(t)
	pngBytes, _ := base64.StdEncoding.DecodeString(testPNG1x1)
	writeFileDirect(t, env, "pic.png", pngBytes)

	out := callRead(t, registry, env, map[string]interface{}{"path": "pic.png"})
	if out["kind"] != "image" {
		t.Fatalf("kind=%v, want image", out["kind"])
	}
	if out["mime"] != "image/png" {
		t.Fatalf("mime=%v, want image/png", out["mime"])
	}
	if out["width"] != 1 || out["height"] != 1 {
		t.Fatalf("宽高=%vx%v, want 1x1", out["width"], out["height"])
	}
	if out["size"] != len(pngBytes) {
		t.Fatalf("size=%v, want %d", out["size"], len(pngBytes))
	}
	if _, hasContent := out["content"]; hasContent {
		t.Fatalf("图片不应回 content（避免 dump 字节），got %v", out["content"])
	}
}

// TestRead_Jupyter 验收：.ipynb cells 渲染为可读文本。
func TestRead_Jupyter(t *testing.T) {
	registry, env := newReadEnv(t)
	nb := `{
		"cells": [
			{"cell_type": "markdown", "source": "# Title"},
			{"cell_type": "code", "source": ["print(1)\n", "print(2)"], "execution_count": 1}
		],
		"nbformat": 4
	}`
	writeFileDirect(t, env, "note.ipynb", []byte(nb))

	out := callRead(t, registry, env, map[string]interface{}{"path": "note.ipynb"})
	if out["kind"] != "jupyter" {
		t.Fatalf("kind=%v, want jupyter", out["kind"])
	}
	if out["cell_count"] != 2 {
		t.Fatalf("cell_count=%v, want 2", out["cell_count"])
	}
	content := out["content"].(string)
	for _, want := range []string{"[#1 markdown]", "# Title", "[#2 code]", "print(1)", "print(2)"} {
		if !strings.Contains(content, want) {
			t.Fatalf("notebook 渲染缺 %q, content=%q", want, content)
		}
	}
}

// TestRead_PDF 验收：PDF 检测 + 估算页数 + 不 dump 字节。
func TestRead_PDF(t *testing.T) {
	registry, env := newReadEnv(t)
	// 含 1 个 /Type /Page 与 1 个 /Type /Pages 的最小 PDF
	pdf := `%PDF-1.4
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >> endobj
trailer << /Root 1 0 R >>
%%EOF`
	writeFileDirect(t, env, "doc.pdf", []byte(pdf))

	out := callRead(t, registry, env, map[string]interface{}{"path": "doc.pdf"})
	if out["kind"] != "pdf" {
		t.Fatalf("kind=%v, want pdf", out["kind"])
	}
	if out["pages"] != 1 {
		t.Fatalf("pages=%v, want 1（仅 /Type /Page 计数，排除 /Pages）", out["pages"])
	}
	if _, hasContent := out["content"]; hasContent {
		t.Fatalf("PDF 不应回 content（避免 dump 字节），got %v", out["content"])
	}
}

// TestRead_Binary 验收：二进制（含 NUL）拒 dump，仅回描述符。
func TestRead_Binary(t *testing.T) {
	registry, env := newReadEnv(t)
	bin := []byte{0x89, 0x50, 0x4E, 0x00, 0x0D, 0x0A, 0xFF, 0xFE} // 含 NUL，非合法 UTF-8
	writeFileDirect(t, env, "blob.bin", bin)

	out := callRead(t, registry, env, map[string]interface{}{"path": "blob.bin"})
	if out["kind"] != "binary" {
		t.Fatalf("kind=%v, want binary", out["kind"])
	}
	if out["size"] != len(bin) {
		t.Fatalf("size=%v, want %d", out["size"], len(bin))
	}
	if _, hasContent := out["content"]; hasContent {
		t.Fatalf("二进制不应回 content，got %v", out["content"])
	}
}

// TestRead_PaginationStillAllowsEdit 验收：分页读后仍可编辑（快照存原始全文，stale 校验正确）。
func TestRead_PaginationStillAllowsEdit(t *testing.T) {
	registry, env := newReadEnv(t)
	// 3 行文件，limit=2 只读前 2 行
	writeFileDirect(t, env, "edit.txt", []byte("line1 alpha\nline2 beta\nline3 gamma"))
	out := callRead(t, registry, env, map[string]interface{}{"path": "edit.txt", "limit": float64(2)})
	if out["returned_lines"] != 2 {
		t.Fatalf("returned_lines=%v, want 2", out["returned_lines"])
	}
	// 编辑第 1 行的唯一串 -> 应成功（快照为全文，stale 校验通过）
	replaceTool, _ := registry.Get("StrReplaceFile")
	if _, err := replaceTool.Func(context.Background(), map[string]interface{}{
		"path":       "edit.txt",
		"old_string": "alpha",
		"new_string": "ALPHA",
	}, env); err != nil {
		t.Fatalf("分页读后编辑失败（read-before-edit 应通过）: %v", err)
	}
	// 确认改入磁盘
	out = callRead(t, registry, env, map[string]interface{}{"path": "edit.txt"})
	if !strings.Contains(out["content"].(string), "ALPHA") {
		t.Fatalf("编辑未生效: %v", out["content"])
	}
}

// TestRead_LimitClampedToMax 验收：limit 超 maxReadLimit 被截到上限。
func TestRead_LimitClampedToMax(t *testing.T) {
	registry, env := newReadEnv(t)
	writeFileDirect(t, env, "tiny.txt", []byte("only line"))
	// limit=99999 -> 截到 maxReadLimit；文件仅 1 行，整读
	out := callRead(t, registry, env, map[string]interface{}{"path": "tiny.txt", "limit": float64(99999)})
	if out["limit"] != maxReadLimit {
		t.Fatalf("limit=%v, want %d (clamped)", out["limit"], maxReadLimit)
	}
	if out["content"] != "only line" {
		t.Fatalf("content=%v, want only line", out["content"])
	}
}
