package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eleball/gateway/pkg/llm"
)

// mockSeedreamServer 返回模拟火山方舟图片生成 API 的测试服务器，并记录最近一次请求体
func mockSeedreamServer(t *testing.T, status int, respBody string, captured *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/images/generations" {
			t.Errorf("请求路径不对: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-ark-key" {
			t.Errorf("鉴权头不对: %s", r.Header.Get("Authorization"))
		}
		if captured != nil {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, captured)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
}

func TestSeedreamCreateTextToImageDefaults(t *testing.T) {
	var captured map[string]interface{}
	server := mockSeedreamServer(t, 200, `{
		"created": 1720000000,
		"data": [{"url": "https://tos.example.com/img1.png", "size": "2048x2048"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 100, "total_tokens": 110}
	}`, &captured)
	defer server.Close()

	p := NewSeedreamProvider(server.URL+"/api/v3", "test-ark-key")
	result, err := p.Create(context.Background(), &VisualCreateRequest{
		Model:  "doubao-seedream-4-5-251128",
		Prompt: "一只在草地上的狗",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 请求体默认值：size=2K、response_format=url、watermark=false、无 image 字段
	if captured["model"] != "doubao-seedream-4-5-251128" {
		t.Errorf("model 不对: %v", captured["model"])
	}
	if captured["size"] != "2K" {
		t.Errorf("默认 size 应为 2K: %v", captured["size"])
	}
	if captured["response_format"] != "url" {
		t.Errorf("默认 response_format 应为 url: %v", captured["response_format"])
	}
	if wm, ok := captured["watermark"].(bool); !ok || wm {
		t.Errorf("默认 watermark 应为 false: %v", captured["watermark"])
	}
	if _, hasImage := captured["image"]; hasImage {
		t.Errorf("文生图不应带 image 字段: %v", captured["image"])
	}

	// 响应解析
	if result.Status != "succeeded" || result.Result.URL != "https://tos.example.com/img1.png" {
		t.Errorf("结果解析不对: %+v", result)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 110 {
		t.Errorf("usage 解析不对: %+v", result.Usage)
	}
	if len(result.Result.URLs) != 1 {
		t.Errorf("URLs 应包含全部结果: %v", result.Result.URLs)
	}
}

func TestSeedreamCreateImageToImageMulti(t *testing.T) {
	var captured map[string]interface{}
	server := mockSeedreamServer(t, 200, `{"created":1,"data":[{"url":"https://x/1.png"},{"url":"https://x/2.png"}]}`, &captured)
	defer server.Close()

	p := NewSeedreamProvider(server.URL+"/api/v3", "test-ark-key")
	_, err := p.Create(context.Background(), &VisualCreateRequest{
		Model:     "doubao-seedream-4-5-251128",
		Prompt:    "把两个人合成合影",
		ImageURL:  "https://x/a.png",
		ImageURLs: []string{"https://x/b.png"},
		Params:    map[string]interface{}{"ratio": "16:9"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 多图 → image 为数组；ratio 16:9 → size 2560x1440
	images, ok := captured["image"].([]interface{})
	if !ok || len(images) != 2 {
		t.Errorf("多图应为数组且含 2 张: %v", captured["image"])
	}
	if captured["size"] != "2560x1440" {
		t.Errorf("ratio 16:9 应映射为 2560x1440: %v", captured["size"])
	}
}

func TestSeedreamCreateSingleImageString(t *testing.T) {
	var captured map[string]interface{}
	server := mockSeedreamServer(t, 200, `{"created":1,"data":[{"url":"https://x/1.png"}]}`, &captured)
	defer server.Close()

	p := NewSeedreamProvider(server.URL+"/api/v3", "test-ark-key")
	_, err := p.Create(context.Background(), &VisualCreateRequest{
		Model:    "doubao-seedream-4-5-251128",
		Prompt:   "换个背景",
		ImageURL: "https://x/only.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured["image"] != "https://x/only.png" {
		t.Errorf("单图应为字符串: %v", captured["image"])
	}
}

func TestSeedreamCreateRateLimited(t *testing.T) {
	server := mockSeedreamServer(t, 429, `{"error":{"code":"RateLimitExceeded","message":"too fast"}}`, nil)
	defer server.Close()

	p := NewSeedreamProvider(server.URL+"/api/v3", "test-ark-key")
	_, err := p.Create(context.Background(), &VisualCreateRequest{Model: "m", Prompt: "x"})
	if !errors.Is(err, UpstreamRateLimitedError) {
		t.Fatalf("429 应映射为 UpstreamRateLimitedError: %v", err)
	}
}

func TestSeedreamCreateServerErrorRetryable(t *testing.T) {
	server := mockSeedreamServer(t, 503, `{"error":{"code":"ServiceUnavailable","message":"model overloaded"}}`, nil)
	defer server.Close()

	p := NewSeedreamProvider(server.URL+"/api/v3", "test-ark-key")
	_, err := p.Create(context.Background(), &VisualCreateRequest{Model: "m", Prompt: "x"})
	if err == nil {
		t.Fatal("503 应报错")
	}
	// 必须是类型化上游错误，业务层据此自动重试
	if llm.UpstreamStatusCode(err) != 503 {
		t.Fatalf("应返回 UpstreamError(503): %v", err)
	}
	if !llm.IsRetryableUpstreamErr(err) {
		t.Fatalf("503 应判定为可重试: %v", err)
	}
	var ue *llm.UpstreamError
	if !errors.As(err, &ue) || ue.Body != "ServiceUnavailable: model overloaded" {
		t.Fatalf("错误体应提取方舟错误码与信息: %v", err)
	}
}

func TestSeedreamCreateB64Fallback(t *testing.T) {
	server := mockSeedreamServer(t, 200, `{"created":1,"data":[{"b64_json":"aGVsbG8="}]}`, nil)
	defer server.Close()

	p := NewSeedreamProvider(server.URL+"/api/v3", "test-ark-key")
	result, err := p.Create(context.Background(), &VisualCreateRequest{
		Model:  "m",
		Prompt: "x",
		Params: map[string]interface{}{"response_format": "b64_json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.URL != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("b64 应回退为 data URL: %s", result.Result.URL)
	}
}
