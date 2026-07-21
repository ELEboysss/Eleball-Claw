//go:build live

package service

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
)

// TestSeedreamLiveCreate 火山方舟 Seedream 真实接口冒烟测试。
// 运行方式（Key 通过环境变量传入，不入库）：
//   SEEDREAM_LIVE_KEY=<ark key> SEEDREAM_LIVE_MODEL=doubao-seedream-4-0-250828 \
//     go test -tags live -run TestSeedreamLiveCreate -v ./internal/service/
// 会产生一次真实图片生成调用（计费），size 固定 1K 控制成本。
// 使用 b64_json 返回并校验图片魔数，避免 TOS 签名链接转存环节干扰验证。
func TestSeedreamLiveCreate(t *testing.T) {
	key := os.Getenv("SEEDREAM_LIVE_KEY")
	if key == "" {
		t.Skip("未设置 SEEDREAM_LIVE_KEY，跳过真实接口测试")
	}
	model := os.Getenv("SEEDREAM_LIVE_MODEL")
	if model == "" {
		model = "doubao-seedream-4-5-251128"
	}

	p := NewSeedreamProvider("", key)
	result, err := p.Create(context.Background(), &VisualCreateRequest{
		Model:  model,
		Prompt: "一只橘猫趴在窗台晒太阳，暖色调，摄影质感",
		Params: map[string]interface{}{"size": "1K", "response_format": "b64_json"},
	})
	if err != nil {
		t.Fatalf("Seedream 真实调用失败: %v", err)
	}
	if result.Result == nil || result.Result.B64JSON == "" {
		t.Fatalf("未返回 b64_json 图片: %+v", result)
	}
	raw, err := base64.StdEncoding.DecodeString(result.Result.B64JSON)
	if err != nil {
		t.Fatalf("b64 解码失败: %v", err)
	}
	// JPEG 魔数 FFD8 或 PNG 魔数 8950
	isJPEG := len(raw) > 2 && raw[0] == 0xFF && raw[1] == 0xD8
	isPNG := len(raw) > 2 && raw[0] == 0x89 && raw[1] == 0x50
	if !isJPEG && !isPNG {
		t.Fatalf("返回内容不是有效图片（前 4 字节: %x）", raw[:min(4, len(raw))])
	}
	tokens := 0
	if result.Usage != nil {
		tokens = result.Usage.TotalTokens
	}
	t.Logf("生成成功: bytes=%d format=%s tokens=%d dataURL前缀=%.40s", len(raw), map[bool]string{true: "jpeg", false: "png"}[isJPEG], tokens, result.Result.URL)

	// 落盘供人工查看（可选，路径由 SEEDREAM_LIVE_OUT 指定）
	if out := os.Getenv("SEEDREAM_LIVE_OUT"); out != "" {
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Logf("写文件失败（不影响验证）: %v", err)
		} else {
			t.Logf("图片已保存: %s", out)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

