package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 本文件实现 Grep 工具的增强（ripgrep 优先 + 丰富参数）与独立 Glob 工具。
// 设计（T-PR-E4）：
//   - Grep 优先调系统 rg（findExecutable("rg")），rg 不可用或出错时回退纯 Go 实现，
//     保证大仓搜索"快且全"。安全：path 先经 env.ResolveFilePath 沙箱校验，rg 只拿到已校验的绝对路径。
//   - 新增参数：output_mode(content|files_with_matches|count)、glob、type、
//     before_context/after_context/context、multiline、head_limit。
//   - Glob：path + pattern，支持 ** 跨目录递归，按 mtime 倒序（最近修改在前）返回。

// grepOpts 聚合 Grep 工具的搜索参数
type grepOpts struct {
	pattern    string
	absPath    string
	isDir      bool
	recursive  bool
	ignoreCase bool
	invert     bool
	typeName   string
	glob       string
	before     int
	after      int
	context    int
	multiline  bool
	outputMode string // content | files_with_matches | count
	headLimit  int
}

// fileTypeExtensions 常见语言扩展名表，供纯 Go 回退的 type 过滤使用。
// rg 路径直接 -t <type>，由 rg 自带类型表解析，覆盖更全。
var fileTypeExtensions = map[string][]string{
	"go": {".go"}, "js": {".js", ".mjs", ".cjs"}, "ts": {".ts", ".tsx"},
	"jsx": {".jsx"}, "py": {".py"}, "java": {".java"}, "kt": {".kt", ".kts"},
	"rs": {".rs"}, "c": {".c", ".h"}, "cpp": {".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx"},
	"cs": {".cs"}, "rb": {".rb"}, "php": {".php"}, "swift": {".swift"},
	"sh": {".sh", ".bash"}, "yaml": {".yaml", ".yml"}, "yml": {".yml", ".yaml"},
	"json": {".json"}, "toml": {".toml"}, "xml": {".xml"},
	"html": {".html", ".htm"}, "css": {".css"}, "scss": {".scss", ".sass"},
	"md": {".md", ".markdown"}, "sql": {".sql"}, "proto": {".proto"},
	"dart": {".dart"}, "lua": {".lua"}, "r": {".r"}, "vim": {".vim"},
}

// typeExts 返回某语言类型对应的扩展名切片；未知类型返回 nil。
func typeExts(name string) []string {
	return fileTypeExtensions[strings.ToLower(name)]
}

// hasTypeExt 判断文件路径是否匹配某类型的扩展名
func hasTypeExt(path string, exts []string) bool {
	if len(exts) == 0 {
		return true
	}
	lp := strings.ToLower(path)
	for _, e := range exts {
		if strings.HasSuffix(lp, e) {
			return true
		}
	}
	return false
}

// globMatchPath 匹配文件相对路径与 glob 模式，支持 ** 跨目录。
// **/ 前缀也应匹配根级文件（**/*.go 匹配 a.go 与 sub/a.go）。
func globMatchPath(pattern, relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	pattern = filepath.ToSlash(pattern)
	variants := []string{pattern}
	if strings.HasPrefix(pattern, "**/") {
		variants = append(variants, strings.TrimPrefix(pattern, "**/"))
	}
	for _, v := range variants {
		if matchGlob(v, relPath) {
			return true
		}
	}
	return false
}

// ripgrepSearch 用系统 rg 执行搜索。返回 (stdout, handled, err)。
// handled=false 表示 rg 不可用或应回退纯 Go（调用方据此降级）。
func ripgrepSearch(ctx context.Context, opts grepOpts) (string, bool, error) {
	rg := findExecutable("rg")
	if rg == "" {
		return "", false, nil
	}
	var args []string
	args = append(args, "--line-number")
	if opts.ignoreCase {
		args = append(args, "-i")
	}
	if opts.invert {
		args = append(args, "-v")
	}
	if opts.multiline {
		args = append(args, "-U")
	}
	if opts.typeName != "" {
		args = append(args, "-t", opts.typeName)
	}
	if opts.glob != "" {
		args = append(args, "-g", opts.glob)
	}
	if opts.context > 0 {
		args = append(args, "-C", strconv.Itoa(opts.context))
	} else {
		if opts.before > 0 {
			args = append(args, "-B", strconv.Itoa(opts.before))
		}
		if opts.after > 0 {
			args = append(args, "-A", strconv.Itoa(opts.after))
		}
	}
	switch opts.outputMode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c")
	}
	// -e 显式标记 pattern，避免 pattern 以 - 开头时被误认为选项
	args = append(args, "-e", opts.pattern)
	args = append(args, opts.absPath)

	cmd := exec.CommandContext(ctx, rg, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		// rg 退出码 1 = 无匹配（grep 惯例，非错误）；退出码 2 = 真错误。
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", true, nil
		}
		// rg 出错（坏正则/非法参数等）：回退纯 Go，由纯 Go 给出一致错误
		return "", false, nil
	}
	return stdout.String(), true, nil
}

