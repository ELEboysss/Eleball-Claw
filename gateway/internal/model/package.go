package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// PackageManifest 秘技包 manifest（marketplace/{id}/package.json，目录唯一入口）。
// 对应契约 specs/package-manifest-schema.json（T2.1 解析器）。
// 三段能力：skills（纯 prompt，skills/{name}/SKILL.md）、tools（process/docker/http）、mcpServers（stdio/http/sse）。
// 目录事实源；DB 中的 SkillRuntime/AgentItem 是其物化视图。
type PackageManifest struct {
	Name        string                       `json:"name"`
	Version     string                       `json:"version"`
	Description string                       `json:"description"`
	Author      string                       `json:"author,omitempty"`
	Category    string                       `json:"category,omitempty"`
	Level       int                          `json:"level,omitempty"`
	Skills      []PackageSkill               `json:"skills,omitempty"`
	Tools       []PackageTool                `json:"tools,omitempty"`
	MCPServers  map[string]PackageMCPServer  `json:"mcpServers,omitempty"`
	// AutoSKU 由 mcpServers 经 DeriveSKUs 派生逐工具 SKU（对齐 legacy module.json auto_sku）：
	// true 时跳过 D5「每 mcpServer 一个通用 SKU」，由 MCP tools/list 派生 mcp__{server}__{tool}。
	AutoSKU bool `json:"auto_sku,omitempty"`
}

// PackageSkill 纯 prompt 能力项；每项 = skills/{name}/SKILL.md → 1 个 prompt 型 SKU。
type PackageSkill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// PackageTool 工具项；每项 → 1 个 tool 型 SKU（{package}-{name}）。
// transport 决定执行方式：process=子进程 / docker=容器镜像 / http=远程服务。
type PackageTool struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Transport   string                  `json:"transport"` // process | docker | http
	Command     []string                `json:"command,omitempty"`
	Args        []string                `json:"args,omitempty"`
	Env         map[string]string       `json:"env,omitempty"`
	Image       string                  `json:"image,omitempty"`
	Endpoint    string                  `json:"endpoint,omitempty"`
	Parameters  *PackageToolParameters  `json:"parameters,omitempty"`
	Credentials map[string]PackageCredential `json:"credentials,omitempty"`
	Pricing     *PackagePricing         `json:"pricing,omitempty"`
	TimeoutSeconds int                  `json:"timeout_seconds,omitempty"`
	ErrorCodes  []string                `json:"error_codes,omitempty"`
}

// PackageToolParameters OpenAI function calling 参数 schema（type 必须为 object）。
type PackageToolParameters struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
	Required   []string       `json:"required,omitempty"`
}

