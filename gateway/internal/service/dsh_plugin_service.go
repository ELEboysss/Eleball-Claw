package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DSH 插件导入（F4）：DSH 插件即 npm 包（@deepseek-ai/dsh-* / dsh-plugin-* / 任意兼容包）。
// 本服务从 npm registry 拉取包 tarball，扫描其中的 Anthropic 标准 SKILL.md（.dsh/skills/、
// skills/、根目录均可）与 package.json 的 mcpServers 配置，预览后可选择导入：
// SKILL.md → WritePromptSkillRaw 落 marketplace/{slug}/（frontmatter 全字段保留）；
// mcpServers → 探测 + InstallMCPRuntime（与 MCP 安装同路径）。

const (
	// dshPluginRegistryBase npm registry 元数据地址
	dshPluginRegistryBase = "https://registry.npmjs.org"
	// dshPluginMaxTarballBytes tarball 下载上限（防异常包放大）
	dshPluginMaxTarballBytes = 20 << 20
	// dshPluginMaxFileBytes 单个条目读取上限（SKILL.md/package.json 级别）
	dshPluginMaxFileBytes = 1 << 20
)

// DSHPluginService DSH 插件（npm 包）预览/导入服务。HTTPClient/RegistryBase 可替换（测试指向 httptest）。
type DSHPluginService struct {
	RegistryBase string
	HTTPClient   *http.Client
	// moduleSvc / mcpStdio 由 main 装配；仅 Import 的 mcpServers 分支需要
	moduleSvc *ModuleService
	mcpStdio  *MCPStdioProtocol
}

// NewDSHPluginService 生产客户端（官方 registry + 30s 超时）。
func NewDSHPluginService(moduleSvc *ModuleService, mcpStdio *MCPStdioProtocol) *DSHPluginService {
	return &DSHPluginService{
		RegistryBase: dshPluginRegistryBase,
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		moduleSvc:    moduleSvc,
		mcpStdio:     mcpStdio,
	}
}

// DSHPluginSkillItem 预览中的可导入秘技项（包内一个 SKILL.md）。
type DSHPluginSkillItem struct {
	Path        string `json:"path"`        // 包内路径（导入选择标识）
	Slug        string `json:"slug"`        // 建议 skill_id（frontmatter.name 折叠）
	Name        string `json:"name"`        // 展示名（metadata.title 或 name）
	Description string `json:"description"` // frontmatter.description
}

