package service

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 一个 1x1 透明 PNG 的 base64 data URI，供图片附件测试复用
const testPNGDataURI = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

// newMultimodalTestSvc 构造带 mockRunner 的 AgentService，用于 buildAttachmentContentParts 测试。
func newMultimodalTestSvc(t *testing.T, ocrResult string, ocrErr error) *AgentService {
	t.Helper()
	agentSvc, _, _ := setupAgentService(t)
	runner := &mockRunner{ocrResult: ocrResult, ocrErr: ocrErr}
	search := &mockSearchProvider{result: map[string]interface{}{"results": []string{}}}
	agentSvc.registry = NewToolRegistryWithDeps(runner, search)
	return agentSvc
}

func TestDecodeDataURI(t *testing.T) {
	// base64 图片
	data, mime, err := decodeDataURI(testPNGDataURI)
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
	assert.NotEmpty(t, data)
	// 非 data URI
	_, _, err = decodeDataURI("https://example.com/a.png")
	assert.Error(t, err)
	// 缺逗号
	_, _, err = decodeDataURI("data:image/png;base64")
	assert.Error(t, err)
	// 纯文本 data URI（非 base64）
	data, mime, err = decodeDataURI("data:text/plain,hello")
	require.NoError(t, err)
	assert.Equal(t, "text/plain", mime)
	assert.Equal(t, "hello", string(data))
}

func TestDataURIDecodedSize(t *testing.T) {
	// base64 段长度 100，估算解码后约 75
	s := dataURIDecodedSize("data:image/png;base64," + repeatStr("A", 100))
	assert.InDelta(t, 75, s, 5)
	// 无逗号回退原长
	assert.Equal(t, 10, dataURIDecodedSize("0123456789"))
}

func repeatStr(s string, n int) string {
	return strings.Repeat(s, n)
}

func TestPreprocessAttachments(t *testing.T) {
	trigger := NewAgentTrigger()
	ctx := context.Background()

	// 图片：缺 dataUrl 报错
	_, err := trigger.PreprocessAttachments(ctx, []AgentAttachment{{ID: "1", Type: "image", Name: "a.png"}}, "c1")
	assert.Error(t, err)

	// 图片：正常透传
	out, err := trigger.PreprocessAttachments(ctx, []AgentAttachment{{ID: "1", Type: "image", Name: "a.png", DataURL: testPNGDataURI}}, "c1")
	require.NoError(t, err)
	assert.Equal(t, testPNGDataURI, out[0].DataURL)

	// 文本文件：从 data:text URI 解码出 Text
	textURI := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("hello world"))
	out, err = trigger.PreprocessAttachments(ctx, []AgentAttachment{{ID: "2", Type: "file", Name: "a.txt", DataURL: textURI}}, "c1")
	require.NoError(t, err)
	assert.Equal(t, "hello world", out[0].Text)

	// 超大文本拒绝
	big := repeatStr("x", MaxAttachmentTextBytes+10)
	_, err = trigger.PreprocessAttachments(ctx, []AgentAttachment{{ID: "3", Type: "file", Name: "big.txt", Text: big}}, "c1")
	assert.Error(t, err)

	// 空列表透传
	out, err = trigger.PreprocessAttachments(ctx, nil, "c1")
	require.NoError(t, err)
	assert.Nil(t, out)
}

func TestBuildAttachmentContentParts_VisionImage(t *testing.T) {
	svc := newMultimodalTestSvc(t, "", nil)
	parts := svc.buildAttachmentContentParts(context.Background(), []AgentAttachment{
		{ID: "1", Type: "image", Name: "a.png", DataURL: testPNGDataURI},
	}, true)
	require.Len(t, parts, 1)
	part := parts[0].(map[string]interface{})
	assert.Equal(t, "image_url", part["type"])
	iu := part["image_url"].(map[string]interface{})
	assert.Equal(t, testPNGDataURI, iu["url"])
}

func TestBuildAttachmentContentParts_NonVisionOCR(t *testing.T) {
	// 非视觉模型：图片走 OCR 降级，文本 part 注入识别结果
	svc := newMultimodalTestSvc(t, "识别到的文字内容", nil)
	parts := svc.buildAttachmentContentParts(context.Background(), []AgentAttachment{
		{ID: "1", Type: "image", Name: "a.png", DataURL: testPNGDataURI},
	}, false)
	require.Len(t, parts, 1)
	part := parts[0].(map[string]interface{})
	assert.Equal(t, "text", part["type"])
	assert.Contains(t, part["text"].(string), "识别到的文字内容")
	assert.Contains(t, part["text"].(string), "a.png")
}

func TestBuildAttachmentContentParts_OCRUnavailable(t *testing.T) {
	// OCR 不可用（tesseract 未装等）：降级为占位说明，不静默丢图
	svc := newMultimodalTestSvc(t, "", errors.New("ocr failed"))
	parts := svc.buildAttachmentContentParts(context.Background(), []AgentAttachment{
		{ID: "1", Type: "image", Name: "a.png", DataURL: testPNGDataURI},
	}, false)
	require.Len(t, parts, 1)
	part := parts[0].(map[string]interface{})
	assert.Equal(t, "text", part["type"])
	assert.Contains(t, part["text"].(string), "不支持图片理解")
}

func TestBuildAttachmentContentParts_TextFile(t *testing.T) {
	svc := newMultimodalTestSvc(t, "", nil)
	parts := svc.buildAttachmentContentParts(context.Background(), []AgentAttachment{
		{ID: "1", Type: "file", Name: "a.txt", Text: "文件正文内容"},
	}, false)
	require.Len(t, parts, 1)
	part := parts[0].(map[string]interface{})
	assert.Equal(t, "text", part["type"])
	assert.Contains(t, part["text"].(string), "文件正文内容")
	assert.Contains(t, part["text"].(string), "a.txt")
}

func TestBuildAttachmentContentParts_BinaryFile(t *testing.T) {
	// 二进制文件（无 text）：占位说明，引导用工具处理
	svc := newMultimodalTestSvc(t, "", nil)
	parts := svc.buildAttachmentContentParts(context.Background(), []AgentAttachment{
		{ID: "1", Type: "file", Name: "a.bin", DataURL: "data:application/octet-stream;base64,AAAA"},
	}, true)
	require.Len(t, parts, 1)
	part := parts[0].(map[string]interface{})
	assert.Equal(t, "text", part["type"])
	assert.Contains(t, part["text"].(string), "二进制文件")
}
