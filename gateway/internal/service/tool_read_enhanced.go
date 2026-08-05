package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// AR-E7：ReadFile 强化--大文件 offset/limit 分页 + 图片/PDF/Jupyter/二进制识别。
//
// 设计要点：
//   - 文本/notebook 走行级分页（默认 2000 行，上限 5000），回带 total_lines /
//     returned_lines / more_available，防大文件撑爆 LLM 上下文。整文件可容纳时
//     原样返回（零行为变更）。
//   - 图片（PNG/JPG/GIF）用 image.DecodeConfig 取宽高，登记为可下载资源并回带
//     描述符；不内联 base64（工具结果经 compactToolResult 序列化为文本进上下文，
//     内联会产出截断垃圾）。视觉取图走 download_url。
//   - PDF 检测 + 估算页数（/Type /Page 正则，压缩 PDF 可能不准），登记资源。
//     光栅化渲染需专用工具，此处不实现（D1 保持纯 Go 无重依赖）。
//   - Jupyter .ipynb 解析 cells 渲染为可读文本，复用分页。
//   - 二进制（含 NUL / 非 UTF-8）拒绝 dump 字节，仅回描述符，防上下文污染。
//   - read-before-edit 快照始终存原始全文（非分页片段），使分页读后仍可编辑且
//     stale 校验正确；图片/PDF/二进制不可文本编辑，不建快照。

const (
	// defaultReadLimit ReadFile 默认返回行数上限（对齐 Claude Code 默认 2000 行）。
	defaultReadLimit = 2000
	// maxReadLimit ReadFile 单次返回行数硬上限，防模型传超大 limit 撑爆上下文。
	maxReadLimit = 5000
)

// readKind 文件内容分类，决定 ReadFile 的返回形态。
type readKind int

const (
	kindText    readKind = iota // 普通文本：分页返回 content
	kindImage                   // 图片：描述符 + 资源
	kindPDF                     // PDF：描述符 + 资源 + 估算页数
	kindJupyter                 // .ipynb：cells 渲染为文本后分页
	kindBinary                  // 二进制：仅描述符，拒 dump
)

// kindString 返回分类的字符串标识，用于工具结果。
func (k readKind) kindString() string {
	switch k {
	case kindImage:
		return "image"
	case kindPDF:
		return "pdf"
	case kindJupyter:
		return "jupyter"
	case kindBinary:
		return "binary"
	default:
		return "text"
	}
}

// classifyFile 按扩展名 + magic bytes 判定文件分类。
// .ipynb 仅当 JSON 可解析时归 jupyter，否则降级走 text/binary 判定。
func classifyFile(absPath string, data []byte) readKind {
	ext := strings.ToLower(filepath.Ext(absPath))
	switch ext {
	case ".ipynb":
		if _, _, err := renderJupyter(data); err == nil {
			return kindJupyter
		}
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return kindImage
	case ".pdf":
		return kindPDF
	}
	// magic bytes 兜底（无扩展名或扩展名不匹配时）
	if len(data) >= 4 && bytes.HasPrefix(data, []byte("%PDF")) {
		return kindPDF
	}
	if _, _, _, ok := imageInfo(data); ok {
		return kindImage
	}
	if isBinaryData(data) {
		return kindBinary
	}
	return kindText
}

// isBinaryData 判定 data 是否为二进制（非文本）。
// 规则：首 8KB 内出现 NUL 字节，或整体非合法 UTF-8。
func isBinaryData(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	limit := len(data)
	if limit > 8*1024 {
		limit = 8 * 1024
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return !utf8.Valid(data)
}

// imageInfo 解码图片宽高与格式（png/jpeg/gif）。非图片或解码失败返回 ok=false。
// 依赖 image.DecodeConfig（仅读头部，不解码像素），由 blank import 注册格式。
func imageInfo(data []byte) (width, height int, format string, ok bool) {
	cfg, f, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, "", false
	}
	return cfg.Width, cfg.Height, f, true
}

// estimatePDFPages 估算 PDF 页数：统计 /Type /Page（排除 /Pages）出现次数。
// 仅扫首 256KB 限成本；压缩/对象流 PDF 可能低估，结果仅供模型参考。
func estimatePDFPages(data []byte) int {
	scan := data
	if len(scan) > 256*1024 {
		scan = scan[:256*1024]
	}
	return len(pdfPageRe.FindAll(scan, -1))
}

var pdfPageRe = regexp.MustCompile(`(?i)/Type\s*/Page\b`)

// ipynbNotebook / ipynbCell 仅解析 ReadFile 所需字段。source 用 RawMessage
// 以兼容字符串与字符串数组两种形态。
type ipynbNotebook struct {
	Cells    []ipynbCell `json:"cells"`
	Nbformat int         `json:"nbformat"`
}

type ipynbCell struct {
	CellType string          `json:"cell_type"`
	Source   json.RawMessage `json:"source"`
}

// ipynbSourceToString 把 cell.source（string 或 []string）拼为单一字符串。
func ipynbSourceToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return strings.Join(arr, "")
	}
	return ""
}

// renderJupyter 解析 .ipynb，把各 cell 渲染为可读文本（#序号 类型 + 源码）。
// 返回渲染文本与 cell 数量；解析失败返回 err。
func renderJupyter(data []byte) (text string, cellCount int, err error) {
	var nb ipynbNotebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return "", 0, err
	}
	var b strings.Builder
	for i, c := range nb.Cells {
		ct := c.CellType
		if ct == "" {
			ct = "unknown"
		}
		fmt.Fprintf(&b, "[#%d %s]\n", i+1, ct)
		if src := strings.TrimRight(ipynbSourceToString(c.Source), "\n"); src != "" {
			b.WriteString(src)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), len(nb.Cells), nil
}

// splitLinesForRead 按 \n 切行，并去除末尾因最终换行产生的空串
// （"a\n" 计 1 行而非 2 行；中间空行保留）。
func splitLinesForRead(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// paginateText 对文本做行级分页。offset 为 1-indexed 起始行（<=0 视为 1），
// limit 为返回行数上限（<=0 取默认，超 maxReadLimit 截到上限）。
// 整文件可容纳（offset=1 且行数<=limit）时原样返回 content（零行为变更）。
// 返回分页文本、总行数、本次返回行数、是否还有更多。
func paginateText(content string, offset, limit int) (page string, totalLines, returnedLines int, more bool) {
	if offset < 1 {
		offset = 1
	}
	if limit < 1 {
		limit = defaultReadLimit
	}
	if limit > maxReadLimit {
		limit = maxReadLimit
	}
	lines := splitLinesForRead(content)
	totalLines = len(lines)
	if offset == 1 && totalLines <= limit {
		return content, totalLines, totalLines, false
	}
	start := offset - 1
	if start >= totalLines {
		return "", totalLines, 0, false
	}
	end := start + limit
	if end > totalLines {
		end = totalLines
	}
	slice := lines[start:end]
	more = end < totalLines
	return strings.Join(slice, "\n"), totalLines, len(slice), more
}
