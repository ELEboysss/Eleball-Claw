package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eleball/gateway/internal/repository"
	"gopkg.in/yaml.v3"
)

// SlashCommandCategory 命令分类常量
const (
	SlashCategoryBuiltin   = "builtin"
	SlashCategorySkills    = "skills"
	SlashCategoryTemplates = "templates"
)

// SlashCommand 单个 slash 命令元数据
// 与 specs/api-schema.yml#/components/schemas/SlashCommand 保持一致
type SlashCommand struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Category        string `json:"category"`
	ArgumentsHint   string `json:"arguments_hint,omitempty"`
	RequiresHandler bool   `json:"requires_handler"`
	Handler         string `json:"handler,omitempty"`
	Icon            string `json:"icon,omitempty"`
}

// SlashCommandCategory 分组后的命令分类
// 与 specs/api-schema.yml#/components/schemas/SlashCommandCategory 保持一致
type SlashCommandCategory struct {
	Name     string         `json:"name"`
	Label    string         `json:"label"`
	Commands []SlashCommand `json:"commands"`
}

// SlashCommandsResponse slash 命令列表响应
// 与 specs/api-schema.yml#/components/schemas/SlashCommandsResponse 保持一致
type SlashCommandsResponse struct {
	Categories []SlashCommandCategory `json:"categories"`
}

// FileFuzzyItem 文件 fuzzy 补全条目
// 与 specs/api-schema.yml#/components/schemas/FileFuzzyItem 保持一致
type FileFuzzyItem struct {
	Path  string  `json:"path"`
	Type  string  `json:"type"`
	Score float64 `json:"score,omitempty"`
}

// FilesFuzzyResponse 文件 fuzzy 补全响应
// 与 specs/api-schema.yml#/components/schemas/FilesFuzzyResponse 保持一致
type FilesFuzzyResponse struct {
	Files []FileFuzzyItem `json:"files"`
}

// PromptTemplate 提示词模板 frontmatter + body
type PromptTemplate struct {
	Slug         string
	Description  string
	ArgumentsHint string
	Body         string
}

// SlashCommandService 输入栏命令中心后端服务（云端 + claw 同构实现）
// 负责聚合内置命令、已安装秘技、提示词模板三类 slash 命令，以及 @ 文件 fuzzy 索引。
type SlashCommandService struct {
	agentRepo  *repository.AgentRepo
	promptsDir string
	sandbox    *FileSandbox
}

// NewSlashCommandService 创建 slash 命令服务
// promptsDir 为空时不扫描模板；sandbox 为空时不支持 @ 文件索引。
func NewSlashCommandService(agentRepo *repository.AgentRepo, promptsDir string, sandbox *FileSandbox) *SlashCommandService {
	return &SlashCommandService{
		agentRepo:  agentRepo,
		promptsDir: promptsDir,
		sandbox:    sandbox,
	}
}

// ListCommands 返回当前用户可见的分组 slash 命令列表
func (s *SlashCommandService) ListCommands(userID string) (*SlashCommandsResponse, error) {
	resp := &SlashCommandsResponse{Categories: []SlashCommandCategory{}}

	resp.Categories = append(resp.Categories, SlashCommandCategory{
		Name:     SlashCategoryBuiltin,
		Label:    "内置命令",
		Commands: s.builtinCommands(),
	})

	skills, err := s.skillCommands(userID)
	if err != nil {
		return nil, fmt.Errorf("枚举秘技失败: %w", err)
	}
	resp.Categories = append(resp.Categories, SlashCommandCategory{
		Name:     SlashCategorySkills,
		Label:    "已安装秘技",
		Commands: skills,
	})

	templates, err := s.templateCommands()
	if err != nil {
		return nil, fmt.Errorf("扫描提示词模板失败: %w", err)
	}
	resp.Categories = append(resp.Categories, SlashCommandCategory{
		Name:     SlashCategoryTemplates,
		Label:    "提示词模板",
		Commands: templates,
	})

	return resp, nil
}