// DSHPluginMCPServerItem 预览中的可导入 MCP server 项（package.json mcpServers）。
type DSHPluginMCPServerItem struct {
	Name     string            `json:"name"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Endpoint string            `json:"endpoint,omitempty"` // url 字段（http 型）
	Env      map[string]string `json:"env,omitempty"`
}

// DSHPluginPreview 预览结果。
type DSHPluginPreview struct {
	Package     string                   `json:"package"`
	Version     string                   `json:"version"`
	Description string                   `json:"description"`
	Skills      []DSHPluginSkillItem     `json:"skills"`
	MCPServers  []DSHPluginMCPServerItem `json:"mcp_servers"`
}

// DSHPluginImportResult 单项导入结果。
type DSHPluginImportResult struct {
	Skills  []string `json:"skills"`  // 成功导入的 skill_id 列表
	MCPs    []string `json:"mcps"`    // 成功安装的 MCP server 名
	Skipped []string `json:"skipped"` // 跳过/失败项（附原因）
}

// parseNPMSpec 解析 "name" / "name@version" / "@scope/name@version"。
func parseNPMSpec(spec string) (name, version string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", errors.New("包名不能为空")
	}
	at := strings.LastIndex(spec, "@")
	if at > 0 { // at=0 是 scoped 包名前导 @，非版本分隔
		name, version = spec[:at], spec[at+1:]
	} else {
		name = spec
	}
	if name == "" || strings.ContainsAny(name, " /\\") && !strings.HasPrefix(name, "@") {
		return "", "", fmt.Errorf("非法 npm 包名: %q", spec)
	}
	return name, version, nil
}

// resolveTarballURL 查询 registry 元数据，取目标版本的 tarball URL 与描述。
func (s *DSHPluginService) resolveTarballURL(ctx context.Context, name, version string) (tarball, resolvedVersion, description string, err error) {
	metaURL := strings.TrimSuffix(s.RegistryBase, "/") + "/" + url.PathEscape(name)
	if strings.HasPrefix(name, "@") {
		// scoped 包：/ 需编码为 %2f（PathEscape 不编 /）
		metaURL = strings.TrimSuffix(s.RegistryBase, "/") + "/" + strings.Replace(name, "/", "%2f", 1)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return "", "", "", err
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("查询 npm registry 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", "", "", fmt.Errorf("npm 包不存在: %s", name)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("npm registry 返回 %d", resp.StatusCode)
	}
	var meta struct {
		DistTags map[string]string `json:"dist-tags"`
		Versions map[string]struct {
			Description string `json:"description"`
			Dist        struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
		} `json:"versions"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&meta); err != nil {
		return "", "", "", fmt.Errorf("解析 registry 元数据失败: %w", err)
	}
	if version == "" || version == "latest" {
		version = meta.DistTags["latest"]
	}
	v, ok := meta.Versions[version]
	if !ok {
		return "", "", "", fmt.Errorf("版本不存在: %s@%s", name, version)
	}
	desc := v.Description
	if desc == "" {
		desc = meta.Description
	}
	return v.Dist.Tarball, version, desc, nil
}

func (s *DSHPluginService) httpClient() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// dshPluginPackageJSON 包 package.json 中我们关心的字段。
type dshPluginPackageJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MCPServers  map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		URL     string            `json:"url"`
	} `json:"mcpServers"`
}

// fetchAndScan 下载 tarball 并扫描 SKILL.md 与 package.json（内存解析，不落盘）。
func (s *DSHPluginService) fetchAndScan(ctx context.Context, spec string) (*DSHPluginPreview, map[string][]byte, error) {
	name, version, err := parseNPMSpec(spec)
	if err != nil {
		return nil, nil, err
	}
	tarball, resolved, desc, err := s.resolveTarballURL(ctx, name, version)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarball, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("下载 tarball 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("tarball 下载返回 %d", resp.StatusCode)
	}
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, dshPluginMaxTarballBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("tarball 解压失败: %w", err)
	}
	defer gz.Close()

	preview := &DSHPluginPreview{Package: name, Version: resolved, Description: desc}
	skillContents := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("读取 tarball 失败: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// npm tarball 条目统一带 package/ 前缀；去掉后判定
		p := strings.TrimPrefix(hdr.Name, "package/")
		base := p
		if idx := strings.LastIndex(p, "/"); idx >= 0 {
			base = p[idx+1:]
		}
		switch {
		case base == "SKILL.md":
			data, err := io.ReadAll(io.LimitReader(tr, dshPluginMaxFileBytes))
			if err != nil {
				continue
			}
			m, err := ParseSkillMDContent(data)
			if err != nil {
				continue // 无合法 frontmatter 的 SKILL.md 跳过
			}
			skillContents[p] = data
			preview.Skills = append(preview.Skills, DSHPluginSkillItem{
				Path:        p,
				Slug:        foldSlugBase(m.Name),
				Name:        m.DisplayName(),
				Description: m.Description,
			})
		case p == "package.json":
			data, err := io.ReadAll(io.LimitReader(tr, dshPluginMaxFileBytes))
			if err != nil {
				continue
			}
			var pkg dshPluginPackageJSON
			if err := json.Unmarshal(data, &pkg); err != nil {
				continue
			}
			if preview.Description == "" {
				preview.Description = pkg.Description
			}
			for srvName, cfg := range pkg.MCPServers {
				preview.MCPServers = append(preview.MCPServers, DSHPluginMCPServerItem{
					Name:     srvName,
					Command:  cfg.Command,
					Args:     cfg.Args,
					Endpoint: cfg.URL,
					Env:      cfg.Env,
				})
			}
		}
	}
	if len(preview.Skills) == 0 && len(preview.MCPServers) == 0 {
		return nil, nil, fmt.Errorf("包 %s 中未找到可导入内容（SKILL.md 或 mcpServers 配置）", spec)
	}
	return preview, skillContents, nil
}