// splitLines 按行切分 rg 输出，去掉尾部空行
func splitLines(s string) []string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// applyHeadLimit 对切片施加 head_limit，返回裁剪后的切片与是否截断
func applyHeadLimit(lines []string, headLimit int) ([]string, bool) {
	if headLimit > 0 && len(lines) > headLimit {
		return lines[:headLimit], true
	}
	return lines, false
}

// grepGoResult 纯 Go 搜索结果
type grepGoResult struct {
	contentLines []string // 已格式化内容行
	files        []string // files_with_matches
	counts       []string // "path:N"
	total        int
	truncated    bool
}

// grepSearchGo 纯 Go 搜索（rg 不可用/出错时回退）。支持 glob/type 过滤、上下文、multiline、head_limit。
func grepSearchGo(ctx context.Context, opts grepOpts) (grepGoResult, error) {
	var result grepGoResult

	// 编译正则：合并 ignoreCase(i) 与 multiline(s) 内联标志
	prefix := ""
	if opts.ignoreCase {
		prefix += "i"
	}
	if opts.multiline {
		prefix += "s"
	}
	pat := opts.pattern
	if prefix != "" {
		pat = "(?" + prefix + ")" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return result, fmt.Errorf("正则表达式非法: %w", err)
	}

	exts := typeExts(opts.typeName)

	// 收集待搜索文件
	var files []string
	if !opts.isDir {
		files = append(files, opts.absPath)
	} else {
		walkErr := filepath.WalkDir(opts.absPath, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				if p != opts.absPath && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir // 跳过 .git 等隐藏目录
				}
				return nil
			}
			files = append(files, p)
			return nil
		})
		if walkErr != nil {
			return result, walkErr
		}
		sort.Strings(files)
	}

	singleFile := !opts.isDir
	needContext := opts.context > 0 || opts.before > 0 || opts.after > 0
	maxLines := grepMaxMatches
	if opts.headLimit > 0 && opts.headLimit < maxLines {
		maxLines = opts.headLimit
	}

	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		// type/glob 过滤（相对搜索根的路径）
		rel, _ := filepath.Rel(opts.absPath, f)
		if opts.isDir {
			if !hasTypeExt(f, exts) {
				continue
			}
			if opts.glob != "" && !globMatchPath(opts.glob, rel) {
				continue
			}
		}
		if opts.outputMode == "content" {
			lines, mlines, err := readLines(f)
			if err != nil {
				continue
			}
			if opts.multiline {
				content := strings.Join(lines, "\n")
				idxs := re.FindAllStringIndex(content, -1)
				for _, ix := range idxs {
					if result.total >= maxLines {
						result.truncated = true
						break
					}
					lineNo := 1 + strings.Count(content[:ix[0]], "\n")
					txt := firstLine(content[ix[0]:])
					result.contentLines = append(result.contentLines, formatGrepLine(singleFile, f, lineNo, txt, true))
					result.total++
				}
				continue
			}
			matched := false
			for i, line := range lines {
				m := re.MatchString(line)
				if m == opts.invert {
					continue
				}
				matched = true
				if result.total >= maxLines {
					result.truncated = true
					break
				}
				result.contentLines = append(result.contentLines, formatGrepLine(singleFile, f, i+1, line, true))
				result.total++
				if needContext {
					result.contentLines = append(result.contentLines, contextLines(singleFile, f, lines, i, opts, mlines)...)
				}
			}
			_ = matched
		} else {
			// files_with_matches / count：只需匹配计数，不要上下文
			cnt, err := countMatches(f, re, opts.invert)
			if err != nil {
				continue
			}
			if opts.outputMode == "files_with_matches" {
				if (cnt > 0 && !opts.invert) || (opts.invert && cnt > 0) {
					result.files = append(result.files, f)
				}
				// invert 模式下 cnt 表示不匹配行数；只要文件有行即算"含匹配"
				if opts.invert && cnt == 0 {
					// 无行可反选，跳过
				}
			} else if opts.outputMode == "count" {
				result.counts = append(result.counts, fmt.Sprintf("%s:%d", f, cnt))
				result.total += cnt
			}
		}
		if result.total >= maxLines && opts.outputMode == "content" {
			break
		}
	}

	// head_limit 对文件列表/计数同样生效
	if opts.headLimit > 0 {
		if opts.outputMode == "files_with_matches" && len(result.files) > opts.headLimit {
			result.files = result.files[:opts.headLimit]
			result.truncated = true
		}
		if opts.outputMode == "count" && len(result.counts) > opts.headLimit {
			result.counts = result.counts[:opts.headLimit]
			result.truncated = true
		}
	}
	return result, nil
}

