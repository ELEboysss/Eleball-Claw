package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 本文件实现常用只读 shell 命令的 Go 内置版本（跨平台、零外部依赖）。
// 背景：Windows 没有 grep/find/ls 等 Unix 工具，且 cmd 的 find.exe 与 GNU find 语义完全不同，
// 导致 Agent 的 Shell/Grep 工具在 Windows 部署大面积失败。
// 设计：Windows 运行器优先走这里的内置实现，未覆盖的白名单命令（python/node 等）再回退外部进程。

// grepMatch 一条匹配记录
type grepMatch struct {
	File    string
	LineNum int
	Text    string
}

const (
	grepMaxMatches   = 200             // 单次搜索最多返回的匹配数，防止结果爆炸
	grepMaxFileSize  = 20 << 20        // 跳过超过 20MB 的文件
	grepBinaryDetect = 8000            // 读取前 N 字节探测二进制文件
	scannerMaxLine   = 1 << 20         // 单行最大 1MB
)

// searchPattern 在 absPath（文件或目录）内按正则逐行搜索。
// recursive=false 且 absPath 为目录时只搜索直接子文件；跳过二进制与超大文件。
// 返回结果按文件路径、行号排序，最多 grepMaxMatches 条。
func searchPattern(ctx context.Context, absPath string, re *regexp.Regexp, recursive bool, invert bool) ([]grepMatch, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("路径不存在: %s", absPath)
	}

	// 收集待搜索文件
	var files []string
	if !info.IsDir() {
		files = append(files, absPath)
	} else if recursive {
		err = filepath.WalkDir(absPath, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // 跳过无法访问的条目
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && p != absPath {
					return filepath.SkipDir // 跳过隐藏目录（.git 等）
				}
				return nil
			}
			files = append(files, p)
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				files = append(files, filepath.Join(absPath, e.Name()))
			}
		}
	}
	sort.Strings(files)

	matches := make([]grepMatch, 0, 16)
	for _, f := range files {
		if len(matches) >= grepMaxMatches || ctx.Err() != nil {
			break
		}
		fileMatches, err := searchOneFile(f, re, invert, grepMaxMatches-len(matches))
		if err != nil {
			continue // 单个文件失败不阻断整体搜索
		}
		matches = append(matches, fileMatches...)
	}
	return matches, nil
}

// searchOneFile 在单个文件内逐行匹配，跳过二进制与超大文件
func searchOneFile(path string, re *regexp.Regexp, invert bool, limit int) ([]grepMatch, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > grepMaxFileSize {
		return nil, errors.New("文件过大，跳过")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// 二进制探测：前 grepBinaryDetect 字节内含 NUL 即视为二进制
	head := make([]byte, grepBinaryDetect)
	n, _ := f.Read(head)
	if n > 0 && bytes.IndexByte(head[:n], 0) >= 0 {
		return nil, errors.New("二进制文件，跳过")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}

	matches := make([]grepMatch, 0, 8)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), scannerMaxLine)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		matched := re.MatchString(scanner.Text())
		if matched != invert {
			matches = append(matches, grepMatch{File: path, LineNum: lineNum, Text: scanner.Text()})
			if len(matches) >= limit {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil && len(matches) == 0 {
		return nil, err
	}
	return matches, nil
}

// formatGrepMatches 按 grep 输出习惯格式化：单文件搜索输出 "行号:内容"，多文件输出 "路径:行号:内容"
func formatGrepMatches(matches []grepMatch, singleFile bool) []string {
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if singleFile {
			out = append(out, fmt.Sprintf("%d:%s", m.LineNum, m.Text))
		} else {
			out = append(out, fmt.Sprintf("%s:%d:%s", m.File, m.LineNum, m.Text))
		}
	}
	return out
}