// builtinCommands 返回固定内置命令列表
func (s *SlashCommandService) builtinCommands() []SlashCommand {
	return []SlashCommand{
		{
			Name:            "/clear",
			Description:     "清空当前对话的上下文历史",
			Category:        SlashCategoryBuiltin,
			RequiresHandler: true,
			Handler:         "clear",
			Icon:            "🗑️",
		},
		{
			Name:            "/compact",
			Description:     "手动压缩对话上下文（可带关注主题）",
			Category:        SlashCategoryBuiltin,
			ArgumentsHint:   "[focus]",
			RequiresHandler: true,
			Handler:         "compact",
			Icon:            "🗜️",
		},
		{
			Name:            "/plan",
			Description:     "进入 Plan 模式：只读研究并生成执行计划",
			Category:        SlashCategoryBuiltin,
			RequiresHandler: true,
			Handler:         "plan",
			Icon:            "📋",
		},
		{
			Name:            "/model",
			Description:     "切换当前会话使用的模型",
			Category:        SlashCategoryBuiltin,
			ArgumentsHint:   "{model_id}",
			RequiresHandler: true,
			Handler:         "model",
			Icon:            "🤖",
		},
		{
			Name:            "/memory",
			Description:     "查看已加载的项目记忆文件（C8 占位）",
			Category:        SlashCategoryBuiltin,
			RequiresHandler: true,
			Handler:         "memory",
			Icon:            "🧠",
		},
	}
}

// skillCommands 从已购买且激活的秘技中生成 slash 命令
func (s *SlashCommandService) skillCommands(userID string) ([]SlashCommand, error) {
	if s.agentRepo == nil || userID == "" {
		return []SlashCommand{}, nil
	}
	items, err := s.agentRepo.ListPurchasedExecutableTools(userID)
	if err != nil {
		return nil, err
	}
	commands := make([]SlashCommand, 0, len(items))
	for _, item := range items {
		name := "/skill:" + item.ID
		desc := item.Description
		if desc == "" {
			desc = item.Name
		}
		commands = append(commands, SlashCommand{
			Name:            name,
			Description:     desc,
			Category:        SlashCategorySkills,
			RequiresHandler: false, // 前端插入 /skill:id 文本后由 Agent 工具链识别
			Handler:         "",
			Icon:            item.IconURL,
		})
	}
	return commands, nil
}

// templateCommands 扫描 ~/.claw/prompts/*.md 生成提示词模板命令
func (s *SlashCommandService) templateCommands() ([]SlashCommand, error) {
	if s.promptsDir == "" {
		return []SlashCommand{}, nil
	}
	entries, err := os.ReadDir(s.promptsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SlashCommand{}, nil
		}
		return nil, err
	}
	commands := make([]SlashCommand, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(s.promptsDir, e.Name())
		tmpl, err := s.loadPromptTemplate(path)
		if err != nil {
			// 单文件损坏不影响其他模板加载
			continue
		}
		commands = append(commands, SlashCommand{
			Name:            "/" + tmpl.Slug,
			Description:     tmpl.Description,
			Category:        SlashCategoryTemplates,
			ArgumentsHint:   tmpl.ArgumentsHint,
			RequiresHandler: false, // 前端直接替换文本
			Icon:            "📝",
		})
	}
	return commands, nil
}

// loadPromptTemplate 解析 frontmatter + body
// frontmatter 支持 description、argument_hint 字段；body 中可包含 $ARGUMENTS / $1 / $@ 等占位符。
func (s *SlashCommandService) loadPromptTemplate(path string) (*PromptTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	slug := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	tmpl := &PromptTemplate{
		Slug:         slug,
		Description:  slug,
		ArgumentsHint: "",
		Body:         string(data),
	}

	if strings.HasPrefix(string(data), "---\n") || strings.HasPrefix(string(data), "---\r\n") {
		parts := strings.SplitN(string(data), "---", 3)
		if len(parts) >= 3 {
			var meta struct {
				Description   string `yaml:"description"`
				ArgumentHint  string `yaml:"argument_hint"`
			}
			if err := yaml.Unmarshal([]byte(parts[1]), &meta); err == nil {
				if meta.Description != "" {
					tmpl.Description = meta.Description
				}
				tmpl.ArgumentsHint = meta.ArgumentHint
			}
			tmpl.Body = strings.TrimSpace(parts[2])
		}
	}
	return tmpl, nil
}

