package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
)

// ToolFunc 工具函数签名
type ToolFunc func(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error)

// ToolEnv 工具执行环境
type ToolEnv struct {
	UserID         string
	AgentID        string // 当前调用的 SKU/AgentItem ID，用于凭证注入
	ConversationID string
	SessionID      string
	Sandbox        *FileSandbox
	// AR-06：claw 本地工作目录（per-session，仅 claw 装配）。非空时文件工具优先解析到 cwd。
	Cwd            string
	SessionRepo    *repository.AgentSessionRepo
	SearchProvider string // 当前 conversation 选择的搜索源，如 baidu / bing
	// Credentials 当前工具声明的凭证字段定义，供驱动校验/注入
	Credentials map[string]model.CredentialDef
	// Agent Team P3：编排深度。0=编排者主循环；>0=子 agent 执行环境。
	// CallAssistant 在 Depth>0 时直接拒绝（结构性限深的防御性兜底）
	Depth int
	// Agent Team P3：本次 execute 的委派计数器（上限 5），由 Execute 装配；子 env 不共享
	DelegateCalls *int
	// Agent Team P3：子调用 token 用量累计钩子，挂到主循环 totalUsage（只经此钩子进账一次）
	UsageAccumulator func(*llm.Usage)
	// AR-03：执行中余额校验回调，循环每轮调用；返回非 nil error 表示余额不足，强制结束循环。
	// 由 AgentService 装配（含节流，避免每轮查 DB）；子 agent env 不注入（计费由主循环统一管）。
	BudgetGuard func() error
	// AR-03：每步成本门控回调（max_cost_per_task）。传入循环累计用量，返回非 nil error 表示超限，强制结束。
	// 由编排器装配（CallAssistant 子任务），用 billingService.EstimateCost 估算；主循环可不注入。
	CostGuard func(usage *llm.Usage) error
	// C1 权限审批：模式 + 决策引擎 + 审批器。Approver 为 nil（云端）时审批闸直接放行。
	// claw（unrestricted）由 Execute 装配；子 agent 编排器透传（子工具也走审批，经 rt.writer 流出）。
	PermissionMode model.PermissionMode
	PermissionSvc  *PermissionService
	Approver       Approver
	// C2 生命周期钩子：PreToolUse/PostToolUse/Stop/PreCompact 可编程层。nil 时跳过。
	HookSvc *HookService
	// C3 plan 模式：当前工具调用 ID（循环每轮注入，供 ExitPlanMode 工具作为审批 key）
	CurrentToolCallID string
	// C3 plan 模式：plan 文件目录（claw 装配 basePath/plans），空则不落 plan 文件
	PlansDir string
}

// ResolveFilePath 解析文件工具路径（AR-06）：env.Cwd 非空时优先解析到 cwd，否则回退会话沙箱。
func (env *ToolEnv) ResolveFilePath(path string) (string, error) {
	if env.Cwd != "" {
		return env.Sandbox.ResolveProjectPath(env.Cwd, path)
	}
	return env.Sandbox.ResolvePath(env.UserID, env.ConversationID, path)
}

// SaveOutput 将工具产物登记为匿名资源
func (env *ToolEnv) SaveOutput(fileName, mimeType, diskPath string, fileSize int64) (string, error) {
	if env.SessionRepo == nil {
		return "", errors.New("SessionRepo 未配置，无法保存资源")
	}
	id := generateID("ar")
	output := &model.AgentSessionOutput{
		ID:         generateID("aso"),
		SessionID:  env.SessionID,
		ResourceID: id,
		FileName:   fileName,
		MimeType:   mimeType,
		FileSize:   fileSize,
		DiskPath:   diskPath,
		CreatedAt:  time.Now().Unix(),
	}
	if err := env.SessionRepo.SaveOutput(output); err != nil {
		return "", err
	}
	return id, nil
}

