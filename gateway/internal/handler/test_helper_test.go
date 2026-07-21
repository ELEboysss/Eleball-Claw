package handler_test

import (
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/service"
)

// newTestEleAgentModelService 创建带有测试模型配置的 EleAgentModelService
func newTestEleAgentModelService() *service.EleAgentModelService {
	return service.NewTestEleAgentModelServiceWithConfigs([]*model.EleAgentModelConfig{
		{Provider: "openai", Protocol: model.EleAgentUpstreamOpenAICompatible, ModelName: "gpt-4o", BaseURL: "https://api.openai.com/v1", IsEnabled: true, Priority: 1},
		{Provider: "qwen", Protocol: model.EleAgentUpstreamOpenAICompatible, ModelName: "Qwen/Qwen3.5-4B", BaseURL: "https://api.siliconflow.cn/v1", IsEnabled: true, Priority: 0},
	})
}