// builtinShell 尝试用 Go 内置实现执行命令。
// 返回 (output, handled, err)；handled=false 表示无内置实现，调用方应回退外部进程。
// 注意：新增分支时同步 builtinCommandNames（which 内置实现据此判断命令可用性）。
func builtinShell(ctx context.Context, command string, args []string) (string, bool, error) {
	switch command {
	case "pwd":
		return builtinPwd()
	case "echo":
		return builtinEcho(args)
	case "printf":
		return builtinPrintf(args)
	case "ls":
		return builtinLs(args)
	case "cat":
		return builtinCat(ctx, args)
	case "head":
		return builtinHeadTail(ctx, args, true)
	case "tail":
		return builtinHeadTail(ctx, args, false)
	case "wc":
		return builtinWc(ctx, args)
	case "grep":
		return builtinGrep(ctx, args)
	case "find":
		return builtinFind(ctx, args)
	case "sort":
		return builtinSort(ctx, args)
	case "uniq":
		return builtinUniq(ctx, args)
	case "cut":
		return builtinCut(ctx, args)
	case "dirname":
		return builtinDirBasename(args, true)
	case "basename":
		return builtinDirBasename(args, false)
	case "date":
		return time.Now().Format("2006-01-02 15:04:05") + "\n", true, nil
	case "whoami":
		return builtinWhoami()
	case "hostname":
		name, err := os.Hostname()
		return name + "\n", true, err
	case "uname":
		return runtime.GOOS + " " + runtime.GOARCH + "\n", true, nil
	case "which", "where":
		return builtinWhich(args)
	}
	return "", false, nil
}

// builtinCommandNames 拥有 Go 内置实现的命令集合（与 builtinShell 分支保持一致）
var builtinCommandNames = map[string]bool{
	"pwd": true, "echo": true, "printf": true, "ls": true, "cat": true,
	"head": true, "tail": true, "wc": true, "grep": true, "find": true,
	"sort": true, "uniq": true, "cut": true, "dirname": true, "basename": true,
	"date": true, "whoami": true, "hostname": true, "uname": true,
	"which": true, "where": true,
}

// builtinWhich 查询命令可用性：优先报告 Go 内置实现（跨平台可用），其次查找 PATH 中的可执行文件。
// 与 GNU which 不同：未找到不报错退出码，逐行输出状态，方便模型阅读后继续决策。
func builtinWhich(args []string) (string, bool, error) {
	if len(args) == 0 {
		return "", true, errors.New("which 缺少命令名参数")
	}
	var b strings.Builder
	for _, name := range args {
		switch {
		case builtinCommandNames[name]:
			fmt.Fprintf(&b, "%s: 内置命令（跨平台可用）\n", name)
		default:
			if p := findExecutable(name); p != "" {
				fmt.Fprintf(&b, "%s: %s\n", name, p)
			} else {
				fmt.Fprintf(&b, "%s: 未找到（不可用）\n", name)
			}
		}
	}
	return b.String(), true, nil
}

func builtinPwd() (string, bool, error) {
	dir, err := os.Getwd()
	return dir + "\n", true, err
}

func builtinEcho(args []string) (string, bool, error) {
	newline := true
	rest := args
	if len(rest) > 0 && rest[0] == "-n" {
		newline = false
		rest = rest[1:]
	}
	out := strings.Join(rest, " ")
	if newline {
		out += "\n"
	}
	return out, true, nil
}