// Tool 工具定义
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
	Func        ToolFunc
	ServerSide  bool                // 是否需要服务器端权限（VIP）
	Manifest    *model.ToolManifest // 工具标准描述（可选，用于集市化与动态加载）
	Driver      string              // 驱动标识：builtin / agent_reach / remote_url 等
	AgentID     string              // 对应 AgentItem/SKU ID，用于凭证注入
	Credentials map[string]model.CredentialDef
	// ReadOnly C1：工具是否只读（无副作用）。ReadFile/Grep/FetchURL/OCR=true；
	// WriteFile/StrReplaceFile/Shell=false。审批闸据此判定：plan 模式仅放行只读，
	// default/acceptEdits 放行只读而无需审批。动态加载的 module/remote 工具默认 false（保守）。
	ReadOnly bool
}

// ToolRegistry 工具注册表
type ToolRegistry struct {
	tools          map[string]*Tool
	runner         PlatformToolRunner
	searchProvider SearchProvider
	driverRegistry *ToolDriverRegistry
}

// NewToolRegistry 创建工具注册表，使用默认跨平台运行器和搜索提供者
func NewToolRegistry() *ToolRegistry {
	driverRegistry := NewToolDriverRegistry()
	r := &ToolRegistry{
		tools:          make(map[string]*Tool),
		runner:         NewPlatformRunner(),
		searchProvider: NewSearchProvider(),
		driverRegistry: driverRegistry,
	}
	r.registerBuiltinDriver(driverRegistry)
	driverRegistry.Register(newRemoteURLDriver(60))
	r.registerDefaults()
	return r
}

// NewToolRegistryWithDeps 创建工具注册表，可注入自定义运行器和搜索提供者（便于测试）
func NewToolRegistryWithDeps(runner PlatformToolRunner, sp SearchProvider) *ToolRegistry {
	driverRegistry := NewToolDriverRegistry()
	r := &ToolRegistry{
		tools:          make(map[string]*Tool),
		runner:         runner,
		searchProvider: sp,
		driverRegistry: driverRegistry,
	}
	r.registerBuiltinDriver(driverRegistry)
	r.registerDefaults()
	return r
}

// NewToolRegistryWithDriverRegistry 创建工具注册表，可注入驱动注册表（便于动态加载测试）
func NewToolRegistryWithDriverRegistry(runner PlatformToolRunner, sp SearchProvider, driverRegistry *ToolDriverRegistry) *ToolRegistry {
	r := &ToolRegistry{
		tools:          make(map[string]*Tool),
		runner:         runner,
		searchProvider: sp,
		driverRegistry: driverRegistry,
	}
	r.registerDefaults()
	return r
}

// registerBuiltinDriver 注册系统内置驱动
func (r *ToolRegistry) registerBuiltinDriver(registry *ToolDriverRegistry) {
	registry.Register(&builtinToolDriver{registry: r})
}

// Register 注册工具
func (r *ToolRegistry) Register(tool *Tool) {
	r.tools[tool.Name] = tool
}

