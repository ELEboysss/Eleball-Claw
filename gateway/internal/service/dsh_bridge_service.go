package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// dsh_bridge_service.go —— DSH 桥接（dsh-mcp-bridge）运行时的本地发现与命令组装。
//
// 桥接器把 DSH 插件树包装成 MCP stdio server（见 tools/dsh-mcp-bridge）；
// 本服务负责 claw 侧的「导入 DSH 插件」一键入口所需的两项本地发现：
//  1. 桥接安装目录（含 dist/server.js）：env DSH_MCP_BRIDGE_HOME > 可执行文件旁/工作目录候选。
//  2. dsh-home（已安装 DSH 包树，含 node_modules/@deepseek-ai/*）：
//     请求显式指定 > env DSH_PACKAGE_HOME > npm 全局 root > npx 缓存扫描。
//
// 插件元数据（凭据预填/能力标签）由桥接器 `--describe` 单次吐出（TS 侧单一事实源，
// 避免 Go/TS 双写漂移）；桥接不可用时 Go 侧回退内置静态副本，保证表单仍可展示。

// DSHBridgeEnvMeta 插件所需环境变量（凭据）的展示元数据。
type DSHBridgeEnvMeta struct {
	Name     string `json:"name"`
	Hint     string `json:"hint"`
	Required bool   `json:"required"`
}

// DSHBridgePluginMeta 已知 DSH 工具插件的展示元数据（与桥接器 --describe 同构）。
type DSHBridgePluginMeta struct {
	Package     string             `json:"package"`
	Label       string             `json:"label"`
	Description string             `json:"description"`
	Tools       []string           `json:"tools"`
	Env         []DSHBridgeEnvMeta `json:"env"`
	Tags        []string           `json:"tags"`
}

// DSHBridgeStatus 桥接可用性探测结果（GET /dsh/bridge-status 响应）。
type DSHBridgeStatus struct {
	Available         bool                  `json:"available"`           // 桥接目录与 dsh-home 均已发现
	NodeAvailable     bool                  `json:"node_available"`      // node 解释器可定位
	BridgeDir         string                `json:"bridge_dir"`          // 桥接安装目录（含 dist/server.js）
	DSHHome           string                `json:"dsh_home"`            // 自动发现的 dsh-home（可被请求覆盖）
	DSHHomeCandidates []string              `json:"dsh_home_candidates"` // 全部候选（前端下拉）
	Plugins           []DSHBridgePluginMeta `json:"plugins"`             // 已知插件元数据
	Hint              string                `json:"hint,omitempty"`      // 不可用时的修复指引
}

// DSHBridgeService 桥接发现与命令组装。无持久状态，按需构造。
type DSHBridgeService struct {
	logger *zap.Logger
}

// NewDSHBridgeService 创建桥接服务。
func NewDSHBridgeService(logger *zap.Logger) *DSHBridgeService {
	return &DSHBridgeService{logger: logger}
}

// dshBridgeMarker 判断一个目录是否为桥接安装目录。
func dshBridgeMarker(dir string) bool {
	if dir == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, "dist", "server.js"))
	return err == nil && !st.IsDir()
}

// dshHomeMarker 判断一个目录是否为已安装 DSH 包树（dsh-home）。
func dshHomeMarker(dir string) bool {
	if dir == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh-app-boot"))
	return err == nil && st.IsDir()
}

// DiscoverBridgeDir 发现桥接安装目录；找不到返回空串。
func (s *DSHBridgeService) DiscoverBridgeDir() string {
	if env := os.Getenv("DSH_MCP_BRIDGE_HOME"); dshBridgeMarker(env) {
		return env
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "tools", "dsh-mcp-bridge"),
			filepath.Join(exeDir, "..", "tools", "dsh-mcp-bridge"),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "tools", "dsh-mcp-bridge"),
			// 开发期：claw-server 从 eleball-claw/gateway 起，桥接在主仓 tools/ 下
			filepath.Join(cwd, "..", "..", "tools", "dsh-mcp-bridge"),
		)
	}
	for _, dir := range candidates {
		if dshBridgeMarker(dir) {
			abs, err := filepath.Abs(dir)
			if err != nil {
				return dir
			}
			return abs
		}
	}
	return ""
}

