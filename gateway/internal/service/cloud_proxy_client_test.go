package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eleball/gateway/pkg/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockCloudGateway 模拟云端网关 /v1/chat/completions 的真实响应格式：
//   - 非流式：私有信封 {"code":0,"data":{"delta":...}}（非标准 OpenAI 结构）
//   - 流式：标准 OpenAI SSE（choices delta + [DONE]）
//
// 该格式差异曾导致 claw 以 openai_compatible 协议调云端代理时报「空响应」，
// 本测试固定该契约，防止云端响应格式或 claw 解析逻辑任何一侧回归。
func newMockCloudGateway(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		if strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"流式"}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"聚合"}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		// 非流式：云端私有信封（标准 OpenAI 客户端解析不出 choices）
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":0,"message":"success","data":{"delta":"信封回复","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}}`)
	}))
}

func chatReq() llm.ChatRequest {
	return llm.ChatRequest{
		Model:    "k3",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	}
}

// TestCloudGatewayEnvelopeBreaksPlainOpenAIClient 回归证据：
// 未经包装的标准 OpenAI 客户端调云端非流式接口会因解析不出内容报「空响应」
// （即用户实际遇到的历史报错，本测试固定该行为防止两侧格式再度漂移）。
func TestCloudGatewayEnvelopeBreaksPlainOpenAIClient(t *testing.T) {
	srv := newMockCloudGateway(t)
	defer srv.Close()

	client := llm.NewOpenAIClient("token", srv.URL+"/v1", 10*time.Second)
	_, err := client.Chat(context.Background(), chatReq())
	require.Error(t, err, "私有信封在标准 OpenAI 解析下应报错（历史 bug 的固定）")
	assert.Contains(t, err.Error(), "空响应")
}

// TestCloudEnvelopeCompatClientChat 包装后非流式 Chat 聚合 SSE，内容与用量完整。
func TestCloudEnvelopeCompatClientChat(t *testing.T) {
	srv := newMockCloudGateway(t)
	defer srv.Close()

	inner := llm.NewOpenAIClient("token", srv.URL+"/v1", 10*time.Second)
	client := &cloudEnvelopeCompatClient{inner: inner}

	chunk, err := client.Chat(context.Background(), chatReq())
	require.NoError(t, err)
	assert.Equal(t, "流式聚合", chunk.Delta)
	require.NotNil(t, chunk.Usage)
	assert.Equal(t, 3, chunk.Usage.TotalTokens)
	assert.Equal(t, "stop", chunk.FinishReason)
}

// TestCloudEnvelopeCompatClientStream 包装后流式原样透传，逐 chunk 可读。
func TestCloudEnvelopeCompatClientStream(t *testing.T) {
	srv := newMockCloudGateway(t)
	defer srv.Close()

	inner := llm.NewOpenAIClient("token", srv.URL+"/v1", 10*time.Second)
	client := &cloudEnvelopeCompatClient{inner: inner}

	chunkChan, err := client.ChatStream(context.Background(), chatReq())
	require.NoError(t, err)
	var sb strings.Builder
	for chunk := range chunkChan {
		sb.WriteString(chunk.Delta)
	}
	assert.Equal(t, "流式聚合", sb.String())
}

// TestWrapCloudEnvelopeCompat 仅云端代理凭据触发包装，普通 BYOK 上游不受影响。
func TestWrapCloudEnvelopeCompat(t *testing.T) {
	inner := llm.NewOpenAIClient("k", "http://example.com/v1", time.Second)

	cloudCred := &EleAgentModelCredential{CloudProxy: true}
	wrapped := wrapCloudEnvelopeCompat(cloudCred, inner)
	_, ok := wrapped.(*cloudEnvelopeCompatClient)
	assert.True(t, ok, "云端代理凭据应包装")

	byokCred := &EleAgentModelCredential{CloudProxy: false}
	assert.Same(t, inner, wrapCloudEnvelopeCompat(byokCred, inner), "BYOK 凭据不应包装")
	assert.Same(t, inner, wrapCloudEnvelopeCompat(nil, inner), "空凭据不应包装")
}