// Get 获取工具
func (r *ToolRegistry) Get(name string) (*Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// Resolve 归一化后获取工具（AR-20）：精确命中失败时尝试别名映射
// （剥 functions. 前缀、蛇形/小写 -> 注册名），兼容 LLM 输出的变形工具名。
// 返回归一化后的真实工具名；未命中返回原名与 false。
func (r *ToolRegistry) Resolve(name string) (*Tool, string, bool) {
	if t, ok := r.tools[name]; ok {
		return t, name, true
	}
	norm := normalizeToolName(name)
	if norm != name {
		if t, ok := r.tools[norm]; ok {
			return t, norm, true
		}
	}
	return nil, name, false
}

// toolNameAliases 工具名别名映射（蛇形/小写 -> 注册名），供 normalizeToolName 查找。
// 新增工具时须同步本表。
var toolNameAliases = map[string]string{
	"write_file":       "WriteFile",
	"read_file":        "ReadFile",
	"str_replace_file": "StrReplaceFile",
	"grep":             "Grep",
	"shell":            "Shell",
	"ocr":              "OCR",
	"fetch_url":        "FetchURL",
	"search_web":       "SearchWeb",
}

// normalizeToolName 归一化 LLM 输出的工具名（借鉴 jcode resolve_tool_name）：
//   - 剥离 "functions." 前缀（部分 OpenAI 兼容路由退化输出 to=functions.NAME）
//   - 别名表查找（蛇形/小写 -> 注册驼峰名，大小写不敏感）
//
// 未命中别名表时原样返回（交由 registry.Get 精确匹配或走未知工具分支）。
func normalizeToolName(name string) string {
	name = strings.TrimSpace(name)
	for strings.HasPrefix(name, "functions.") {
		name = strings.TrimPrefix(name, "functions.")
	}
	if mapped, ok := toolNameAliases[strings.ToLower(name)]; ok {
		return mapped
	}
	return name
}

// List 列出所有工具
func (r *ToolRegistry) List() []*Tool {
	items := make([]*Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		items = append(items, tool)
	}
	return items
}

// ListAvailable 根据用户权限列出可用工具
func (r *ToolRegistry) ListAvailable(isVIP bool) []*Tool {
	var items []*Tool
	for _, tool := range r.tools {
		if tool.ServerSide && !isVIP {
			continue
		}
		items = append(items, tool)
	}
	return items
}

// DriverRegistry 返回驱动注册表
func (r *ToolRegistry) DriverRegistry() *ToolDriverRegistry {
	return r.driverRegistry
}

// Clone 深度克隆工具注册表（用于会话级动态工具注入）
func (r *ToolRegistry) Clone() *ToolRegistry {
	cloned := &ToolRegistry{
		tools:          make(map[string]*Tool, len(r.tools)),
		runner:         r.runner,
		searchProvider: r.searchProvider,
		driverRegistry: r.driverRegistry,
	}
	for name, tool := range r.tools {
		cloned.tools[name] = tool
	}
	return cloned
}

// RegisterBuiltinSearchWeb 注册内置 SearchWeb 工具（读 env BAIDU_API_KEY/BING_SEARCH_API_KEY）。
// 仅云端调用：云端用内置搜索实现；claw 由外置 search-web 模块（千帆/Bing SKU）提供搜索，不注册此项，
// 避免 LLM 感知到无凭证的内置工具而报「搜索服务未配置」。
func (r *ToolRegistry) RegisterBuiltinSearchWeb() {
	r.Register(&Tool{
		Name:        "SearchWeb",
		Description: "联网搜索，获取实时信息。国内云服务器推荐配置 baidu（每日 100 次免费额度）或 bing",
		ServerSide:  false,
		ReadOnly:    true,
		Driver:      string(model.ToolDriverBuiltin),
		Manifest: &model.ToolManifest{
			ID:          "com.eleball.tools.search_web",
			Name:        "联网搜索",
			Description: "联网搜索，获取实时信息。",
			Driver:      model.ToolDriverBuiltin,
			Category:    "搜索",
			Level:       1,
			Permissions: []model.ToolPermission{},
			Actions:     []model.ToolAction{{Name: "search", Description: "执行搜索", Params: map[string]string{"query": "query"}}},
		},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "搜索关键词",
				},
			},
			"required": []string{"query"},
		},
		Func: r.toolSearchWeb,
	})
}

