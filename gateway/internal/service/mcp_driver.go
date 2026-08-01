package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
)

// mcpDriver Streamable HTTP JSON-RPC MCP 工具驱动（一期仅支持 tools/call）。
// 不实现 stdio 传输、进程管理或 probe 端点。
type mcpDriver struct {
	client            *http.Client
	credentialService *AgentCredentialService
}

// NewMCPDriver 创建 MCP 驱动实例
func NewMCPDriver(credentialService *AgentCredentialService) ToolDriver {
	return &mcpDriver{
		client:            &http.Client{Timeout: 60 * time.Second},
		credentialService: credentialService,
	}
}

func (d *mcpDriver) Name() string {
	return string(model.ToolDriverMCP)
}

func (d *mcpDriver) Schema() model.ToolManifest {
	return model.ToolManifest{
		ID:          "com.eleball.tools.mcp",
		Name:        "MCP 远程工具",
		Description: "通过 Streamable HTTP JSON-RPC 调用兼容 MCP 协议的远程工具服务。",
		Driver:      model.ToolDriverMCP,
		Category:    "system",
		Level:       1,
		Permissions: []model.ToolPermission{model.ToolPermissionNetwork},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{"type": "string", "description": "MCP 工具名"},
				"params": map[string]interface{}{"type": "object", "description": "工具业务参数"},
			},
			"required": []string{"action"},
		},
	}
}

func (d *mcpDriver) Execute(ctx context.Context, action string, params map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	// 1. 解析目标 endpoint
	endpoint := ""
	if v, ok := params["__mcp_endpoint__"].(string); ok {
		endpoint = v
	}

	var config *model.MCPServerConfig
	if endpoint == "" {
		if raw, ok := params["__mcp_server__"].(string); ok && raw != "" {
			config = &model.MCPServerConfig{}
			if err := json.Unmarshal([]byte(raw), config); err != nil {
				return ToolResult{Error: fmt.Sprintf("解析 MCP 服务器配置失败: %v", err), ErrorCode: "parameter_invalid"}.ToMap(), nil
			}
			endpoint = config.URL
		}
	}

	if endpoint == "" {
		return ToolResult{Error: "mcp driver 缺少 endpoint", ErrorCode: "parameter_invalid"}.ToMap(), nil
	}

	// 2. 构建 arguments
	arguments := make(map[string]interface{})
	if raw, ok := params["params"].(map[string]interface{}); ok && raw != nil {
		for k, v := range raw {
			arguments[k] = v
		}
	} else {
		for k, v := range params {
			if k == "action" || k == "__mcp_endpoint__" || k == "__mcp_server__" ||
				k == "__module_id__" || k == "__endpoint__" || k == "credentials" {
				continue
			}
			arguments[k] = v
		}
	}

	// 3. 注入凭证
	credentials := make(map[string]string)
	if env.UserID != "" && env.AgentID != "" && d.credentialService != nil && len(env.Credentials) > 0 {
		if err := d.credentialService.ValidateRequired(env.UserID, env.AgentID, env.Credentials); err != nil {
			return ToolResult{Error: err.Error(), ErrorCode: "credential_required"}.ToMap(), nil
		}
		loaded, err := d.credentialService.LoadForExecution(env.UserID, env.AgentID)
		if err != nil {
			return ToolResult{Error: err.Error(), ErrorCode: "credential_required"}.ToMap(), nil
		}
		for k, v := range loaded {
			credentials[k] = v
		}
	}

	// 4. 准备请求头（支持 ${credentials.KEY} 模板替换）
	headers := make(map[string]string)
	if config != nil && len(config.Headers) > 0 {
		for hk, hv := range config.Headers {
			headers[hk] = substituteCredentialPlaceholders(hv, credentials)
		}
	} else if apiKey := credentials["api_key"]; apiKey != "" {
		// headers 为空且存在 api_key 时，默认使用 Bearer 认证
		headers["Authorization"] = "Bearer " + apiKey
	}

	// 5. 构建 JSON-RPC 2.0 请求体
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      rand.Int(),
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      action,
			"arguments": arguments,
		},
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	// 6. 发起 HTTP POST
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for hk, hv := range headers {
		req.Header.Set(hk, hv)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return ToolResult{Error: fmt.Sprintf("mcp server returned %d", resp.StatusCode), ErrorCode: "upstream_error"}.ToMap(), nil
	}

	// 7. 解析 JSON-RPC 响应
	var rpcResp struct {
		JSONRPC string                 `json:"jsonrpc"`
		ID      interface{}            `json:"id"`
		Result  map[string]interface{} `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return ToolResult{Error: rpcResp.Error.Message, ErrorCode: "upstream_error"}.ToMap(), nil
	}

	// 8. 处理 result：isError=true 时表示工具级错误
	if rpcResp.Result != nil {
		if isErr, _ := rpcResp.Result["isError"].(bool); isErr {
			content := extractMCPContentText(rpcResp.Result)
			return ToolResult{Error: content, ErrorCode: "tool_error"}.ToMap(), nil
		}
		return rpcResp.Result, nil
	}
	return map[string]interface{}{}, nil
}

// credentialPlaceholderRe 匹配 header 模板中的 ${credentials.KEY}。
var credentialPlaceholderRe = regexp.MustCompile(`\$\{credentials\.([^}]+)\}`)

// substituteCredentialPlaceholders 将 ${credentials.KEY} 替换为对应凭证值，
// 无法解析时替换为空字符串。
func substituteCredentialPlaceholders(value string, credentials map[string]string) string {
	return credentialPlaceholderRe.ReplaceAllStringFunc(value, func(match string) string {
		matches := credentialPlaceholderRe.FindStringSubmatch(match)
		if len(matches) < 2 {
			return ""
		}
		if v, ok := credentials[matches[1]]; ok {
			return v
		}
		return ""
	})
}

// extractMCPContentText 从 MCP result 中提取文本内容，兼容 content 数组或 content 字符串。
func extractMCPContentText(result map[string]interface{}) string {
	if content, ok := result["content"].(string); ok {
		return content
	}
	if arr, ok := result["content"].([]interface{}); ok {
		var sb strings.Builder
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					sb.WriteString(text)
				}
			}
		}
		return sb.String()
	}
	return ""
}