// npxCacheDirs 返回 npx 缓存根（各平台惯例路径）。
func npxCacheDirs() []string {
	var dirs []string
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			dirs = append(dirs, filepath.Join(local, "npm-cache", "_npx"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".npm", "_npx"))
	}
	return dirs
}

// DiscoverDSHHomeCandidates 扫描全部 dsh-home 候选（npx 缓存按修改时间倒序）。
func (s *DSHBridgeService) DiscoverDSHHomeCandidates() []string {
	var found []string
	if env := os.Getenv("DSH_PACKAGE_HOME"); dshHomeMarker(env) {
		found = append(found, env)
	}
	// npm 全局安装：`npm root -g`/@deepseek-ai/dsh 的父目录即包树根
	if out, err := exec.Command("npm", "root", "-g").Output(); err == nil {
		root := strings.TrimSpace(string(out))
		if dshHomeMarker(filepath.Join(root, "@deepseek-ai", "dsh")) {
			// 全局 root 本身即 node_modules，取其父目录
			found = append(found, filepath.Dir(root))
		}
	}
	type candidate struct {
		path  string
		mtime time.Time
	}
	var scanned []candidate
	for _, cacheRoot := range npxCacheDirs() {
		entries, err := os.ReadDir(cacheRoot)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(cacheRoot, entry.Name())
			if !dshHomeMarker(dir) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			scanned = append(scanned, candidate{path: dir, mtime: info.ModTime()})
		}
	}
	sort.Slice(scanned, func(i, j int) bool { return scanned[i].mtime.After(scanned[j].mtime) })
	for _, c := range scanned {
		found = append(found, c.path)
	}
	return found
}

// dshBridgeStaticPlugins 桥接不可用时的静态元数据回退（与桥接器 --describe 保持同步；
// 正常路径以 --describe 实时输出为准，本表仅兜底表单展示）。
var dshBridgeStaticPlugins = []DSHBridgePluginMeta{
	{
		Package:     "@deepseek-ai/dsh-tool-fs",
		Label:       "DSH 文件工具",
		Description: "读写编辑本地文本文件（read/write/edit），无沙箱直读写",
		Tools:       []string{"read", "write", "edit"},
		Tags:        []string{"filesystem"},
	},
	{
		Package:     "@deepseek-ai/dsh-tool-fs-search",
		Label:       "DSH 文件搜索",
		Description: "按文件名 glob 与按内容 grep 搜索本地文件",
		Tools:       []string{"glob", "grep"},
		Tags:        []string{"filesystem", "subprocess"},
	},
	{
		Package:     "@deepseek-ai/dsh-tool-web",
		Label:       "DSH 联网搜索",
		Description: "DeepSeek 联网搜索（web_search；web_fetch 无公开提供者，预设关闭）",
		Tools:       []string{"web_search"},
		Env:         []DSHBridgeEnvMeta{{Name: "DEEPSEEK_API_KEY", Hint: "DeepSeek 开放平台 API Key（搜索请求按次调用）", Required: true}},
		Tags:        []string{"network"},
	},
	{
		Package:     "@deepseek-ai/dsh-tool-pwsh",
		Label:       "DSH Shell（PowerShell）",
		Description: "执行 PowerShell 命令（Windows；Linux/macOS 请选 bash 插件）",
		Tools:       []string{"pwsh"},
		Tags:        []string{"shell", "subprocess"},
	},
	{
		Package:     "@deepseek-ai/dsh-tool-bash",
		Label:       "DSH Shell（bash）",
		Description: "执行 bash 命令（Linux/macOS；Windows 请选 pwsh 插件）",
		Tools:       []string{"bash"},
		Tags:        []string{"shell", "subprocess"},
	},
}