// registerDefaults 注册默认工具（不含 SearchWeb：云端显式注册，claw 用外置 search-web 模块）
func (r *ToolRegistry) registerDefaults() {
	r.Register(&Tool{
		Name:        "FetchURL",
		Description: "抓取指定网页的正文内容，用于深度阅读搜索结果页面",
		ServerSide:  false,
		ReadOnly:    true,
		Driver:      string(model.ToolDriverBuiltin),
		Manifest: &model.ToolManifest{
			ID:          "com.eleball.tools.fetch_url",
			Name:        "网页抓取",
			Description: "抓取指定网页的正文内容。",
			Driver:      model.ToolDriverBuiltin,
			Category:    "搜索",
			Level:       1,
			Permissions: []model.ToolPermission{model.ToolPermissionNetwork},
			Actions:     []model.ToolAction{{Name: "fetch", Description: "抓取网页", Params: map[string]string{"url": "url"}}},
		},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "需要抓取的网页 URL",
				},
			},
			"required": []string{"url"},
		},
		Func: r.toolFetchURL,
	})

	r.Register(&Tool{
		Name:        "ReadFile",
		Description: "读取用户 conversation 目录或公共知识库中的文件内容",
		ServerSide:  true,
		ReadOnly:    true,
		Driver:      string(model.ToolDriverBuiltin),
		Manifest: &model.ToolManifest{
			ID:          "com.eleball.tools.read_file",
			Name:        "读取文件",
			Description: "读取用户 conversation 目录或公共知识库中的文件内容。",
			Driver:      model.ToolDriverBuiltin,
			Category:    "文件",
			Level:       1,
			Permissions: []model.ToolPermission{model.ToolPermissionFileTools},
			Actions:     []model.ToolAction{{Name: "read", Description: "读取文件", Params: map[string]string{"path": "path"}}},
		},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "相对于当前工作目录的文件路径，可用绝对路径",
				},
			},
			"required": []string{"path"},
		},
		Func: r.toolReadFile,
	})

	r.Register(&Tool{
		Name:        "WriteFile",
		Description: "在用户 conversation 目录中写入或覆盖文件",
		ServerSide:  true,
		ReadOnly:    false,
		Driver:      string(model.ToolDriverBuiltin),
		Manifest: &model.ToolManifest{
			ID:          "com.eleball.tools.write_file",
			Name:        "写入文件",
			Description: "在用户 conversation 目录中写入或覆盖文件。",
			Driver:      model.ToolDriverBuiltin,
			Category:    "文件",
			Level:       1,
			Permissions: []model.ToolPermission{model.ToolPermissionFileTools},
			Actions:     []model.ToolAction{{Name: "write", Description: "写入文件", Params: map[string]string{"path": "path", "content": "content"}}},
		},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "相对于当前工作目录的文件路径，可用绝对路径",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "文件内容",
				},
			},
			"required": []string{"path", "content"},
		},
		Func: r.toolWriteFile,
	})

	r.Register(&Tool{
		Name:        "StrReplaceFile",
		Description: "修改用户 conversation 目录中的文件内容",
		ServerSide:  true,
		ReadOnly:    false,
		Driver:      string(model.ToolDriverBuiltin),
		Manifest: &model.ToolManifest{
			ID:          "com.eleball.tools.str_replace_file",
			Name:        "替换文件内容",
			Description: "修改用户 conversation 目录中的文件内容。",
			Driver:      model.ToolDriverBuiltin,
			Category:    "文件",
			Level:       1,
			Permissions: []model.ToolPermission{model.ToolPermissionFileTools},
			Actions:     []model.ToolAction{{Name: "replace", Description: "替换文件内容", Params: map[string]string{"path": "path", "old_string": "old_string", "new_string": "new_string"}}},
		},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "相对于当前工作目录的文件路径，可用绝对路径",
				},
				"old_string": map[string]interface{}{
					"type":        "string",
					"description": "需要替换的原始字符串",
				},
				"new_string": map[string]interface{}{
					"type":        "string",
					"description": "替换后的新字符串",
				},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
		Func: r.toolStrReplaceFile,
	})

	r.Register(&Tool{
		Name:        "Grep",
		Description: "在沙箱内搜索文件内容。仅允许访问当前用户的 conversation/session 目录和公共知识库目录",
		ServerSide:  true,
		ReadOnly:    true,
		Driver:      string(model.ToolDriverBuiltin),
		Manifest: &model.ToolManifest{
			ID:          "com.eleball.tools.grep",
			Name:        "文件搜索",
			Description: "在沙箱内搜索文件内容。",
			Driver:      model.ToolDriverBuiltin,
			Category:    "文件",
			Level:       1,
			Permissions: []model.ToolPermission{model.ToolPermissionFileTools},
			Actions:     []model.ToolAction{{Name: "grep", Description: "搜索文件内容", Params: map[string]string{"path": "path", "pattern": "pattern", "recursive": "recursive"}}},
		},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "相对于当前工作目录的文件或目录路径，可用绝对路径",
				},
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "搜索模式（支持基本正则）",
				},
				"recursive": map[string]interface{}{
					"type":        "boolean",
					"description": "是否递归搜索目录，path 为目录时自动递归",
				},
			},
			"required": []string{"path", "pattern"},
		},
		Func: r.toolGrep,
	})

	r.Register(&Tool{
		Name:        "Shell",
		Description: "在服务器执行受限 shell 命令。格式要求：command 只填主命令（单个词，不含空格），参数必须放入 args 数组，不要把整行命令塞进 command。仅允许白名单命令（ls/cat/pwd/echo/head/tail/wc/grep/find/sort/uniq/cut/python3/pip3/node/which/date 等只读命令）；不支持管道 |、重定向 >/<、多命令（&&/||/;）、内联执行（-c/-e）。示例：{\"command\":\"grep\",\"args\":[\"-rn\",\"关键词\",\".\"]}",
		ServerSide:  true,
		ReadOnly:    false,
		Driver:      string(model.ToolDriverBuiltin),
		Manifest: &model.ToolManifest{
			ID:          "com.eleball.tools.shell",
			Name:        "受限 Shell",
			Description: "在服务器执行受限 shell 命令。",
			Driver:      model.ToolDriverBuiltin,
			Category:    "系统",
			Level:       2,
			Permissions: []model.ToolPermission{model.ToolPermissionFileTools, model.ToolPermissionShell},
			Actions:     []model.ToolAction{{Name: "shell", Description: "执行受限 shell 命令", Params: map[string]string{"command": "command", "args": "args"}}},
		},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "主命令（单个词，不含空格与参数），如 ls、grep、python3",
				},
				"args": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "命令参数数组，每个元素一个参数，如 [\"-rn\",\"关键词\",\".\"]；不要拼接成单个字符串",
				},
			},
			"required": []string{"command"},
		},
		Func: r.toolShell,
	})

	r.Register(&Tool{
		Name:        "OCR",
		Description: "识别图片中的文字。Ubuntu 24.04 需安装 tesseract-ocr；Windows 需安装 tesseract 并加入 PATH",
		ServerSide:  true,
		ReadOnly:    true,
		Driver:      string(model.ToolDriverBuiltin),
		Manifest: &model.ToolManifest{
			ID:          "com.eleball.tools.ocr",
			Name:        "图片文字识别",
			Description: "识别图片中的文字。",
			Driver:      model.ToolDriverBuiltin,
			Category:    "多媒体",
			Level:       2,
			Permissions: []model.ToolPermission{model.ToolPermissionFileTools},
			Actions:     []model.ToolAction{{Name: "ocr", Description: "识别图片文字", Params: map[string]string{"path": "path"}}},
		},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "相对于当前工作目录的图片路径，可用绝对路径",
				},
			},
			"required": []string{"path"},
		},
		Func: r.toolOCR,
	})

	// VideoGenerate 已移除：当前实现仅为 ffmpeg 占位视频，非真正 AI 视频生成，
	// 避免误导用户。后续接入 Seedance / CogVideo / 可灵等真实文生视频 API 后再恢复。

	// C3 plan 模式：ExitPlanMode 提交研究计划供用户审批。ReadOnly=true（仅写 .claw/plans/ 受管目录，
	// 不改用户工作区），故 plan 模式下可调用；ServerSide=true 与其他内置 agent 工具一致（claw 无 VIP 门控）；
	// schema 构建时仅 plan 模式暴露给 LLM。
	r.Register(&Tool{
		Name:        "ExitPlanMode",
		Description: "提交研究/实施计划供用户审批。仅在 plan（计划）模式下调用：完成只读研究后，把计划写成结构化 markdown 经此工具提交，调用后会暂停等待用户接受/拒绝/细化。接受后会话切到 acceptEdits 模式开始执行。",
		ServerSide:  true,
		ReadOnly:    true,
		Driver:      string(model.ToolDriverBuiltin),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"goal": map[string]interface{}{
					"type":        "string",
					"description": "本次计划要达成的用户目标（一句话）",
				},
				"plan": map[string]interface{}{
					"type":        "string",
					"description": "计划正文（markdown）：含实施步骤、涉及文件/命令、风险与验收标准",
				},
			},
			"required": []string{"goal", "plan"},
		},
		Func: r.toolExitPlanMode,
	})
}

