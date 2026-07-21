package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/pkg/llm"
	"go.uber.org/zap"
)

// ClientFactory 根据 Provider 和 API Key 动态创建 LLM 客户端
// 首期支持 OpenAI 兼容协议（openai / deepseek / custom）。
type ClientFactory struct {
	defaultTimeout time.Duration
	logger         *zap.Logger
}

// NewClientFactory 创建客户端工厂
func NewClientFactory(timeout time.Duration) *ClientFactory {
	return &ClientFactory{defaultTimeout: timeout, logger: zap.NewNop()}
}

// SetLogger 为工厂后续创建的客户端设置日志器
func (f *ClientFactory) SetLogger(logger *zap.Logger) {
	if logger != nil {
		f.logger = logger
	}
}

// Create 创建 LLM 客户端
// provider: openai / deepseek / qwen / custom
// apiKey: 明文 API Key
// baseURL: 可选，为空时使用 Provider 默认地址
//
// 注意：该函数用于 BYOK 直调路径，按 provider 推断协议。Ele Agent 代理路径请使用 CreateByProtocol。
func (f *ClientFactory) Create(provider, apiKey, baseURL string) (llm.Client, error) {
	switch provider {
	case "openai", "deepseek", "qwen", "custom":
		return f.CreateOpenAICompatibleClient(provider, apiKey, baseURL), nil
	default:
		return nil, fmt.Errorf("不支持的模型厂商: %s", provider)
	}
}

// CreateByProtocol 根据协议类型创建 LLM 客户端
// protocol: openai_compatible / anthropic_messages
// apiKey: 明文 API Key
// baseURL: 可选，为空时使用协议默认地址
//
// 该函数用于 Ele Agent 代理路径，允许管理员后台为同一品牌配置不同协议。
func (f *ClientFactory) CreateByProtocol(protocol, apiKey, baseURL string) (llm.Client, error) {
	// 空协议兜底为 OpenAI 兼容，兼容历史数据与旧测试配置
	if protocol == "" {
		protocol = string(model.EleAgentUpstreamOpenAICompatible)
	}
	switch protocol {
	case string(model.EleAgentUpstreamOpenAICompatible):
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return f.CreateOpenAICompatibleClient("custom", apiKey, baseURL), nil
	case string(model.EleAgentUpstreamAnthropicMessages):
		if baseURL == "" {
			baseURL = "https://api.anthropic.com/v1"
		}
		client := llm.NewAnthropicClient(apiKey, baseURL, f.defaultTimeout)
		client.SetLogger(f.logger)
		return client, nil
	default:
		return nil, fmt.Errorf("不支持的协议类型: %s", protocol)
	}
}

// CreateOpenAICompatibleClient 创建 OpenAI 兼容客户端（供 Ele Agent 代理直接调用）
func (f *ClientFactory) CreateOpenAICompatibleClient(provider, apiKey, baseURL string) llm.Client {
	if baseURL == "" {
		baseURL = defaultBaseURL(provider)
	}
	// 去除末尾斜杠，与 llm.NewOpenAIClient 内部一致
	baseURL = strings.TrimRight(baseURL, "/")
	client := llm.NewOpenAIClient(apiKey, baseURL, f.defaultTimeout)
	client.SetLogger(f.logger)
	return client
}

// defaultBaseURL 返回 Provider 默认 BaseURL
func defaultBaseURL(provider string) string {
	switch provider {
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "qwen":
		return "https://api.siliconflow.cn/v1"
	case "openai":
		return "https://api.openai.com/v1"
	default:
		return "https://api.openai.com/v1"
	}
}

// SupportedProviders 返回支持的 Provider 列表
func SupportedProviders() []map[string]string {
	return []map[string]string{
		{"provider": "qwen", "name": "通义千问", "default_base_url": "https://api.siliconflow.cn/v1"},
		{"provider": "openai", "name": "OpenAI", "default_base_url": "https://api.openai.com/v1"},
		{"provider": "deepseek", "name": "DeepSeek", "default_base_url": "https://api.deepseek.com/v1"},
		{"provider": "custom", "name": "自定义 OpenAI 兼容", "default_base_url": ""},
	}
}
