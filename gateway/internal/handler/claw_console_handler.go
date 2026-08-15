package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/seed"
	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ClawConsoleHandler claw 本地控制台处理器（P3 细化）。
//
// 提供本地 token 用量统计等本地控制台端点（替代云端 admin 的 DAU/收入统计）。
// claw 本地不计费，但记录 token 用量用于本地观察；不展示平台级数据。
type ClawConsoleHandler struct {
	db                   *gorm.DB
	mcpStdio             *service.MCPStdioProtocol     // stdio MCP 一次性探测
	processWorkDirs      []string                      // process 沙箱允许的工作目录前缀（探测时校验）
	moduleService        *service.ModuleService        // /mcp/generate 写模块 + rescan + autostart
	interpreterBootstrap *service.InterpreterBootstrap // H1 托管解释器安装（python-build-standalone）
	dshPluginSvc         *service.DSHPluginService     // F4：DSH 插件（npm 包）预览/导入
	dshBridgeSvc         *service.DSHBridgeService     // T5：DSH 桥接运行时导入（dsh-mcp-bridge）
	skillSyncFn          func(dir, skillID, creatorID, creatorName string) (int, int, int) // F4：SKU 定向同步（main 适配 seed）
}

// NewClawConsoleHandler 创建 claw 控制台处理器
func NewClawConsoleHandler(db *gorm.DB) *ClawConsoleHandler {
	return &ClawConsoleHandler{db: db}
}

// SetMCPStdioProtocol 注入 stdio MCP 协议实例（供 /mcp/probe 探测 stdio server）
func (h *ClawConsoleHandler) SetMCPStdioProtocol(p *service.MCPStdioProtocol) {
	h.mcpStdio = p
}

// SetProcessSandboxWorkDirs 注入 process 沙箱允许的工作目录前缀（探测时校验 work_dir）
func (h *ClawConsoleHandler) SetProcessSandboxWorkDirs(dirs []string) {
	h.processWorkDirs = dirs
}

// SetModuleService 注入模块服务（供 /mcp/generate 写模块 + rescan + autostart）
func (h *ClawConsoleHandler) SetModuleService(s *service.ModuleService) {
	h.moduleService = s
}

// SetInterpreterBootstrap 注入托管解释器引导器（H1，供 /tools/install-interpreter）
func (h *ClawConsoleHandler) SetInterpreterBootstrap(b *service.InterpreterBootstrap) {
	h.interpreterBootstrap = b
}

// SetDSHPluginService 注入 DSH 插件导入服务（F4，/dsh-plugin/preview|import）
func (h *ClawConsoleHandler) SetDSHPluginService(s *service.DSHPluginService) {
	h.dshPluginSvc = s
}

// SetDSHBridgeService 注入 DSH 桥接服务（T5，/dsh/bridge-status|import-runtime）
func (h *ClawConsoleHandler) SetDSHBridgeService(s *service.DSHBridgeService) {
	h.dshBridgeSvc = s
}

// SetSkillSyncFn 注入 prompt 秘技 SKU 定向同步函数（F4，main 侧适配 seed.SyncPromptSkillDir）
func (h *ClawConsoleHandler) SetSkillSyncFn(fn func(dir, skillID, creatorID, creatorName string) (int, int, int)) {
	h.skillSyncFn = fn
}

// dshPluginPreviewRequest /v1/claw-console/dsh-plugin/preview 请求体
type dshPluginPreviewRequest struct {
	Package string `json:"package"` // npm 包名（可带 @version / @scope）
}

// PreviewDSHPlugin 预览 DSH 插件（npm 包）可导入内容（F4）。
// POST /v1/claw-console/dsh-plugin/preview：拉 tarball 扫描 SKILL.md + mcpServers，不写盘。
func (h *ClawConsoleHandler) PreviewDSHPlugin(c *gin.Context) {
	var req dshPluginPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Package) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "package 不能为空（npm 包名，可带 @version）"})
		return
	}
	if h.dshPluginSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "DSH 插件服务未初始化"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	preview, err := h.dshPluginSvc.Preview(ctx, req.Package)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": preview})
}

