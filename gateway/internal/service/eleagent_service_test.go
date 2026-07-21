package service

import (
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/stretchr/testify/assert"
)

func testEleAgentModelService() *EleAgentModelService {
	return NewTestEleAgentModelServiceWithConfigs([]*model.EleAgentModelConfig{
		{Provider: "openai", Protocol: model.EleAgentUpstreamOpenAICompatible, ModelName: "gpt-4o", BaseURL: "https://api.openai.com/v1", IsEnabled: true},
		{Provider: "qwen", Protocol: model.EleAgentUpstreamOpenAICompatible, ModelName: "Qwen/Qwen3-8B", BaseURL: "https://api.siliconflow.cn/v1", IsEnabled: true},
	})
}

func setupEleAgentService(t *testing.T) *EleAgentService {
	chatService := NewChatProxyService(nil, nil, testEleAgentModelService(), nil)
	return NewEleAgentService(chatService, testEleAgentModelService(), nil, "http://localhost:8080/v1")
}

func TestEleAgentService_GetCredentials_Success(t *testing.T) {
	svc := setupEleAgentService(t)

	creds, err := svc.GetCredentials("user-1", "openai", "gpt-4o")

	assert.NoError(t, err)
	assert.NotNil(t, creds)
	assert.Equal(t, "http://localhost:8080/v1", creds.BaseURL)
	assert.NotEmpty(t, creds.APIKey)
	assert.True(t, len(creds.APIKey) > len("eleagent_"))
	assert.False(t, creds.ExpiresAt.IsZero())
}

func TestEleAgentService_GetCredentials_EmptyParams(t *testing.T) {
	svc := setupEleAgentService(t)

	_, err := svc.GetCredentials("user-1", "", "gpt-4o")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "子平台 Provider 和模型名不能为空")

	_, err = svc.GetCredentials("user-1", "openai", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "子平台 Provider 和模型名不能为空")
}

func TestEleAgentService_GetCredentials_UnsupportedProvider(t *testing.T) {
	svc := setupEleAgentService(t)

	_, err := svc.GetCredentials("user-1", "nonexist", "model")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不支持的 Ele Agent 模型")
}

func TestEleAgentService_DefaultBaseURL(t *testing.T) {
	chatService := NewChatProxyService(nil, nil, testEleAgentModelService(), nil)
	// 传入空字符串时使用默认生产地址
	svc := NewEleAgentService(chatService, testEleAgentModelService(), nil, "")

	creds, err := svc.GetCredentials("user-1", "openai", "gpt-4o")
	assert.NoError(t, err)
	assert.Equal(t, "https://api.eleball.cn/v1", creds.BaseURL)
}