// PackageCredential 工具需要的用户凭证声明（Cookie/API Key/Token）。
type PackageCredential struct {
	Type        string `json:"type"` // cookie | api_key | token
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PackagePricing 工具定价（缺省 free）。
type PackagePricing struct {
	Type          string `json:"type,omitempty"` // free | per_call | per_token
	AmountPerCall int    `json:"amount_per_call,omitempty"`
	Currency      string `json:"currency,omitempty"` // danwan | elegant | cny
}

// PackageMCPServer MCP 服务器配置；每项 → 1 个 mcp 型 SKU（{package}-mcp-{key}）。
// transport：stdio=子进程 / http=Streamable HTTP / sse=Server-Sent Events。
type PackageMCPServer struct {
	Transport string            `json:"transport"` // stdio | http | sse
	Command   []string          `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`     // cloud/DNS 可达地址（云端物化用）
	HostURL   string            `json:"hostUrl,omitempty"` // claw/宿主机可达地址（发布端口，claw 物化优先用）
	Headers   map[string]string `json:"headers,omitempty"`
	// Credentials MCP 服务器用户凭证声明（与 PackageTool.Credentials 同构）：
	// 物化时经 rt.SetCredentials 透传，逐工具派生 SKU 继承（buildDerivedManifest.CredentialsMap）。
	Credentials map[string]PackageCredential `json:"credentials,omitempty"`
}

// 字段校验模式（与 specs/package-manifest-schema.json 一致）。
var (
	pkgNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	pkgSkillPattern = pkgNamePattern
	pkgToolPattern  = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	pkgVerPattern   = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// ParsePackageManifest 解析 package.json 字节：严格 JSON（未知字段报错）+ 字段校验。
// 未知字段报错对齐 schema 的 additionalProperties:false；level 缺省按 1。
func ParsePackageManifest(data []byte) (*PackageManifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m PackageManifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("package.json 解析失败: %w", err)
	}
	// 禁止尾随垃圾（如两个 JSON 拼接）：第二个顶层值非 EOF 即报错
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("package.json 含多余内容（多个 JSON 对象？）")
	}
	if m.Level == 0 {
		m.Level = 1
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate 校验 manifest 字段（名称/版本/等级/三段能力/transport 专属字段）。
func (m *PackageManifest) Validate() error {
	if m == nil {
		return errors.New("manifest 为空")
	}
	if !pkgNamePattern.MatchString(m.Name) {
		return fmt.Errorf("name 非法（需小写 slug [a-z0-9][a-z0-9-]*）: %q", m.Name)
	}
	if !pkgVerPattern.MatchString(m.Version) {
		return fmt.Errorf("version 非法（需语义化 主.次.修订）: %q", m.Version)
	}
	if m.Description == "" {
		return errors.New("description 不能为空")
	}
	if m.Level < 1 || m.Level > 6 {
		return fmt.Errorf("level 需在 1-6 之间，得到 %d", m.Level)
	}

	seenSkill := map[string]bool{}
	for i, sk := range m.Skills {
		if !pkgSkillPattern.MatchString(sk.Name) {
			return fmt.Errorf("skills[%d].name 非法（需小写 slug）: %q", i, sk.Name)
		}
		if seenSkill[sk.Name] {
			return fmt.Errorf("skills 名重复: %q", sk.Name)
		}
		seenSkill[sk.Name] = true
	}

	seenTool := map[string]bool{}
	for i, t := range m.Tools {
		if err := t.validate(); err != nil {
			return fmt.Errorf("tools[%d](%s): %w", i, t.Name, err)
		}
		if seenTool[t.Name] {
			return fmt.Errorf("tools 名重复: %q", t.Name)
		}
		seenTool[t.Name] = true
	}

	for key, srv := range m.MCPServers {
		if err := srv.validate(); err != nil {
			return fmt.Errorf("mcpServers[%q]: %w", key, err)
		}
	}
	return nil
}

func (t PackageTool) validate() error {
	if !pkgToolPattern.MatchString(t.Name) {
		return fmt.Errorf("name 非法（需 [a-zA-Z0-9_]+）: %q", t.Name)
	}
	if t.Description == "" {
		return errors.New("description 不能为空")
	}
	switch t.Transport {
	case "process":
		if len(t.Command) == 0 {
			return errors.New("transport=process 时 command 必填（argv）")
		}
	case "docker":
		if t.Image == "" {
			return errors.New("transport=docker 时 image 必填")
		}
	case "http":
		if t.Endpoint == "" {
			return errors.New("transport=http 时 endpoint 必填")
		}
	default:
		return fmt.Errorf("transport 非法（需 process/docker/http）: %q", t.Transport)
	}
	if t.Parameters != nil {
		if t.Parameters.Type != "object" {
			return fmt.Errorf("parameters.type 必须为 object，得到 %q", t.Parameters.Type)
		}
		if t.Parameters.Properties == nil {
			return errors.New("parameters.properties 必填")
		}
	}
	for name, c := range t.Credentials {
		switch c.Type {
		case "cookie", "api_key", "token":
		default:
			return fmt.Errorf("credentials[%q].type 非法（需 cookie/api_key/token）: %q", name, c.Type)
		}
	}
	if t.Pricing != nil {
		switch t.Pricing.Type {
		case "", "free", "per_call", "per_token":
		default:
			return fmt.Errorf("pricing.type 非法（需 free/per_call/per_token）: %q", t.Pricing.Type)
		}
	}
	return nil
}

func (s PackageMCPServer) validate() error {
	switch s.Transport {
	case "stdio":
		if len(s.Command) == 0 {
			return errors.New("transport=stdio 时 command 必填（argv）")
		}
	case "http", "sse":
		if s.URL == "" {
			return fmt.Errorf("transport=%s 时 url 必填", s.Transport)
		}
	default:
		return fmt.Errorf("transport 非法（需 stdio/http/sse）: %q", s.Transport)
	}
	for name, c := range s.Credentials {
		switch c.Type {
		case "cookie", "api_key", "token":
		default:
			return fmt.Errorf("credentials[%q].type 非法（需 cookie/api_key/token）: %q", name, c.Type)
		}
	}
	return nil
}