// readLines 读取文件全部行（跳过二进制/超大文件），返回行切片与原始行（供上下文）
func readLines(path string) ([]string, []string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.Size() > grepMaxFileSize {
		return nil, nil, errors.New("文件过大，跳过")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, nil, errors.New("二进制文件，跳过")
	}
	// 限制单行长度，避免超长行爆内存
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 64*1024), scannerMaxLine)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return lines, lines, nil
}

// countMatches 统计单文件匹配行数（invert 时为不匹配行数）
func countMatches(path string, re *regexp.Regexp, invert bool) (int, error) {
	lines, _, err := readLines(path)
	if err != nil {
		return 0, err
	}
	cnt := 0
	for _, line := range lines {
		if re.MatchString(line) == invert {
			continue
		}
		cnt++
	}
	return cnt, nil
}

// contextLines 返回匹配行 i 周围的上下文行（已格式化，用 - 分隔表示非匹配行）
func contextLines(singleFile bool, file string, lines []string, i int, opts grepOpts, _ []string) []string {
	var out []string
	before, after := opts.before, opts.after
	if opts.context > 0 {
		before = opts.context
		after = opts.context
	}
	start := i - before
	if start < 0 {
		start = 0
	}
	end := i + after
	if end >= len(lines) {
		end = len(lines) - 1
	}
	for j := start; j <= end; j++ {
		if j == i {
			continue
		}
		out = append(out, formatGrepLine(singleFile, file, j+1, lines[j], false))
	}
	return out
}

// formatGrepLine 格式化一行搜索结果：匹配行用 ":"，上下文行用 "-"。
// 单文件不前缀文件名；多文件前缀 "path:"。
func formatGrepLine(singleFile bool, file string, lineNum int, text string, isMatch bool) string {
	sep := ":"
	if !isMatch {
		sep = "-"
	}
	if singleFile {
		return fmt.Sprintf("%d%s%s", lineNum, sep, text)
	}
	return fmt.Sprintf("%s:%d%s%s", file, lineNum, sep, text)
}

// firstLine 取多行匹配文本的第一行（multiline 模式展示用）
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// toolGrep 在沙箱内搜索文件内容（T-PR-E4：ripgrep 优先 + 丰富参数）。
// 仅允许访问当前用户 conversation/session 目录和公共知识库目录（path 先经沙箱校验）。
func (r *ToolRegistry) toolGrep(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	path, _ := input["path"].(string)
	pattern, _ := input["pattern"].(string)
	if path == "" {
		return nil, errors.New("path 不能为空")
	}
	if pattern == "" {
		return nil, errors.New("pattern 不能为空")
	}

	absPath, err := env.ResolveFilePath(path)
	if err != nil {
		return nil, err
	}

	// 空字节拒绝（与旧实现一致）；正则元字符允许——rg/RE2 保证线性时间，无 ReDoS。
	if strings.ContainsRune(pattern, 0) {
		return nil, errors.New("pattern 包含非法字符: 空字节")
	}

	info, statErr := os.Stat(absPath)
	isDir := statErr == nil && info.IsDir()

	opts := grepOpts{
		pattern:    pattern,
		absPath:    absPath,
		isDir:      isDir,
		outputMode: "content",
	}
	if rec, ok := input["recursive"].(bool); ok {
		opts.recursive = rec
	} else if isDir {
		opts.recursive = true // 目录默认递归（与 schema 描述一致）
	}
	if ic, ok := input["ignore_case"].(bool); ok {
		opts.ignoreCase = ic
	}
	if inv, ok := input["invert"].(bool); ok {
		opts.invert = inv
	}
	if t, ok := input["type"].(string); ok {
		opts.typeName = t
	}
	if g, ok := input["glob"].(string); ok {
		opts.glob = g
	}
	if v, ok := toInt(input["before_context"]); ok {
		opts.before = v
	}
	if v, ok := toInt(input["after_context"]); ok {
		opts.after = v
	}
	if v, ok := toInt(input["context"]); ok {
		opts.context = v
	}
	if ml, ok := input["multiline"].(bool); ok {
		opts.multiline = ml
	}
	if om, ok := input["output_mode"].(string); ok && om != "" {
		opts.outputMode = om
	}
	if v, ok := toInt(input["head_limit"]); ok {
		opts.headLimit = v
	}

	// 校验正则（rg 与纯 Go 共享；提前给出一致错误）
	if _, err := regexp.Compile(pattern); err != nil {
		return nil, fmt.Errorf("正则表达式非法: %w", err)
	}

	base := map[string]interface{}{
		"path":      path,
		"abs_path":  absPath,
		"pattern":   pattern,
		"output_mode": opts.outputMode,
	}

	// 优先 ripgrep
	if stdout, handled, _ := ripgrepSearch(ctx, opts); handled {
		return formatRipgrepResult(stdout, opts, base), nil
	}

	// 回退纯 Go
	res, err := grepSearchGo(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("grep 执行失败: %w", err)
	}
	switch opts.outputMode {
	case "files_with_matches":
		base["files"] = res.files
		base["count"] = len(res.files)
		base["truncated"] = res.truncated
	case "count":
		base["counts"] = res.counts
		base["total"] = res.total
		base["truncated"] = res.truncated
	default:
		base["matches"] = res.contentLines
		base["truncated"] = res.truncated
	}
	return base, nil
}

