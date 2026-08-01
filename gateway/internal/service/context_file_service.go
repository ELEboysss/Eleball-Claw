package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eleball/gateway/internal/config"
	"gopkg.in/yaml.v3"
)

// C8：项目记忆文件加载服务。
// 负责从 claw 本地 cwd 向上遍历加载 CLAUDE.md / AGENTS.md，以及按条件加载 .claw/rules/*.md。
// 与云端 gateway 概念对齐：统一使用 <project_instructions> 注入块与 ProjectMemoryFile 响应结构。

type (
	// ContextFile 单个已加载的上下文文件。
	ContextFile struct {
		Path    string `json:"path"`   // 绝对路径
		Source  string `json:"source"` // 文件名（如 CLAUDE.md）
		Content string `json:"-"`      // 文件原始内容（不计入 JSON）
		Size    int    `json:"size"`   // 内容字符数（按 rune 计）
		Applied bool   `json:"applied"`// 是否已注入 system prompt
	}

	// ContextFileService 项目记忆加载器。
	ContextFileService struct {
		cfg config.ContextFilesConfig
	}
)

// 上下文文件候选名（大小写不敏感）。按此顺序在同一目录内查找，但只取第一个匹配。
var contextFileCandidates = []string{
	"CLAUDE.md",
	"AGENTS.md",
	"Claude.md",
	"Agents.md",
	"claude.md",
	"agents.md",
}

// NewContextFileService 创建项目记忆加载服务。
func NewContextFileService(cfg config.ContextFilesConfig) *ContextFileService {
	if cfg.MaxTotalChars <= 0 {
		cfg.MaxTotalChars = 12000
	}
	if cfg.MaxFileChars <= 0 {
		cfg.MaxFileChars = 8000
	}
	return &ContextFileService{cfg: cfg}
}

// FindContextFiles 从 cwd 向上遍历发现所有 CLAUDE.md / AGENTS.md（不施加总预算）。
// 返回结果按「父目录 -> 子目录」顺序排列、按绝对路径去重。子目录文件可覆盖/补充父目录语义。
// cwd 为空或无效时返回空切片。
func (s *ContextFileService) FindContextFiles(cwd string) ([]ContextFile, error) {
	if cwd == "" {
		return []ContextFile{}, nil
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return []ContextFile{}, nil
	}
	info, err := os.Stat(absCwd)
	if err != nil || !info.IsDir() {
		return []ContextFile{}, nil
	}

	// 收集从根到 cwd 的路径链；遇到项目根（.git）、用户主目录或文件系统根时停止。
	// 项目根边界防止在临时目录测试/运行时意外加载仓库外的 CLAUDE.md。
	var dirs []string
	cur := absCwd
	home, _ := os.UserHomeDir()
	for {
		dirs = append([]string{cur}, dirs...)
		// 当前目录是项目根：处理完后停止上溯
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur || cur == home {
			break
		}
		cur = parent
	}

	var files []ContextFile
	seen := make(map[string]struct{})
	for _, dir := range dirs {
		for _, name := range contextFileCandidates {
			path := filepath.Join(dir, name)
			if _, ok := seen[path]; ok {
				continue
			}
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			// 大小写不敏感：若目录下已有同名不同大小写文件，按 candidates 顺序第一个为准
			seen[path] = struct{}{}
			file, err := s.readFile(path, name)
			if err != nil {
				continue
			}
			files = append(files, file)
			break // 同一目录只取一个上下文文件
		}
	}
	return files, nil
}

// LoadContextFiles 返回实际需要注入到 system prompt 的项目记忆文件（已按总预算截断）。
func (s *ContextFileService) LoadContextFiles(cwd string) ([]ContextFile, error) {
	files, err := s.FindContextFiles(cwd)
	if err != nil {
		return nil, err
	}
	return s.applyBudget(files), nil
}