// toolSearchWeb 搜索工具
func (r *ToolRegistry) toolSearchWeb(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	query, _ := input["query"].(string)
	if query == "" {
		return nil, errors.New("query 不能为空")
	}
	// 优先使用 conversation 选择的搜索源；未指定或指定的源不可用时，动态回退到第一个可用源
	providerName := ""
	if env != nil && env.SearchProvider != "" {
		providerName = env.SearchProvider
	}
	if providerName == "" || !IsSearchProviderAvailable(providerName) {
		providerName = GetFirstAvailableSearchProvider()
	}
	if providerName == "" {
		return r.searchProvider.Search(ctx, query)
	}
	return GetSearchProvider(providerName).Search(ctx, query)
}

// toolFetchURL 抓取网页正文工具
func (r *ToolRegistry) toolFetchURL(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	urlStr, _ := input["url"].(string)
	if urlStr == "" {
		return nil, errors.New("url 不能为空")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("网页返回 %d", resp.StatusCode)
	}

	// 限制读取长度，避免 token 爆炸
	const maxBytes = 256 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, err
	}

	title := extractTitle(string(body))
	text := stripHTMLTags(string(body))
	// 进一步压缩空白字符
	re := regexp.MustCompile(`\s+`)
	text = re.ReplaceAllString(text, " ")
	// 限制返回字符数
	const maxChars = 8000
	if len(text) > maxChars {
		text = text[:maxChars] + "\n[内容已截断]"
	}

	return map[string]interface{}{
		"url":   urlStr,
		"title": title,
		"text":  text,
	}, nil
}

