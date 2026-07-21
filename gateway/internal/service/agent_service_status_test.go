package service

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/eleball/gateway/pkg/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentService_Execute_UpdatesSessionStatus_Success(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	client := &mockAgentLLM{responses: []llm.ChatChunk{{Delta: "hello"}}}
	agentSvc.clientResolver = func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return client, nil
	}

	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u2")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "hi", EnableTools: boolPtr(true)}, &buf)
	require.NoError(t, err)

	items, total, err := agentSvc.ListSessions(ctx, "u2", 1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	assert.Equal(t, "succeeded", items[0].Status)
	assert.NotNil(t, items[0].CompletedAt)
}

func TestAgentService_Execute_UpdatesSessionStatus_Failure(t *testing.T) {
	agentSvc, _, _ := setupAgentService(t)
	agentSvc.clientResolver = func(ctx context.Context, provider, model, baseURL, apiKey string) (AgentLLMClient, error) {
		return nil, fmt.Errorf("resolver error")
	}

	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), "user_id", "u2")
	err := agentSvc.Execute(ctx, AgentExecuteRequest{Message: "hi", EnableTools: boolPtr(true)}, &buf)
	require.NoError(t, err)

	items, total, err := agentSvc.ListSessions(ctx, "u2", 1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	assert.Equal(t, "failed", items[0].Status)
	assert.NotNil(t, items[0].CompletedAt)
}
