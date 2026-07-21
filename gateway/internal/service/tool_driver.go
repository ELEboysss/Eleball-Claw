package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/eleball/gateway/internal/model"
)

// ToolDriver 外部工具驱动接口
// 所有可接入 Eleball Agent 工作流的能力——无论是系统内置工具、Agent-Reach 互联网能力，
// 还是第三方远程服务——都通过此接口统一接入。
type ToolDriver interface {
	// Name 返回驱动标识，如 builtin / agent_reach / remote_url
	Name() string
	// Execute 执行具体动作
	// action: 动作名，来自 ToolManifest.Actions 或 LLM 调用参数
	// params: LLM 解析后的调用参数
	// env: 当前工具执行环境（含沙箱、Session、UserID 等）
	Execute(ctx context.Context, action string, params map[string]interface{}, env *ToolEnv) (map[string]interface{}, error)
	// Schema 返回该驱动默认的 ToolManifest（仅用于文档/校验，实际以 AgentItem 中 manifest 为准）
	Schema() model.ToolManifest
}

// ToolDriverRegistry 驱动注册表
// 负责按名称管理所有 ToolDriver 实例。
type ToolDriverRegistry struct {
	drivers map[string]ToolDriver
}

// NewToolDriverRegistry 创建驱动注册表
func NewToolDriverRegistry() *ToolDriverRegistry {
	return &ToolDriverRegistry{
		drivers: make(map[string]ToolDriver),
	}
}

// Register 注册驱动
func (r *ToolDriverRegistry) Register(driver ToolDriver) {
	r.drivers[driver.Name()] = driver
}

// Get 获取驱动
func (r *ToolDriverRegistry) Get(name string) (ToolDriver, bool) {
	driver, ok := r.drivers[name]
	return driver, ok
}

// List 列出所有驱动
func (r *ToolDriverRegistry) List() []ToolDriver {
	items := make([]ToolDriver, 0, len(r.drivers))
	for _, d := range r.drivers {
		items = append(items, d)
	}
	return items
}

// ListNames 列出所有驱动名称
func (r *ToolDriverRegistry) ListNames() []string {
	names := make([]string, 0, len(r.drivers))
	for name := range r.drivers {
		names = append(names, name)
	}
	return names
}

// ToolResult 统一工具输出格式
// 所有 driver 建议返回此结构，便于 LLM 消费与前端展示。
type ToolResult struct {
	Content   string   `json:"content"`
	Sources   []string `json:"sources,omitempty"`
	Error     string   `json:"error,omitempty"`
	ErrorCode string   `json:"error_code,omitempty"`
}

// ToMap 将 ToolResult 转为 map[string]interface{}
func (r ToolResult) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"content": r.Content,
		"sources": r.Sources,
		"error":   r.Error,
	}
	if r.ErrorCode != "" {
		m["error_code"] = r.ErrorCode
	}
	return m
}

// ToolResultFromMap 从 map 解析 ToolResult
func ToolResultFromMap(m map[string]interface{}) ToolResult {
	var res ToolResult
	if v, ok := m["content"].(string); ok {
		res.Content = v
	}
	if arr, ok := m["sources"].([]string); ok {
		res.Sources = arr
	}
	if v, ok := m["error"].(string); ok {
		res.Error = v
	}
	return res
}

// remoteURLDriver 远程 HTTP 工具驱动
type remoteURLDriver struct {
	client *http.Client
}

func newRemoteURLDriver(timeout int) ToolDriver {
	return &remoteURLDriver{
		client: &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}
}

func (d *remoteURLDriver) Name() string {
	return string(model.ToolDriverRemoteURL)
}

func (d *remoteURLDriver) Schema() model.ToolManifest {
	return model.ToolManifest{
		ID:          "com.eleball.tools.remote_url",
		Name:        "远程 URL 工具",
		Description: "通过 HTTP POST 调用远程服务执行自定义逻辑。",
		Driver:      model.ToolDriverRemoteURL,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{"type": "string", "description": "动作名"},
				"input":  map[string]interface{}{"type": "string", "description": "输入参数 JSON 字符串"},
			},
			"required": []string{"action", "input"},
		},
	}
}

func (d *remoteURLDriver) Execute(ctx context.Context, action string, params map[string]interface{}, env *ToolEnv) (map[string]interface{}, error) {
	endpoint, _ := params["__endpoint__"].(string)
	if endpoint == "" {
		return nil, errors.New("remote_url driver 缺少 endpoint")
	}
	input := map[string]interface{}{}
	if raw, ok := params["input"].(string); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &input)
	}
	body := map[string]interface{}{
		"action":  action,
		"params":  params,
		"input":   input,
		"user_id": env.UserID,
		"session": env.SessionID,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("remote_url 调用失败，状态码 %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}