// extractTitle 从 HTML 中提取 <title> 内容
func extractTitle(html string) string {
	re := regexp.MustCompile(`(?i)<title[^>]*>([^<]*)</title>`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// toolReadFile 读文件工具
func (r *ToolRegistry) toolReadFile(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	path, _ := input["path"].(string)
	absPath, err := env.ResolveFilePath(path)
	if err != nil {
		return nil, err
	}
	data, err := env.Sandbox.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"path":     path,
		"abs_path": absPath,
		"content":  string(data),
	}, nil
}

// toolWriteFile 写文件工具
func (r *ToolRegistry) toolWriteFile(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	path, _ := input["path"].(string)
	content, _ := input["content"].(string)
	if path == "" {
		return nil, errors.New("path 不能为空")
	}
	absPath, err := env.ResolveFilePath(path)
	if err != nil {
		return nil, err
	}
	// AR-06 写审计：读旧内容（存在则）用于生成 diff
	oldContent := ""
	if oldData, rErr := env.Sandbox.ReadFile(absPath); rErr == nil {
		oldContent = string(oldData)
	}
	if err := env.Sandbox.WriteFile(absPath, []byte(content)); err != nil {
		return nil, err
	}
	// AR-06 写审计：追加 unified diff 到 session metadata.json（失败不阻断）
	_ = env.Sandbox.AppendWriteAudit(env.UserID, env.SessionID, "WriteFile", path, oldContent, content)

	fileName := filepath.Base(absPath)
	mimeType := mime.TypeByExtension(filepath.Ext(absPath))
	var fileSize int64
	if fi, err := os.Stat(absPath); err == nil {
		fileSize = fi.Size()
	}
	resourceID, err := env.SaveOutput(fileName, mimeType, absPath, fileSize)
	if err != nil {
		// 资源登记失败不影响工具结果，但记录日志以便排查
		return map[string]interface{}{
			"path":     path,
			"abs_path": absPath,
			"written":  true,
			"error":    fmt.Sprintf("登记下载资源失败: %v", err),
		}, nil
	}

	return map[string]interface{}{
		"path":         path,
		"abs_path":     absPath,
		"written":      true,
		"resource_id":  resourceID,
		"mime_type":    mimeType,
		"download_url": fmt.Sprintf("/v1/agent/resources/%s", resourceID),
	}, nil
}

// toolStrReplaceFile 修改文件工具
func (r *ToolRegistry) toolStrReplaceFile(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	path, _ := input["path"].(string)
	oldString, _ := input["old_string"].(string)
	newString, _ := input["new_string"].(string)
	if oldString == "" {
		return nil, errors.New("old_string 不能为空")
	}

	absPath, err := env.ResolveFilePath(path)
	if err != nil {
		return nil, err
	}
	data, err := env.Sandbox.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	content := strings.ReplaceAll(string(data), oldString, newString)
	if err := env.Sandbox.WriteFile(absPath, []byte(content)); err != nil {
		return nil, err
	}
	// AR-06 写审计：追加 old->new unified diff 到 session metadata.json（失败不阻断）
	_ = env.Sandbox.AppendWriteAudit(env.UserID, env.SessionID, "StrReplaceFile", path, string(data), content)

	fileName := filepath.Base(absPath)
	mimeType := mime.TypeByExtension(filepath.Ext(absPath))
	var fileSize int64
	if fi, err := os.Stat(absPath); err == nil {
		fileSize = fi.Size()
	}
	resourceID, err := env.SaveOutput(fileName, mimeType, absPath, fileSize)
	if err != nil {
		// 文件已改成功（modified:true）；SaveOutput 仅登记下载资源，属非关键 infra，
		// 失败不应让模型误以为编辑失败 -> 降级为 warning 而非 error（防误导模型/判官）。
		return map[string]interface{}{
			"path":        path,
			"abs_path":    absPath,
			"modified":    true,
			"resource_id": "",
			"warning":     fmt.Sprintf("下载资源登记失败（不影响文件修改）: %v", err),
		}, nil
	}

	return map[string]interface{}{
		"path":         path,
		"abs_path":     absPath,
		"modified":     true,
		"resource_id":  resourceID,
		"mime_type":    mimeType,
		"download_url": fmt.Sprintf("/v1/agent/resources/%s", resourceID),
	}, nil
}

// toolGrep 在沙箱内搜索文件内容
// 仅允许访问当前用户 conversation/session 目录和公共知识库目录。
// 实现为 Go 内置正则搜索（跨平台，不依赖外部 grep 命令，Windows 部署可用）。
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

	// 安全检查：toolGrep 是纯 Go RE2 正则搜索（不经过 shell，pattern 只传入
	// regexp.Compile 与 searchPattern 的 *regexp.Regexp），因此允许正则元字符
	// （| ( ) $ ^ . * 等，如 "claw|Claw|CLAW" 或 "OpenClaw.*工具列表|25种Tools"）。
	// 仅拒绝空字节这类无意义的注入指示符；语法合法性由下方 regexp.Compile 校验，
	// RE2 保证线性时间，无 ReDoS 风险。
	if strings.ContainsRune(pattern, 0) {
		return nil, errors.New("pattern 包含非法字符: 空字节")
	}

	recursive := false
	if rec, ok := input["recursive"].(bool); ok {
		recursive = rec
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("正则表达式非法: %w", err)
	}

	matches, err := searchPattern(ctx, absPath, re, recursive, false)
	if err != nil {
		return nil, fmt.Errorf("grep 执行失败: %w", err)
	}

	// 与 GNU grep 输出习惯对齐：单文件输出 "行号:内容"，目录搜索输出 "路径:行号:内容"
	singleFile := true
	if info, statErr := os.Stat(absPath); statErr == nil && info.IsDir() {
		singleFile = false
	}
	return map[string]interface{}{
		"path":      path,
		"abs_path":  absPath,
		"pattern":   pattern,
		"matches":   formatGrepMatches(matches, singleFile),
		"truncated": len(matches) >= grepMaxMatches,
	}, nil
}