// dshPluginImportRequest /v1/claw-console/dsh-plugin/import 请求体
type dshPluginImportRequest struct {
	Package    string   `json:"package"`
	Skills     []string `json:"skills"`      // 选中的包内 SKILL.md 路径；空=全部
	MCPServers []string `json:"mcp_servers"` // 选中的 MCP server 名；空=不导入
}

// ImportDSHPlugin 导入 DSH 插件选中项（F4）。
// POST /v1/claw-console/dsh-plugin/import：SKILL.md → 秘技包落盘+SKU 同步；mcpServers → 探测安装。
func (h *ClawConsoleHandler) ImportDSHPlugin(c *gin.Context) {
	var req dshPluginImportRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Package) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "package 不能为空"})
		return
	}
	if h.dshPluginSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "DSH 插件服务未初始化"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()
	result, err := h.dshPluginSvc.Import(ctx, req.Package, req.Skills, req.MCPServers, c.GetString("user_id"), h.skillSyncFn)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error(), "data": result})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": result})
}

// tokenUsageStats 本地 token 用量聚合
type tokenUsageStats struct {
	TotalCalls        int64 `json:"total_calls"`
	TodayCalls        int64 `json:"today_calls"`
	TotalInputTokens  int64 `json:"total_input_tokens"`
	TotalOutputTokens int64 `json:"total_output_tokens"`
}