// formatRipgrepResult 把 rg stdout 解析为结构化结果
func formatRipgrepResult(stdout string, opts grepOpts, base map[string]interface{}) map[string]interface{} {
	lines := splitLines(stdout)
	switch opts.outputMode {
	case "files_with_matches":
		out, trunc := applyHeadLimit(lines, opts.headLimit)
		base["files"] = out
		base["count"] = len(out)
		base["truncated"] = trunc
		return base
	case "count":
		// rg -c 单文件输出裸数字 N，目录输出 "path:N"；统一为 "path:N"
		var counts []string
		total := 0
		for _, l := range lines {
			if l == "" {
				continue
			}
			if !strings.Contains(l, ":") || strings.HasSuffix(l, ":") {
				// 裸数字（单文件）
				l = fmt.Sprintf("%s:%s", opts.absPath, l)
			}
			counts = append(counts, l)
			if idx := strings.LastIndex(l, ":"); idx >= 0 {
				if n, err := strconv.Atoi(l[idx+1:]); err == nil {
					total += n
				}
			}
		}
		out, trunc := applyHeadLimit(counts, opts.headLimit)
		base["counts"] = out
		base["total"] = total
		base["truncated"] = trunc
		return base
	default:
		out, trunc := applyHeadLimit(lines, opts.headLimit)
		base["matches"] = out
		base["truncated"] = trunc
		return base
	}
}

// toInt 从 map 中取整数（兼容 JSON 数字 float64/int）
func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// toolGlob 按 glob 模式匹配文件路径（T-PR-E4 新增）。
// 支持 ** 跨目录递归；结果按 mtime 倒序（最近修改在前）。
func (r *ToolRegistry) toolGlob(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	path, _ := input["path"].(string)
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return nil, errors.New("pattern 不能为空")
	}
	if path == "" {
		path = "."
	}

	absPath, err := env.ResolveFilePath(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("路径不存在: %s", path)
	}
	if !info.IsDir() {
		// path 本身是文件：仅匹配 pattern 与该文件名
		if globMatchPath(pattern, filepath.Base(absPath)) {
			return map[string]interface{}{
				"path":     path,
				"abs_path": absPath,
				"pattern":  pattern,
				"files":    []string{filepath.Base(absPath)},
				"count":    1,
			}, nil
		}
		return map[string]interface{}{
			"path":     path,
			"abs_path": absPath,
			"pattern":  pattern,
			"files":    []string{},
			"count":    0,
		}, nil
	}

	type entry struct {
		rel  string
		mtime int64
	}
	var entries []entry
	walkErr := filepath.WalkDir(absPath, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != absPath && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(absPath, p)
		rel = filepath.ToSlash(rel)
		if !globMatchPath(pattern, rel) {
			return nil
		}
		fi, err := d.Info()
		mt := int64(0)
		if err == nil {
			mt = fi.ModTime().Unix()
		}
		entries = append(entries, entry{rel: rel, mtime: mt})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	// 按 mtime 倒序（最近修改在前）
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mtime > entries[j].mtime
	})

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		files = append(files, e.rel)
	}
	truncated := false
	if hl, ok := toInt(input["head_limit"]); ok && hl > 0 && len(files) > hl {
		files = files[:hl]
		truncated = true
	}
	return map[string]interface{}{
		"path":      path,
		"abs_path":  absPath,
		"pattern":   pattern,
		"files":     files,
		"count":     len(files),
		"truncated": truncated,
	}, nil
}