// toolShell 受限 shell 工具
func (r *ToolRegistry) toolShell(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	command, _ := input["command"].(string)
	if command == "" {
		return nil, errors.New("command 不能为空")
	}
	if err := shellSafe(command); err != nil {
		return nil, fmt.Errorf("%w。%s", err, shellUsageHint)
	}

	var args []string
	if rawArgs, ok := input["args"].([]interface{}); ok {
		for _, a := range rawArgs {
			if s, ok := a.(string); ok {
				if err := shellSafe(s); err != nil {
					return nil, fmt.Errorf("参数包含非法字符: %w。%s", err, shellUsageHint)
				}
				args = append(args, s)
			}
		}
	}

	output, err := r.runner.Shell(ctx, command, args, env.Cwd)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"command": command,
		"args":    args,
		"output":  output,
	}, nil
}

// toolOCR OCR 工具
func (r *ToolRegistry) toolOCR(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	path, _ := input["path"].(string)
	absPath, err := env.ResolveFilePath(path)
	if err != nil {
		return nil, err
	}
	text, err := r.runner.OCR(ctx, absPath)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"path": path,
		"text": text,
	}, nil
}

// OCRDataURI C7 多模态降级：对 data URI 形式的图片执行 OCR。
// 解码 data URI -> 落临时文件（按 MIME 取扩展名）-> 复用 runner.OCR（tesseract）-> 清理。
// 用于非视觉模型收到图片附件时把图片转为文本注入对话，避免图片被丢弃。
func (r *ToolRegistry) OCRDataURI(ctx context.Context, dataURI string) (string, error) {
	data, mimeType, err := decodeDataURI(dataURI)
	if err != nil {
		return "", fmt.Errorf("解析图片 data URI 失败: %w", err)
	}
	ext := ".png"
	if exts, _ := mime.ExtensionsByType(mimeType); len(exts) > 0 {
		ext = exts[0]
	}
	tmpFile, err := os.CreateTemp("", "claw-ocr-*"+ext)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("写入临时文件失败: %w", err)
	}
	tmpFile.Close()
	text, err := r.runner.OCR(ctx, tmpPath)
	if err != nil {
		return "", err
	}
	return text, nil
}