// LoadRuleFiles 加载 cwd 下 .claw/rules/*.md 中 frontmatter `paths:` 与 touchedPaths 匹配的规则文件。
// 仅当 cfg.RulesEnabled=true 时生效；否则返回空切片。
// 规则文件本身不向上遍历，假设 cwd 即为项目根（claw 工作目录由用户选定）。
func (s *ContextFileService) LoadRuleFiles(cwd string, touchedPaths []string) ([]ContextFile, error) {
	if !s.cfg.RulesEnabled || cwd == "" || len(touchedPaths) == 0 {
		return []ContextFile{}, nil
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return []ContextFile{}, nil
	}
	rulesDir := filepath.Join(absCwd, ".claw", "rules")
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return []ContextFile{}, nil
	}

	var files []ContextFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(rulesDir, e.Name())
		globs, err := s.readRuleFrontmatterPaths(path)
		if err != nil || len(globs) == 0 {
			continue
		}
		if !ruleMatchesAny(globs, touchedPaths) {
			continue
		}
		file, err := s.readFile(path, e.Name())
		if err != nil {
			continue
		}
		files = append(files, file)
	}
	return files, nil
}

// ListLoadedFiles 返回 /memory  slash 命令展示的文件列表（仅上下文文件，不含动态规则）。
// 返回所有发现的文件；Applied=true 表示该文件在当前配置下会被注入 system prompt（已启用且在预算内）。
func (s *ContextFileService) ListLoadedFiles(cwd string) ([]ContextFile, error) {
	files, err := s.FindContextFiles(cwd)
	if err != nil {
		return nil, err
	}
	injected := s.applyBudget(files)
	injectedSet := make(map[string]struct{})
	for _, f := range injected {
		injectedSet[f.Path] = struct{}{}
	}
	for i := range files {
		files[i].Applied = s.cfg.Enabled
		if _, ok := injectedSet[files[i].Path]; !ok {
			files[i].Applied = false
		}
	}
	return files, nil
}

// FormatInjectionBlock 将已加载的上下文文件格式化为 <project_instructions> 注入块。
// 按文件顺序依次输出，每个文件一个 <project_instructions path="..." source="..."> 块。
// 空切片或内容全部被截断时返回空字符串。
func (s *ContextFileService) FormatInjectionBlock(files []ContextFile) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, f := range files {
		if f.Content == "" {
			continue
		}
		fmt.Fprintf(&sb, "<project_instructions path=\"%s\" source=\"%s\">\n%s\n</project_instructions>\n\n",
			escapeXML(f.Path), escapeXML(f.Source), escapeXML(f.Content))
	}
	return strings.TrimSpace(sb.String())
}

// readFile 读取单个文件并施加单文件字符上限。
func (s *ContextFileService) readFile(path, source string) (ContextFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ContextFile{}, err
	}
	content := string(data)
	runes := []rune(content)
	truncated := false
	if len(runes) > s.cfg.MaxFileChars {
		runes = runes[:s.cfg.MaxFileChars]
		truncated = true
	}
	if truncated {
		runes = append(runes, []rune(fmt.Sprintf("\n\n[项目记忆文件 %s 已按 %d 字符截断]", source, s.cfg.MaxFileChars))...)
	}
	content = string(runes)
	return ContextFile{
		Path:    path,
		Source:  source,
		Content: content,
		Size:    len(runes),
		Applied: true,
	}, nil
}

// applyBudget 按总字符预算截断文件列表；超出预算时保留完整文件直到放不下为止。
func (s *ContextFileService) applyBudget(files []ContextFile) []ContextFile {
	if len(files) == 0 {
		return files
	}
	var result []ContextFile
	used := 0
	for _, f := range files {
		if used+f.Size > s.cfg.MaxTotalChars && len(result) > 0 {
			// 超出总预算：不再追加后续文件（后续是子目录文件，优先级更低）
			break
		}
		result = append(result, f)
		used += f.Size
	}
	return result
}

// readRuleFrontmatterPaths 读取 .claw/rules/*.md 的 frontmatter，返回 paths 字段的 glob 列表。
func (s *ContextFileService) readRuleFrontmatterPaths(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return nil, nil
	}
	parts := strings.SplitN(text, "---", 3)
	if len(parts) < 3 {
		return nil, nil
	}
	var meta struct {
		Paths []string `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
		return nil, err
	}
	return meta.Paths, nil
}

// ruleMatchesAny 判断任意 glob 是否匹配任意 touchedPath。
func ruleMatchesAny(globs, touchedPaths []string) bool {
	for _, g := range globs {
		for _, p := range touchedPaths {
			if matchGlob(g, p) {
				return true
			}
		}
	}
	return false
}

// escapeXML 对属性值中的特殊字符做简单转义，用于 <project_instructions> 标签属性。
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