// modelUsage 单模型用量
type modelUsage struct {
	ModelID      string `json:"model_id"`
	Provider     string `json:"provider"`
	Calls        int64  `json:"calls"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// GetStats 本地 token 用量统计（P3 细化，plan §D.2）。
// GET /v1/claw-console/stats
func (h *ClawConsoleHandler) GetStats(c *gin.Context) {
	var stats tokenUsageStats
	h.db.Table("token_usages").
		Select("COUNT(*) as total_calls, COALESCE(SUM(input_tokens),0) as total_input_tokens, COALESCE(SUM(output_tokens),0) as total_output_tokens").
		Scan(&stats)
	// 今日调用（本地时区当天 00:00 起）
	h.db.Table("token_usages").
		Where("created_at >= date('now','localtime')").
		Count(&stats.TodayCalls)

	var models []modelUsage
	h.db.Table("token_usages").
		Select("model_id, provider, COUNT(*) as calls, COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens").
		Group("model_id, provider").
		Order("calls DESC").
		Limit(10).
		Scan(&models)

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"usage":  stats,
			"models": models,
		},
	})
}

// mcpProbeRequest /v1/claw-console/mcp/probe 请求体
type mcpProbeRequest struct {
	Transport string            `json:"transport"` // mcp_stdio（默认） | mcp_http
	Command   string            `json:"command"`   // stdio 启动命令
	Args      []string          `json:"args"`      // stdio 参数
	Env       map[string]string `json:"env"`       // stdio 环境变量
	WorkDir   string            `json:"work_dir"`  // stdio 工作目录
	Endpoint  string            `json:"endpoint"`  // http MCP 地址
	Headers   map[string]string `json:"headers"`   // http MCP 请求头
}

// ProbeMCP 一次性探测候选 MCP server：initialize + tools/list -> 返回工具 schema。
// 供 skill-maker 自动生成 module.json + N SKU。stdio 临时 spawn 后即关闭，http 直接探测。
// POST /v1/claw-console/mcp/probe
func (h *ClawConsoleHandler) ProbeMCP(c *gin.Context) {
	var req mcpProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "请求参数错误: " + err.Error()})
		return
	}
	if req.Transport == "" {
		req.Transport = "mcp_stdio"
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	var tools []service.MCPTool
	var err error
	switch req.Transport {
	case "mcp_stdio":
		if h.mcpStdio == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "stdio MCP 协议未初始化"})
			return
		}
		if vErr := h.validateProbeWorkDir(req.WorkDir); vErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": vErr.Error()})
			return
		}
		tools, err = h.mcpStdio.ProbeStdio(ctx, req.Command, req.Args, req.Env, req.WorkDir)
	case "mcp_http":
		if req.Endpoint == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "endpoint 不能为空"})
			return
		}
		httpProto := service.NewMCPHTTPProtocol(nil)
		tools, err = httpProto.ListTools(ctx, req.Endpoint, req.Headers)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "不支持的 transport: " + req.Transport})
		return
	}

	if err != nil {
		// D3：解释器缺失返回结构化错误，供 web 展示安装引导按钮。
		var ime *service.InterpreterMissingError
		if errors.As(err, &ime) {
			c.JSON(http.StatusOK, gin.H{
				"code":    2002,
				"message": ime.Error(),
				"data": gin.H{
					"error_code":  "interpreter_missing",
					"interpreter": ime.Command,
					"hint":        ime.Hint,
				},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": "MCP 探测失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"tools": tools},
	})
}

// mcpInstallRequest /v1/claw-console/mcp/install 请求体（G3 动态安装远端 MCP）。
// 嵌入 mcpProbeRequest 复用 stdio/http 探测字段，增 Name/Description。
type mcpInstallRequest struct {
	mcpProbeRequest
	Name        string `json:"name"` // 必填，运行时名称 + ID 派生
	Description string `json:"description"`
}

// mcpInstallOutcome 单个 server 探测+安装结果（M4 批量导入返回项）。
// OK=true 时 Result 非 nil；OK=false 时 ErrorCode/Message 描述失败（interpreter_missing 带 Interpreter/Hint）。
type mcpInstallOutcome struct {
	Name        string                    `json:"name"`
	Transport   string                    `json:"transport"`
	OK          bool                      `json:"ok"`
	Result      *service.MCPInstallResult `json:"result,omitempty"`
	ErrorCode   string                    `json:"error_code,omitempty"`
	Message     string                    `json:"message,omitempty"`
	Interpreter string                    `json:"interpreter,omitempty"`
	Hint        string                    `json:"hint,omitempty"`
}

// probeAndInstallMCP 探测 + 安装单个 MCP server（InstallMCP 与 ImportMCPConfig 共用，G3）。
// 复用 ProbeMCP 的 stdio/http 探测逻辑校验 server 可用并拿工具列表，
// -> moduleService.InstallMCPRuntime 创建 source=mcp_remote 的 SkillRuntime 并派生 SKU。
// 成功时 fail=nil；失败时 result=nil，fail 描述原因（interpreter_missing 带 Interpreter/Hint）。
func (h *ClawConsoleHandler) probeAndInstallMCP(ctx context.Context, req mcpInstallRequest) (*service.MCPInstallResult, *mcpInstallOutcome) {
	if req.Transport == "" {
		req.Transport = "mcp_stdio"
	}
	fail := func(code, msg string) *mcpInstallOutcome {
		return &mcpInstallOutcome{Name: req.Name, Transport: req.Transport, ErrorCode: code, Message: msg}
	}

	var tools []service.MCPTool
	var err error
	switch req.Transport {
	case "mcp_stdio":
		if h.mcpStdio == nil {
			return nil, fail("probe_failed", "stdio MCP 协议未初始化")
		}
		if vErr := h.validateProbeWorkDir(req.WorkDir); vErr != nil {
			return nil, fail("probe_failed", vErr.Error())
		}
		tools, err = h.mcpStdio.ProbeStdio(ctx, req.Command, req.Args, req.Env, req.WorkDir)
	case "mcp_http":
		if req.Endpoint == "" {
			return nil, fail("probe_failed", "endpoint 不能为空")
		}
		tools, err = service.NewMCPHTTPProtocol(nil).ListTools(ctx, req.Endpoint, req.Headers)
	default:
		return nil, fail("probe_failed", "不支持的 transport: "+req.Transport)
	}
	if err != nil {
		// D3：解释器缺失返回结构化信息，供 web 展示安装引导按钮。
		var ime *service.InterpreterMissingError
		if errors.As(err, &ime) {
			oc := fail("interpreter_missing", ime.Error())
			oc.Interpreter = ime.Command
			oc.Hint = ime.Hint
			return nil, oc
		}
		return nil, fail("probe_failed", "MCP 探测失败: "+err.Error())
	}
	if len(tools) == 0 {
		return nil, fail("empty_tools", "探测到的工具为空，无法安装")
	}
	installReq := &service.MCPInstallRequest{
		Transport:   req.Transport,
		Name:        req.Name,
		Description: req.Description,
		Command:     req.Command,
		Args:        req.Args,
		Env:         req.Env,
		WorkDir:     req.WorkDir,
		Endpoint:    req.Endpoint,
		Headers:     req.Headers,
	}
	result, err := h.moduleService.InstallMCPRuntime(installReq, tools)
	if err != nil {
		return nil, fail("install_failed", "安装失败: "+err.Error())
	}
	return result, nil
}

// InstallMCP 动态安装远端 MCP server（G3，Smithery 式）。
// 先探测（复用 ProbeMCP 的 stdio/http 探测逻辑，校验 server 可用并拿到工具列表）
// -> moduleService.InstallMCPRuntime 创建 source=mcp_remote 的 SkillRuntime 并派生 SKU。
// stdio 探测成功后由 InstallMCPRuntime 异步 manager.Start 拉起长驻进程；
// http 探测成功后 ForceProbe 同步置 online。
// POST /v1/claw-console/mcp/install
func (h *ClawConsoleHandler) InstallMCP(c *gin.Context) {
	var req mcpInstallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "请求参数错误: " + err.Error()})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "name 不能为空"})
		return
	}
	if h.moduleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "模块服务未初始化"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result, fail := h.probeAndInstallMCP(ctx, req)
	if fail != nil {
		if fail.ErrorCode == "interpreter_missing" {
			c.JSON(http.StatusOK, gin.H{
				"code":    2002,
				"message": fail.Message,
				"data": gin.H{
					"error_code":  "interpreter_missing",
					"interpreter": fail.Interpreter,
					"hint":        fail.Hint,
				},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": fail.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    result,
	})
}

// ImportMCPConfig 批量导入标准 MCP 配置文件（M4）。
// POST /v1/claw-console/mcp/import-config：接受 Claude Desktop（claude_desktop_config.json）
// / Cursor / .mcp.json 通用配置（{"mcpServers": {name: {command, args, env | url, headers}}}），
// 支持粘贴 JSON（application/json）或上传文件（multipart/form-data，file 字段）。
// 逐 server 走 probeAndInstallMCP（G3 探测 + InstallMCPRuntime + DeriveSKUs），返回每个 server 结果。
// 不执行任意命令（仅经 G3 受控 spawn）；不认识字段忽略。单个 server 失败不中断其余。
func (h *ClawConsoleHandler) ImportMCPConfig(c *gin.Context) {
	if h.moduleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "模块服务未初始化"})
		return
	}

	var raw []byte
	var err error
	if strings.HasPrefix(c.ContentType(), "multipart/") {
		file, ferr := c.FormFile("file")
		if ferr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "读取上传文件失败: " + ferr.Error()})
			return
		}
		f, ferr := file.Open()
		if ferr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "打开上传文件失败: " + ferr.Error()})
			return
		}
		defer f.Close()
		raw, err = io.ReadAll(f)
	} else {
		raw, err = io.ReadAll(c.Request.Body)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "读取请求体失败: " + err.Error()})
		return
	}

	reqs, skipped, err := service.ParseMCPConfig(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}
	if len(reqs) == 0 && len(skipped) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": "配置中无可导入的 MCP server（需 command 或 url）"})
		return
	}

	// 多 server 顺序探测+安装；每 server 最长 30s，整体上限按数量线性放宽。
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(len(reqs))*30*time.Second)
	defer cancel()

	outcomes := make([]mcpInstallOutcome, 0, len(reqs)+len(skipped))
	// E2：被跳过的条目（sse / 缺 command/url）带进因并入结果，不再静默丢弃。
	for _, sk := range skipped {
		outcomes = append(outcomes, mcpInstallOutcome{Name: sk.Name, ErrorCode: sk.Code, Message: sk.Reason})
	}
	for _, rq := range reqs {
		one := mcpInstallRequest{
			mcpProbeRequest: mcpProbeRequest{
				Transport: rq.Transport,
				Command:   rq.Command,
				Args:      rq.Args,
				Env:       rq.Env,
				Endpoint:  rq.Endpoint,
				Headers:   rq.Headers,
			},
			Name:        rq.Name,
			Description: rq.Description,
		}
		result, fail := h.probeAndInstallMCP(ctx, one)
		oc := mcpInstallOutcome{Name: rq.Name, Transport: rq.Transport}
		if fail != nil {
			oc.ErrorCode = fail.ErrorCode
			oc.Message = fail.Message
			oc.Interpreter = fail.Interpreter
			oc.Hint = fail.Hint
		} else {
			oc.OK = true
			oc.Result = result
		}
		outcomes = append(outcomes, oc)
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"results": outcomes},
	})
}

// SearchMCPRegistry 搜索 MCP 官方社区注册表（E1）。
// GET /v1/claw-console/mcp/registry/search?q=&limit=：只读代理 registry.modelcontextprotocol.io，
// 每个结果映射为安装表单建议（streamable-http remote -> mcp_http；npm/pypi -> npx/uvx stdio），
// 前端「填入安装表单」后走既有探测->安装链路。上游不可达返回 code 2002。
func (h *ClawConsoleHandler) SearchMCPRegistry(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "q 不能为空"})
		return
	}
	limit := 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	results, err := service.NewMCPRegistryClient().Search(ctx, q, limit)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"results": results},
	})
}

// GeneratePromptSkill 生成 prompt-only 秘技（E4）。
// POST /v1/claw-console/skills/generate：写 marketplace/{slug}/SKILL.md（Anthropic 标准，
// frontmatter 含 name/description/metadata.title/metadata.category）->
// seed.SyncPromptSkillDir 定向同步出 driver=none SKU（skillmd-{slug}，创建者=当前用户）。
func (h *ClawConsoleHandler) GeneratePromptSkill(c *gin.Context) {
	var req service.PromptSkillGenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "请求参数错误: " + err.Error()})
		return
	}
	if h.moduleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "模块服务未初始化"})
		return
	}
	result, err := h.moduleService.WritePromptSkill(req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error()})
		return
	}
	// 定向同步 SKU（只处理这一个目录，不做全量扫描防误下架）：
	// 创建者取登录用户 + 前端透传 username（缺省「我」），用户生成不标「官方」。
	creatorName := strings.TrimSpace(req.Username)
	if creatorName == "" {
		creatorName = "我"
	}
	created, synced, skipped := seed.SyncPromptSkillDir(
		repository.NewAgentRepo(h.db), filepath.Dir(result.Dir), result.SkillID,
		c.GetString("user_id"), creatorName, nil)
	if created+synced == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": fmt.Sprintf("SKILL.md 已写入但 SKU 同步失败（skipped=%d），请检查 frontmatter", skipped)})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"skill_id": result.SkillID,
			"sku_id":   "skillmd-" + result.SkillID,
			"dir":      result.Dir,
		},
	})
}

// validateProbeWorkDir 校验探测请求的 work_dir 在沙箱白名单内（仅 stdio）
func (h *ClawConsoleHandler) validateProbeWorkDir(workDir string) error {
	if workDir == "" || len(h.processWorkDirs) == 0 {
		return nil
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return err
	}
	for _, prefix := range h.processWorkDirs {
		absPrefix, err := filepath.Abs(prefix)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(absPrefix, absWorkDir); err == nil && rel != ".." {
			return nil
		}
	}
	return errProbeWorkDirForbidden
}

var errProbeWorkDirForbidden = &probeError{msg: "work_dir 不在允许范围内"}

type probeError struct{ msg string }

func (e *probeError) Error() string { return e.msg }

// GenerateModule 一键生成用户 stdio MCP 模块（阶段 E3）。
// POST /v1/claw-console/mcp/generate：ProbeStdio 探工具 -> 生成 module.json + main.py 写 marketplace home
// -> rescan -> autostart -> supervisor 探活触发 DeriveSKUs。失败返回 D3 结构化 interpreter 错误。
func (h *ClawConsoleHandler) GenerateModule(c *gin.Context) {
	var req service.UserModuleGenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "请求参数错误: " + err.Error()})
		return
	}
	if req.Command == "" {
		req.Command = "python"
	}
	if len(req.Args) == 0 {
		req.Args = []string{"main.py"}
	}
	if req.WorkDir != "" {
		if vErr := h.validateProbeWorkDir(req.WorkDir); vErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": vErr.Error()})
			return
		}
	}
	if h.mcpStdio == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "stdio MCP 协议未初始化"})
		return
	}
	if h.moduleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "模块服务未初始化"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	tools, err := h.mcpStdio.ProbeStdio(ctx, req.Command, req.Args, req.Env, req.WorkDir)
	if err != nil {
		var ime *service.InterpreterMissingError
		if errors.As(err, &ime) {
			c.JSON(http.StatusOK, gin.H{
				"code":    2002,
				"message": ime.Error(),
				"data": gin.H{
					"error_code":  "interpreter_missing",
					"interpreter": ime.Command,
					"hint":        ime.Hint,
				},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": "MCP 探测失败: " + err.Error()})
		return
	}

	result, err := h.moduleService.WriteUserModule(req, tools)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": "生成模块失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    result,
	})
}

// DraftMainPy skill-maker AI 起草 main.py 草稿（F1 收尾）。
// POST /v1/claw-console/mcp/draft-main：据能力描述 + 凭证声明调对话模型生成 stdio MCP 脚本草稿，
// 供 web 编辑器预览/编辑后再 generate。模型由前端从 /eleagent/models 选择传入。
func (h *ClawConsoleHandler) DraftMainPy(c *gin.Context) {
	var req service.DraftMainPyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "请求参数错误: " + err.Error()})
		return
	}
	if h.moduleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "模块服务未初始化"})
		return
	}
	// 起草是一次完整 LLM 补全，放宽到 90s
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	mainPy, err := h.moduleService.DraftMainPy(ctx, req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"main_py": mainPy},
	})
}

// TestCall 直接调用模块某工具（绕过 LLM/Agent 层），展示原始返回（F2）。
// POST /v1/claw-console/modules/:id/test-call：造秘技页验证「从 0 造模块并调用成功」。
// stdio 凭证在 spawn 时注入（D1），故无需 per-call 传凭证；模块离线返回可读错误。
func (h *ClawConsoleHandler) TestCall(c *gin.Context) {
	moduleID := c.Param("id")
	var req service.TestCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "请求参数错误: " + err.Error()})
		return
	}
	if h.moduleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "模块服务未初始化"})
		return
	}
	userID := c.GetString("user_id")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	result, err := h.moduleService.TestCall(ctx, moduleID, req, userID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    result,
	})
}

// DSHBridgeStatus 探测 DSH 桥接（dsh-mcp-bridge）本地可用性（T5）。
// 返回桥接目录/dsh-home 候选/已知插件元数据（凭据预填行与能力标签），
// 不可用时 available=false + hint 指引修复，前端据此渲染引导横幅。
// GET /v1/claw-console/dsh/bridge-status
func (h *ClawConsoleHandler) DSHBridgeStatus(c *gin.Context) {
	if h.dshBridgeSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "DSH 桥接服务未初始化"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    h.dshBridgeSvc.Status(ctx),
	})
}

// dshImportRuntimeRequest /v1/claw-console/dsh/import-runtime 请求体（T5）。
type dshImportRuntimeRequest struct {
	Package     string            `json:"package"`    // 必填，DSH 工具插件包名（如 @deepseek-ai/dsh-tool-fs）
	Name        string            `json:"name"`       // 运行时名，缺省由包名派生
	Description string            `json:"description"`
	DSHHome     string            `json:"dsh_home"`   // 已安装 DSH 包树目录，缺省用自动发现的首选
	Env         map[string]string `json:"env"`        // 凭据等环境变量（如 DEEPSEEK_API_KEY）
	Tools       []string          `json:"tools"`      // 工具白名单（可选）
}

// DSHImportRuntime 一键导入 DSH 工具插件为本地秘技运行时（T5）。
// 组装桥接命令（node <bridge>/dist/server.js --dsh-home ... --plugin ...），
// 复用 G3 probeAndInstallMCP 完成探测 → 建 SkillRuntime → 派生 SKU 全链路。
// POST /v1/claw-console/dsh/import-runtime
func (h *ClawConsoleHandler) DSHImportRuntime(c *gin.Context) {
	var req dshImportRuntimeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "请求参数错误: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Package) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "package 不能为空"})
		return
	}
	if h.moduleService == nil || h.dshBridgeSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "模块服务或 DSH 桥接服务未初始化"})
		return
	}

	bridgeDir := h.dshBridgeSvc.DiscoverBridgeDir()
	if bridgeDir == "" {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": "未找到 dsh-mcp-bridge 安装目录（需含 dist/server.js）；可设置环境变量 DSH_MCP_BRIDGE_HOME 指定"})
		return
	}
	dshHome := strings.TrimSpace(req.DSHHome)
	if dshHome == "" {
		candidates := h.dshBridgeSvc.DiscoverDSHHomeCandidates()
		if len(candidates) > 0 {
			dshHome = candidates[0]
		}
	}
	if dshHome == "" {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": "未发现已安装的 DSH 包树；请执行 npm install @deepseek-ai/dsh 或在表单中指定目录"})
		return
	}

	args, err := service.BuildBridgeArgs(bridgeDir, dshHome, []string{req.Package}, req.Tools)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = service.DefaultDSHRuntimeName(req.Package)
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		// 缺省描述取插件元数据（静态副本/实时 describe 均覆盖已知插件）。
		for _, p := range h.dshBridgeSvc.DescribePlugins(c.Request.Context(), bridgeDir) {
			if p.Package == req.Package {
				description = p.Description
				break
			}
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, fail := h.probeAndInstallMCP(ctx, mcpInstallRequest{
		mcpProbeRequest: mcpProbeRequest{
			Transport: "mcp_stdio",
			Command:   "node",
			Args:      args,
			Env:       req.Env,
		},
		Name:        name,
		Description: description,
	})
	if fail != nil {
		if fail.ErrorCode == "interpreter_missing" {
			c.JSON(http.StatusOK, gin.H{
				"code":    2002,
				"message": fail.Message,
				"data": gin.H{
					"error_code":  "interpreter_missing",
					"interpreter": fail.Interpreter,
					"hint":        fail.Hint,
				},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": fail.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    result,
	})
}

// DepsStatus 查询模块依赖状态（H2）：是否带第三方依赖（requirements.txt/package.json）
// 及是否已装。供秘技集市页展示「依赖未装」badge 与装前包列表预览。
// GET /v1/claw-console/modules/:id/deps-status
func (h *ClawConsoleHandler) DepsStatus(c *gin.Context) {
	moduleID := c.Param("id")
	if h.moduleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "模块服务未初始化"})
		return
	}
	status, err := h.moduleService.DepsStatus(moduleID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": status})
}

// InstallDeps 安装模块依赖（H2，用户显式触发）：python 建 venv + pip install；
// node npm install。装后重启模块使新依赖生效。不自动执行（用户在集市页确认才调）。
// POST /v1/claw-console/modules/:id/install-deps
func (h *ClawConsoleHandler) InstallDeps(c *gin.Context) {
	moduleID := c.Param("id")
	if h.moduleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "模块服务未初始化"})
		return
	}
	// 装依赖可能耗时较长（下载/编译），放宽到 5 分钟。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	result, err := h.moduleService.InstallDeps(ctx, moduleID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": err.Error(), "data": result})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": result})
}

// installInterpreterRequest /v1/claw-console/tools/install-interpreter 请求体（H1）。
type installInterpreterRequest struct {
	Interpreter string `json:"interpreter"` // python/python3/node/npx（缺省 python）
}

// InstallInterpreter 安装托管解释器（H1，降低安装成本）：用户无系统 python/node 时自动下载
// 官方发行版（python-build-standalone / nodejs.org，SHA-256 校验）到 ~/.eleball-claw/tools/。
// 系统已有则直接返回（source=system）；已装托管则复用（reused=true）；否则下载安装。
// D3 interpreter_missing 横幅的「自动安装」按钮调用此端点。
// POST /v1/claw-console/tools/install-interpreter
func (h *ClawConsoleHandler) InstallInterpreter(c *gin.Context) {
	var req installInterpreterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "请求参数错误: " + err.Error()})
		return
	}
	if req.Interpreter == "" {
		req.Interpreter = "python"
	}
	if h.interpreterBootstrap == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 2002, "message": "解释器引导未初始化"})
		return
	}
	// 下载安装可能耗时较长（~30MB），放宽到 5 分钟。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	resolved, err := h.interpreterBootstrap.EnsureInterpreter(ctx, req.Interpreter)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 2002, "message": "安装失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"interpreter": req.Interpreter,
			"path":        resolved.Path,
			"version":     resolved.Version,
			"source":      resolved.Source,
			"reused":      resolved.Reused,
		},
	})
}