// DescribePlugins 调桥接器 --describe 拉取插件元数据；失败回退静态副本。
func (s *DSHBridgeService) DescribePlugins(ctx context.Context, bridgeDir string) []DSHBridgePluginMeta {
	node, err := locateCommand("node")
	if err == nil && bridgeDir != "" {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, runErr := exec.CommandContext(ctx, node, filepath.Join(bridgeDir, "dist", "server.js"), "--describe").Output()
		if runErr == nil {
			var payload struct {
				Plugins []DSHBridgePluginMeta `json:"plugins"`
			}
			if jsonErr := json.Unmarshal(out, &payload); jsonErr == nil && len(payload.Plugins) > 0 {
				return payload.Plugins
			}
		}
		if s.logger != nil {
			s.logger.Warn("dsh-mcp-bridge --describe 失败，回退静态插件元数据", zap.Error(runErr))
		}
	}
	return dshBridgeStaticPlugins
}

// Status 汇总桥接可用性探测结果。
func (s *DSHBridgeService) Status(ctx context.Context) *DSHBridgeStatus {
	_, nodeErr := locateCommand("node")
	bridgeDir := s.DiscoverBridgeDir()
	candidates := s.DiscoverDSHHomeCandidates()
	st := &DSHBridgeStatus{
		NodeAvailable:     nodeErr == nil,
		BridgeDir:         bridgeDir,
		DSHHomeCandidates: candidates,
		Plugins:           s.DescribePlugins(ctx, bridgeDir),
	}
	if len(candidates) > 0 {
		st.DSHHome = candidates[0]
	}
	st.Available = st.NodeAvailable && bridgeDir != "" && st.DSHHome != ""
	switch {
	case !st.NodeAvailable:
		st.Hint = "未找到 node 解释器，请先安装 Node.js（^22.19 或 >=24）"
	case bridgeDir == "":
		st.Hint = "未找到 dsh-mcp-bridge 安装目录（需含 dist/server.js）；可设置环境变量 DSH_MCP_BRIDGE_HOME 指定"
	case st.DSHHome == "":
		st.Hint = "未发现已安装的 DSH 包树；请执行 npm install @deepseek-ai/dsh，或设置环境变量 DSH_PACKAGE_HOME 指定安装目录"
	}
	return st
}

// BuildBridgeArgs 组装桥接进程 argv（server.js 之后的参数）。
func BuildBridgeArgs(bridgeDir, dshHome string, plugins, tools []string) ([]string, error) {
	if !dshBridgeMarker(bridgeDir) {
		return nil, fmt.Errorf("桥接目录无效（缺 dist/server.js）: %s", bridgeDir)
	}
	if !dshHomeMarker(dshHome) {
		return nil, fmt.Errorf("dsh-home 无效（缺 node_modules/@deepseek-ai/dsh-app-boot）: %s", dshHome)
	}
	if len(plugins) == 0 {
		return nil, errors.New("至少需要一个 DSH 插件包名")
	}
	args := []string{filepath.Join(bridgeDir, "dist", "server.js"), "--dsh-home", dshHome}
	for _, pkg := range plugins {
		args = append(args, "--plugin", pkg)
	}
	for _, tool := range tools {
		args = append(args, "--tool", tool)
	}
	return args, nil
}

// DefaultDSHRuntimeName 由插件包名派生默认运行时名（dsh-tool-fs -> dsh-fs）。
func DefaultDSHRuntimeName(pkg string) string {
	bare := pkg
	if idx := strings.LastIndex(bare, "/"); idx >= 0 {
		bare = bare[idx+1:]
	}
	bare = strings.TrimPrefix(bare, "dsh-")
	bare = strings.TrimPrefix(bare, "tool-")
	bare = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, strings.ToLower(bare))
	bare = strings.Trim(bare, "-")
	if bare == "" {
		bare = "plugin"
	}
	return "dsh-" + bare
}