// FuzzyFiles 在指定工作目录下 fuzzy 搜索文件路径
// cwd 为空时返回空结果；query 为空时返回目录下的顶层条目。
func (s *SlashCommandService) FuzzyFiles(cwd, query string, limit int) (*FilesFuzzyResponse, error) {
	resp := &FilesFuzzyResponse{Files: []FileFuzzyItem{}}
	if s.sandbox == nil || cwd == "" {
		return resp, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// 校验 cwd 落在沙箱允许范围（claw 用 projectRoot 第三根）
	absCwd, err := s.sandbox.ResolveProjectPath(cwd, ".")
	if err != nil {
		return resp, nil // 越界不报错，返回空结果
	}

	// 收集所有文件与目录
	type scored struct {
		item  FileFuzzyItem
		score float64
	}
	var all []scored
	queryLower := strings.ToLower(query)

	err = filepath.WalkDir(absCwd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过不可读路径
		}
		if path == absCwd {
			return nil
		}
		rel, err := filepath.Rel(absCwd, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		item := FileFuzzyItem{Path: rel}
		if d.IsDir() {
			item.Type = "dir"
		} else {
			item.Type = "file"
		}
		score := fuzzyScore(rel, queryLower)
		if score > 0 {
			all = append(all, scored{item: item, score: score})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("遍历目录失败: %w", err)
	}

	// 按相关度排序
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].item.Path < all[j].item.Path
	})
	if len(all) > limit {
		all = all[:limit]
	}
	for _, s := range all {
		s.item.Score = s.score
		resp.Files = append(resp.Files, s.item)
	}
	return resp, nil
}

// fuzzyScore 简单 fuzzy 评分：精确前缀 > 子串 > 首字母匹配；不匹配返回 0
func fuzzyScore(path, query string) float64 {
	if query == "" {
		return 1.0
	}
	pathLower := strings.ToLower(path)
	base := filepath.Base(pathLower)
	if base == query {
		return 100.0
	}
	if strings.HasPrefix(base, query) {
		return 80.0 + float64(len(query))/float64(len(base))*10.0
	}
	if strings.Contains(base, query) {
		return 60.0 + float64(len(query))/float64(len(base))*10.0
	}
	if strings.HasPrefix(pathLower, query) {
		return 50.0
	}
	if strings.Contains(pathLower, query) {
		return 30.0
	}
	// 首字母匹配
	if matchInitials(base, query) {
		return 20.0
	}
	return 0
}

// matchInitials 检查 query 是否为 path 各单词首字母组合
func matchInitials(path, query string) bool {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '_' || r == '-' || r == '.'
	})
	if len(parts) == 0 {
		return false
	}
	var initials strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			initials.WriteByte(p[0])
		}
	}
	return strings.HasPrefix(initials.String(), query)
}

// ApplyPromptTemplate 按 frontmatter 规则把模板 body 与参数插值
// 支持 $ARGUMENTS、$@、$1..$9、${1:-default}。
func ApplyPromptTemplate(body string, args []string) string {
	// $ARGUMENTS / $@ 替换为空格拼接的所有参数
	all := strings.Join(args, " ")
	body = strings.ReplaceAll(body, "$ARGUMENTS", all)
	body = strings.ReplaceAll(body, "$@", all)

	// $1..$9
	for i := 1; i <= 9; i++ {
		placeholder := fmt.Sprintf("$%d", i)
		var val string
		if i <= len(args) {
			val = args[i-1]
		}
		body = strings.ReplaceAll(body, placeholder, val)
	}

	// ${1:-default} ... ${9:-default}
	for i := 1; i <= 9; i++ {
		prefix := fmt.Sprintf("${%d:-", i)
		idx := 0
		for {
			start := strings.Index(body[idx:], prefix)
			if start < 0 {
				break
			}
			start += idx
			end := strings.Index(body[start:], "}")
			if end < 0 {
				break
			}
			end += start
			defaultVal := body[start+len(prefix) : end]
			var val string
			if i <= len(args) && args[i-1] != "" {
				val = args[i-1]
			} else {
				val = defaultVal
			}
			body = body[:start] + val + body[end+1:]
			idx = start + len(val)
		}
	}
	return body
}

// ValidateSlashCommandName 校验 slash 命令名是否合法（以 / 开头，无空白）
func ValidateSlashCommandName(name string) error {
	if !strings.HasPrefix(name, "/") {
		return errors.New("slash 命令必须以 / 开头")
	}
	if strings.TrimSpace(name) != name {
		return errors.New("slash 命令不能包含前后空白")
	}
	if strings.ContainsAny(name, "\n\r\t") {
		return errors.New("slash 命令不能包含换行或制表符")
	}
	return nil
}