// toolExitPlanMode C3：提交 plan 供用户审批。写 plan 文件到 .claw/plans/ + 发 plan_review 阻塞等决策。
// 决策通过 POST /agent/plan-review 投递（复用 approvalRegistry）；结果回灌 LLM 指导后续行为。
func (r *ToolRegistry) toolExitPlanMode(ctx context.Context, input map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	if env == nil || env.Approver == nil {
		return nil, errors.New("plan 审批当前不可用（需 claw 本地环境）")
	}
	goal, _ := input["goal"].(string)
	plan, _ := input["plan"].(string)
	if strings.TrimSpace(plan) == "" {
		return nil, errors.New("plan 不能为空")
	}
	planPath := ""
	if env.PlansDir != "" {
		slug := planSlug(goal)
		planPath = filepath.Join(env.PlansDir, slug+".md")
		if err := os.MkdirAll(env.PlansDir, 0o755); err == nil {
			_ = os.WriteFile(planPath, []byte(formatPlanFile(goal, plan)), 0o644)
		} else {
			planPath = "" // 目录创建失败则仅传内容，不引用路径
		}
	}
	dec, err := env.Approver.RequestPlanReview(ctx, PlanReviewRequest{
		SessionID:   env.SessionID,
		ToolCallID:  env.CurrentToolCallID,
		PlanPath:    planPath,
		PlanContent: plan,
		Goal:        goal,
	})
	if err != nil {
		return map[string]interface{}{"status": "error", "message": err.Error()}, nil
	}
	msg := "plan 已被用户拒绝"
	switch dec.Decision {
	case "accepted":
		msg = "plan 已被用户接受，会话已切到 acceptEdits 模式，请按计划执行"
	case "refined":
		fb := dec.Reason
		if fb == "" {
			fb = "（未给出反馈）"
		}
		msg = "用户要求细化 plan，反馈：" + fb
	case "rejected":
		if dec.Reason != "" {
			msg += "（" + dec.Reason + "）"
		}
	}
	return map[string]interface{}{"status": dec.Decision, "message": msg, "plan_path": planPath}, nil
}

// planSlug 从目标生成 plan 文件名 slug（小写字母数字 + 短时间后缀防冲突）。
func planSlug(goal string) string {
	s := strings.ToLower(goal)
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '/':
			b.WriteRune('-')
		}
	}
	slug := b.String()
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "plan"
	}
	if len(slug) > 32 {
		slug = slug[:32]
	}
	return slug + "-" + randSuffix(6)
}

// formatPlanFile 生成 plan 文件 markdown 内容。
func formatPlanFile(goal, plan string) string {
	return fmt.Sprintf("# 计划：%s\n\n> 由 claw plan 模式生成\n\n%s\n", goal, plan)
}

// randSuffix 生成 n 位十六进制时间后缀（防 plan 文件名冲突）。
func randSuffix(n int) string {
	s := fmt.Sprintf("%x", time.Now().UnixNano())
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
