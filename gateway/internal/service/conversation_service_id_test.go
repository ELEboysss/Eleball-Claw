package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationService_GetOrCreate_PreservesClientID(t *testing.T) {
	_, convSvc, _ := setupAgentService(t)
	ctx := context.Background()
	clientID := "client-conv-123"

	conv, err := convSvc.GetOrCreate(ctx, "u2", clientID)
	require.NoError(t, err)
	assert.Equal(t, clientID, conv.ID)

	// 再次获取应返回同一个对话
	conv2, err := convSvc.GetOrCreate(ctx, "u2", clientID)
	require.NoError(t, err)
	assert.Equal(t, clientID, conv2.ID)
}

func TestConversationService_GetOrCreate_EmptyClientID_GeneratesID(t *testing.T) {
	_, convSvc, _ := setupAgentService(t)
	ctx := context.Background()

	conv, err := convSvc.GetOrCreate(ctx, "u2", "")
	require.NoError(t, err)
	assert.NotEmpty(t, conv.ID)
	assert.Contains(t, conv.ID, "conv-")
}
