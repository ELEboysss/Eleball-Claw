package service

import (
	"context"
	"strings"

	"github.com/eleball/gateway/pkg/llm"
)

// cloudEnvelopeCompatClient 云端网关信封兼容包装。
//
// 背景：云端网关（api.eleball.cn）/v1/chat/completions 的非流式响应是私有信封
// {code, message, data:{delta, usage}}，不是标准 OpenAI 结构（choices[0].message.content），
// 通用 OpenAI 客户端按标准格式解析会得到空内容（报「空响应」并带原始信封）；
// 而云端的 SSE 流式响应是标准 OpenAI 格式（toOpenAIStreamChunk + [DONE]）。
//
// 因此对指向云端的代理配置（credential.CloudProxy）：
//   - Chat：改走上游 SSE，把分片聚合成完整响应（对外仍是非流式语义）
//   - ChatStream：原样透传（上游本就标准）
type cloudEnvelopeCompatClient struct {
	inner llm.Client
}

// wrapCloudEnvelopeCompat 若凭据指向云端代理，返回信封兼容包装；否则原样返回
func wrapCloudEnvelopeCompat(credential *EleAgentModelCredential, client llm.Client) llm.Client {
	if credential != nil && credential.CloudProxy {
		return &cloudEnvelopeCompatClient{inner: client}
	}
	return client
}

// Chat 聚合上游 SSE 分片为完整响应
func (c *cloudEnvelopeCompatClient) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatChunk, error) {
	chunkChan, err := c.inner.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}

	var content strings.Builder
	var reasoning strings.Builder
	var usage *llm.Usage
	var toolCalls []llm.ToolCall
	finishReason := ""
	for chunk := range chunkChan {
		content.WriteString(chunk.Delta)
		reasoning.WriteString(chunk.ReasoningContent)
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.ToolCalls) > 0 {
			toolCalls = chunk.ToolCalls
		}
		if chunk.FinishReason != "" {
			finishReason = chunk.FinishReason
		}
	}
	if usage == nil {
		usage = llm.EstimateUsageFromMessages(req.Messages, content.String())
	}
	return &llm.ChatChunk{
		Delta:            content.String(),
		ReasoningContent: reasoning.String(),
		Usage:            usage,
		ToolCalls:        toolCalls,
		FinishReason:     finishReason,
	}, nil
}

// ChatStream 云端 SSE 本就是标准 OpenAI 格式，原样透传
func (c *cloudEnvelopeCompatClient) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	return c.inner.ChatStream(ctx, req)
}