// Preview 预览 DSH 插件可导入内容（不写盘）。
func (s *DSHPluginService) Preview(ctx context.Context, spec string) (*DSHPluginPreview, error) {
	preview, _, err := s.fetchAndScan(ctx, spec)
	return preview, err
}

// Import 导入选中项：skills 为包内路径列表（空=全部），mcpServers 为名称列表（空=不导入 MCP）。
// 秘技导入后经 skillSyncFn 定向同步 SKU；MCP 走探测 + InstallMCPRuntime。
func (s *DSHPluginService) Import(ctx context.Context, spec string, skillPaths []string, mcpNames []string, creatorID string, skillSyncFn func(dir, skillID, creatorID, creatorName string) (int, int, int)) (*DSHPluginImportResult, error) {
	if s.moduleSvc == nil {
		return nil, errors.New("模块服务未初始化")
	}
	preview, contents, err := s.fetchAndScan(ctx, spec)
	if err != nil {
		return nil, err
	}
	result := &DSHPluginImportResult{}

	// 秘技导入（skillPaths 为空=全部导入）
	wantAll := len(skillPaths) == 0
	want := map[string]bool{}
	for _, p := range skillPaths {
		want[p] = true
	}
	for _, item := range preview.Skills {
		if !wantAll && !want[item.Path] {
			continue
		}
		res, err := s.moduleSvc.WritePromptSkillRaw(item.Slug, contents[item.Path])
		if err != nil {
			result.Skipped = append(result.Skipped, fmt.Sprintf("%s: %v", item.Path, err))
			continue
		}
		if skillSyncFn != nil {
			skillSyncFn(res.Dir, res.SkillID, creatorID, "DSH 插件")
		}
		result.Skills = append(result.Skills, res.SkillID)
	}

	// MCP server 导入（空=不导入；逐项探测安装）
	mcpWant := map[string]bool{}
	for _, n := range mcpNames {
		mcpWant[n] = true
	}
	for _, srv := range preview.MCPServers {
		if !mcpWant[srv.Name] {
			continue
		}
		req := &MCPInstallRequest{
			Name:     srv.Name,
			Command:  srv.Command,
			Args:     srv.Args,
			Env:      srv.Env,
			Endpoint: srv.Endpoint,
		}
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var tools []MCPTool
		var pErr error
		if srv.Endpoint != "" {
			req.Transport = "mcp_http"
			tools, pErr = NewMCPHTTPProtocol(nil).ListTools(probeCtx, srv.Endpoint, nil)
		} else if s.mcpStdio != nil {
			tools, pErr = s.mcpStdio.ProbeStdio(probeCtx, srv.Command, srv.Args, srv.Env, "")
		} else {
			pErr = errors.New("stdio MCP 协议未初始化")
		}
		cancel()
		if pErr != nil {
			result.Skipped = append(result.Skipped, fmt.Sprintf("mcp %s: 探测失败: %v", srv.Name, pErr))
			continue
		}
		if _, err := s.moduleSvc.InstallMCPRuntime(req, tools); err != nil {
			result.Skipped = append(result.Skipped, fmt.Sprintf("mcp %s: %v", srv.Name, err))
			continue
		}
		result.MCPs = append(result.MCPs, srv.Name)
	}
	if len(result.Skills) == 0 && len(result.MCPs) == 0 && len(result.Skipped) > 0 {
		return result, fmt.Errorf("全部导入失败: %s", strings.Join(result.Skipped, "; "))
	}
	return result, nil
}