func builtinPrintf(args []string) (string, bool, error) {
	if len(args) == 0 {
		return "", true, errors.New("printf 缺少格式串")
	}
	format := args[0]
	// 还原 shell printf 的常见转义
	format = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\\`, `\`).Replace(format)
	if len(args) == 1 || !strings.Contains(format, "%") {
		return format, true, nil
	}
	// %s 统一按 %v 处理，避免类型不匹配
	format = strings.ReplaceAll(format, "%s", "%v")
	vars := make([]interface{}, 0, len(args)-1)
	for _, a := range args[1:] {
		vars = append(vars, a)
	}
	return fmt.Sprintf(format, vars...), true, nil
}

// readFileLines 读取文件全部行（去除行尾 \n）
func readFileLines(ctx context.Context, path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), scannerMaxLine)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return lines, ctx.Err()
		}
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// parseNumFlag 解析 -n 数字参数（如 head -n 20 / -20）
func parseNumFlag(args []string, def int) (num int, rest []string, err error) {
	num = def
	rest = args
	if len(rest) == 0 {
		return num, rest, nil
	}
	if rest[0] == "-n" {
		if len(rest) < 2 {
			return num, rest, errors.New("-n 缺少数字参数")
		}
		num, err = strconv.Atoi(rest[1])
		rest = rest[2:]
		return num, rest, err
	}
	if strings.HasPrefix(rest[0], "-") && len(rest[0]) > 1 {
		if v, e := strconv.Atoi(rest[0][1:]); e == nil {
			num = v
			rest = rest[1:]
		}
	}
	return num, rest, nil
}

func builtinHeadTail(ctx context.Context, args []string, head bool) (string, bool, error) {
	num, files, err := parseNumFlag(args, 10)
	if err != nil || num < 0 {
		return "", true, fmt.Errorf("非法行数参数: %v", args)
	}
	if len(files) == 0 {
		return "", true, errors.New("缺少文件路径（内置实现不支持标准输入）")
	}
	var b strings.Builder
	for _, path := range files {
		lines, err := readFileLines(ctx, path)
		if err != nil {
			return b.String(), true, fmt.Errorf("读取失败 %s: %w", path, err)
		}
		if head && num < len(lines) {
			lines = lines[:num]
		}
		if !head && num < len(lines) {
			lines = lines[len(lines)-num:]
		}
		for _, l := range lines {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	return b.String(), true, nil
}

func builtinCat(ctx context.Context, args []string) (string, bool, error) {
	if len(args) == 0 {
		return "", true, errors.New("缺少文件路径（内置实现不支持标准输入）")
	}
	var b strings.Builder
	for _, path := range args {
		data, err := os.ReadFile(path)
		if err != nil {
			return b.String(), true, fmt.Errorf("读取失败 %s: %w", path, err)
		}
		b.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			b.WriteString("\n")
		}
	}
	return b.String(), true, nil
}

func builtinWc(ctx context.Context, args []string) (string, bool, error) {
	showLines, showWords, showBytes := false, false, false
	var files []string
	for _, a := range args {
		switch a {
		case "-l":
			showLines = true
		case "-w":
			showWords = true
		case "-c":
			showBytes = true
		default:
			files = append(files, a)
		}
	}
	if !showLines && !showWords && !showBytes {
		showLines, showWords, showBytes = true, true, true
	}
	if len(files) == 0 {
		return "", true, errors.New("缺少文件路径（内置实现不支持标准输入）")
	}
	var b strings.Builder
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return b.String(), true, fmt.Errorf("路径不存在: %s", path)
		}
		var counts []string
		if showLines {
			lines, err := readFileLines(ctx, path)
			if err != nil {
				return b.String(), true, err
			}
			counts = append(counts, strconv.Itoa(len(lines)))
		}
		if showWords || showBytes {
			data, err := os.ReadFile(path)
			if err != nil {
				return b.String(), true, err
			}
			if showWords {
				counts = append(counts, strconv.Itoa(len(strings.Fields(string(data)))))
			}
			if showBytes {
				counts = append(counts, strconv.FormatInt(info.Size(), 10))
			}
		}
		counts = append(counts, path)
		b.WriteString(strings.Join(counts, " ") + "\n")
	}
	return b.String(), true, nil
}

func builtinLs(args []string) (string, bool, error) {
	showAll, long := false, false
	var paths []string
	for _, a := range args {
		switch {
		case a == "-a" || a == "-la" || a == "-al":
			showAll = true
			long = long || a != "-a"
		case a == "-l":
			long = true
		case strings.HasPrefix(a, "-"):
			return "", true, fmt.Errorf("内置 ls 不支持的参数: %s", a)
		default:
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var b strings.Builder
	for _, path := range paths {
		entries, err := os.ReadDir(path)
		if err != nil {
			return b.String(), true, fmt.Errorf("读取目录失败 %s: %w", path, err)
		}
		for _, e := range entries {
			if !showAll && strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if long {
				info, err := e.Info()
				if err != nil {
					continue
				}
				fmt.Fprintf(&b, "%10d %s %s\n", info.Size(), info.ModTime().Format("2006-01-02 15:04"), e.Name())
			} else {
				b.WriteString(e.Name() + "\n")
			}
		}
	}
	return b.String(), true, nil
}

func builtinGrep(ctx context.Context, args []string) (string, bool, error) {
	var (
		recursive, ignoreCase, invertMatch, countOnly, filesOnly bool
		pattern                                                  string
		paths                                                    []string
	)
	// 解析参数
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) && pattern == "" {
				pattern = args[i+1]
				paths = append(paths, args[i+2:]...)
			} else {
				paths = append(paths, args[i+1:]...)
			}
			i = len(args)
		case a == "-e":
			if i+1 >= len(args) {
				return "", true, errors.New("-e 缺少 pattern")
			}
			pattern = args[i+1]
			i++
		case a == "-r" || a == "-R":
			recursive = true
		case a == "-n":
			// 内置实现始终带行号，兼容忽略
		case a == "-i":
			ignoreCase = true
		case a == "-v":
			invertMatch = true
		case a == "-c":
			countOnly = true
		case a == "-l":
			filesOnly = true
		case strings.HasPrefix(a, "-"):
			return "", true, fmt.Errorf("内置 grep 不支持的参数: %s（支持 -r/-n/-i/-v/-c/-l/-e/--）", a)
		case pattern == "":
			pattern = a
		default:
			paths = append(paths, a)
		}
	}
	if pattern == "" {
		return "", true, errors.New("grep 缺少 pattern")
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", true, fmt.Errorf("正则表达式非法: %w", err)
	}

	var all []grepMatch
	for _, p := range paths {
		matches, err := searchPattern(ctx, p, re, recursive, invertMatch)
		if err != nil {
			return "", true, err
		}
		all = append(all, matches...)
		if len(all) >= grepMaxMatches {
			all = all[:grepMaxMatches]
			break
		}
	}

	switch {
	case countOnly:
		perFile := map[string]int{}
		var order []string
		for _, m := range all {
			if _, ok := perFile[m.File]; !ok {
				order = append(order, m.File)
			}
			perFile[m.File]++
		}
		var b strings.Builder
		for _, f := range order {
			fmt.Fprintf(&b, "%s:%d\n", f, perFile[f])
		}
		return b.String(), true, nil
	case filesOnly:
		seen := map[string]bool{}
		var b strings.Builder
		for _, m := range all {
			if !seen[m.File] {
				seen[m.File] = true
				b.WriteString(m.File + "\n")
			}
		}
		return b.String(), true, nil
	default:
		// 单文件搜索不前缀文件名；目录或多路径搜索输出 "路径:行号:内容"
		singleFile := len(paths) == 1
		if info, err := os.Stat(paths[0]); err == nil && info.IsDir() {
			singleFile = false
		}
		lines := formatGrepMatches(all, singleFile)
		return strings.Join(lines, "\n") + trailingNewline(lines), true, nil
	}
}

func trailingNewline(lines []string) string {
	if len(lines) > 0 {
		return "\n"
	}
	return ""
}

func builtinFind(ctx context.Context, args []string) (string, bool, error) {
	var (
		paths     []string
		maxDepth  = -1
		typeFlag  string
		namePat   string
		ignoreCas bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-maxdepth":
			if i+1 >= len(args) {
				return "", true, errors.New("-maxdepth 缺少数字")
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil {
				return "", true, fmt.Errorf("-maxdepth 参数非法: %s", args[i+1])
			}
			maxDepth = v
			i++
		case "-type":
			if i+1 >= len(args) || (args[i+1] != "f" && args[i+1] != "d") {
				return "", true, errors.New("-type 仅支持 f 或 d")
			}
			typeFlag = args[i+1]
			i++
		case "-name", "-iname":
			if i+1 >= len(args) {
				return "", true, errors.New(a + " 缺少模式")
			}
			namePat = args[i+1]
			ignoreCas = a == "-iname"
			i++
		case "-o":
			return "", true, errors.New("内置 find 暂不支持 -o 组合条件，请拆成多次调用")
		default:
			if strings.HasPrefix(a, "-") {
				return "", true, fmt.Errorf("内置 find 不支持的参数: %s（支持 -maxdepth/-type/-name/-iname）", a)
			}
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	if ignoreCas {
		namePat = strings.ToLower(namePat)
	}

	var b strings.Builder
	count := 0
	const maxResults = 500
	for _, root := range paths {
		if count >= maxResults || ctx.Err() != nil {
			break
		}
		rootDepth := len(strings.Split(filepath.Clean(root), string(os.PathSeparator)))
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctx.Err() != nil || count >= maxResults {
				return fs.SkipAll
			}
			depth := len(strings.Split(filepath.Clean(p), string(os.PathSeparator))) - rootDepth
			if maxDepth >= 0 && depth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if typeFlag == "f" && d.IsDir() {
				return nil
			}
			if typeFlag == "d" && !d.IsDir() {
				return nil
			}
			if namePat != "" {
				name := d.Name()
				if ignoreCas {
					name = strings.ToLower(name)
				}
				ok, err := filepath.Match(namePat, name)
				if err != nil || !ok {
					return nil
				}
			}
			b.WriteString(p + "\n")
			count++
			return nil
		})
		if err != nil && err != fs.SkipAll {
			return b.String(), true, fmt.Errorf("遍历失败 %s: %w", root, err)
		}
	}
	return b.String(), true, nil
}

func builtinSort(ctx context.Context, args []string) (string, bool, error) {
	reverse, numeric, unique := false, false, false
	var files []string
	for _, a := range args {
		switch a {
		case "-r":
			reverse = true
		case "-n":
			numeric = true
		case "-u":
			unique = true
		default:
			if strings.HasPrefix(a, "-") {
				return "", true, fmt.Errorf("内置 sort 不支持的参数: %s", a)
			}
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		return "", true, errors.New("缺少文件路径（内置实现不支持标准输入）")
	}
	var lines []string
	for _, path := range files {
		ls, err := readFileLines(ctx, path)
		if err != nil {
			return "", true, err
		}
		lines = append(lines, ls...)
	}
	if numeric {
		sort.SliceStable(lines, func(i, j int) bool {
			vi, _ := strconv.ParseFloat(strings.TrimSpace(lines[i]), 64)
			vj, _ := strconv.ParseFloat(strings.TrimSpace(lines[j]), 64)
			return vi < vj
		})
	} else {
		sort.Strings(lines)
	}
	var b strings.Builder
	prev := ""
	for i, l := range lines {
		if unique && i > 0 && l == prev {
			continue
		}
		prev = l
		b.WriteString(l + "\n")
	}
	out := b.String()
	if reverse {
		ls := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		for i, j := 0, len(ls)-1; i < j; i, j = i+1, j-1 {
			ls[i], ls[j] = ls[j], ls[i]
		}
		out = strings.Join(ls, "\n") + "\n"
	}
	return out, true, nil
}

func builtinUniq(ctx context.Context, args []string) (string, bool, error) {
	countMode := false
	var files []string
	for _, a := range args {
		if a == "-c" {
			countMode = true
		} else if strings.HasPrefix(a, "-") {
			return "", true, fmt.Errorf("内置 uniq 不支持的参数: %s", a)
		} else {
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		return "", true, errors.New("缺少文件路径（内置实现不支持标准输入）")
	}
	lines, err := readFileLines(ctx, files[0])
	if err != nil {
		return "", true, err
	}
	var b strings.Builder
	for i := 0; i < len(lines); {
		j := i
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		if countMode {
			fmt.Fprintf(&b, "%7d %s\n", j-i, lines[i])
		} else {
			b.WriteString(lines[i] + "\n")
		}
		i = j
	}
	return b.String(), true, nil
}

func builtinCut(ctx context.Context, args []string) (string, bool, error) {
	delim := "\t"
	var fieldsSpec string
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-d":
			if i+1 >= len(args) {
				return "", true, errors.New("-d 缺少分隔符")
			}
			delim = args[i+1]
			i++
		case strings.HasPrefix(a, "-d"):
			delim = strings.TrimPrefix(a, "-d")
		case a == "-f":
			if i+1 >= len(args) {
				return "", true, errors.New("-f 缺少字段列表")
			}
			fieldsSpec = args[i+1]
			i++
		case strings.HasPrefix(a, "-f"):
			fieldsSpec = strings.TrimPrefix(a, "-f")
		default:
			files = append(files, a)
		}
	}
	if fieldsSpec == "" {
		return "", true, errors.New("cut 缺少 -f 字段列表")
	}
	// 解析 1-based 字段列表，如 "1,3" 或 "2"
	var wanted []int
	for _, part := range strings.Split(fieldsSpec, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || v < 1 {
			return "", true, fmt.Errorf("-f 字段非法: %s（暂不支持区间，使用逗号分隔）", part)
		}
		wanted = append(wanted, v)
	}
	if len(files) == 0 {
		return "", true, errors.New("缺少文件路径（内置实现不支持标准输入）")
	}
	var b strings.Builder
	for _, path := range files {
		lines, err := readFileLines(ctx, path)
		if err != nil {
			return b.String(), true, err
		}
		for _, l := range lines {
			cols := strings.Split(l, delim)
			var picked []string
			for _, idx := range wanted {
				if idx <= len(cols) {
					picked = append(picked, cols[idx-1])
				}
			}
			b.WriteString(strings.Join(picked, delim) + "\n")
		}
	}
	return b.String(), true, nil
}

func builtinDirBasename(args []string, dir bool) (string, bool, error) {
	if len(args) == 0 {
		return "", true, errors.New("缺少路径参数")
	}
	var b strings.Builder
	for _, p := range args {
		p = strings.TrimRight(p, `/\`)
		if dir {
			b.WriteString(filepath.Dir(p) + "\n")
		} else {
			b.WriteString(filepath.Base(p) + "\n")
		}
	}
	return b.String(), true, nil
}

func builtinWhoami() (string, bool, error) {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username + "\n", true, nil
	}
	if name := os.Getenv("USERNAME"); name != "" {
		return name + "\n", true, nil
	}
	if name := os.Getenv("USER"); name != "" {
		return name + "\n", true, nil
	}
	return "", true, errors.New("无法获取当前用户")
}
