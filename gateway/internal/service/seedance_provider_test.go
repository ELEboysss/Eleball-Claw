package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eleball/gateway/internal/model"
)

// mockSeedanceServer 返回模拟火山方舟 Seedance 视频生成 API 的测试服务器，
// createResp 为创建任务响应，queryResp 为查询任务响应，captured 记录最近一次创建请求体。
func mockSeedanceServer(t *testing.T, createStatus int, createResp string, queryStatus int, queryResp string, captured *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-ark-key" {
			t.Errorf("鉴权头不对: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v3/contents/generations/tasks":
			if captured != nil {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, captured)
			}
			w.WriteHeader(createStatus)
			_, _ = w.Write([]byte(createResp))
		default:
			// GET /api/v3/contents/generations/tasks/{id}
			w.WriteHeader(queryStatus)
			_, _ = w.Write([]byte(queryResp))
		}
	}))
}

func TestSeedanceCreateTextToVideo(t *testing.T) {
	var captured map[string]interface{}
	server := mockSeedanceServer(t, 200, `{"id":"cgt-test-123","model":"doubao-seedance-1-0-pro-250528","status":"queued"}`, 200, `{}`, &captured)
	defer server.Close()

	p := NewSeedanceProvider(server.URL+"/api/v3", "test-ark-key")
	result, err := p.Create(context.Background(), &VisualCreateRequest{
		Model:  "doubao-seedance-1-0-pro-250528",
		Prompt: "海浪拍打礁石",
		Params: map[string]interface{}{
			"ratio":      "16:9",
			"duration":   float64(5),
			"resolution": "720p",
			"watermark":  false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.UpstreamTaskID != "cgt-test-123" {
		t.Errorf("任务 ID 解析不对: %s", result.UpstreamTaskID)
	}
	if result.Status != model.VisualTaskStatusPending {
		t.Errorf("queued 应映射为 pending: %s", result.Status)
	}

	// 请求体字段与协议对齐
	content, ok := captured["content"].([]interface{})
	if !ok || len(content) != 1 {
		t.Fatalf("content 应为单元素数组: %v", captured["content"])
	}
	part := content[0].(map[string]interface{})
	if part["type"] != "text" || part["text"] != "海浪拍打礁石" {
		t.Errorf("文本 part 不对: %v", part)
	}
	if captured["ratio"] != "16:9" || captured["resolution"] != "720p" {
		t.Errorf("ratio/resolution 不对: %v", captured)
	}
	if d, ok := captured["duration"].(float64); !ok || int(d) != 5 {
		t.Errorf("duration 不对: %v", captured["duration"])
	}
	if wm, ok := captured["watermark"].(bool); !ok || wm {
		t.Errorf("watermark 应为 false: %v", captured["watermark"])
	}
}

func TestSeedanceCreateWithFirstFrame(t *testing.T) {
	var captured map[string]interface{}
	server := mockSeedanceServer(t, 200, `{"id":"cgt-test-img","status":"queued"}`, 200, `{}`, &captured)
	defer server.Close()

	p := NewSeedanceProvider(server.URL+"/api/v3", "test-ark-key")
	_, err := p.Create(context.Background(), &VisualCreateRequest{
		Model:    "doubao-seedance-1-0-pro-250528",
		Prompt:   "让画面动起来",
		ImageURL: "https://x/first.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	content := captured["content"].([]interface{})
	if len(content) != 2 {
		t.Fatalf("应包含文本与首帧图两个 part: %v", content)
	}
	imgPart := content[1].(map[string]interface{})
	if imgPart["type"] != "image_url" {
		t.Fatalf("第二个 part 应为 image_url: %v", imgPart)
	}
	imgURL := imgPart["image_url"].(map[string]interface{})
	if imgURL["url"] != "https://x/first.png" {
		t.Errorf("首帧图 URL 不对: %v", imgURL)
	}
}

// TestSeedanceQuerySucceededContentObject 验证成功任务的 content 为对象结构（火山方舟真实响应），
// 修复前按数组解析会直接报「解析 Seedance 查询响应失败」。
func TestSeedanceQuerySucceededContentObject(t *testing.T) {
	server := mockSeedanceServer(t, 200, `{"id":"cgt-x","status":"succeeded"}`, 200, `{
		"id": "cgt-x",
		"status": "succeeded",
		"content": {"video_url": "https://tos.example.com/out.mp4"},
		"usage": {"completion_tokens": 103818, "total_tokens": 103818}
	}`, nil)
	defer server.Close()

	p := NewSeedanceProvider(server.URL+"/api/v3", "test-ark-key")
	result, err := p.Query(context.Background(), "cgt-x")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.VisualTaskStatusSucceeded {
		t.Errorf("状态应为 succeeded: %s", result.Status)
	}
	if result.Result == nil || result.Result.URL != "https://tos.example.com/out.mp4" {
		t.Fatalf("video_url 解析不对: %+v", result.Result)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 103818 {
		t.Errorf("usage 解析不对: %+v", result.Usage)
	}
}

func TestSeedanceQueryFailed(t *testing.T) {
	server := mockSeedanceServer(t, 200, `{}`, 200, `{
		"id": "cgt-x",
		"status": "failed",
		"error": {"code": "ContentFiltered", "message": "提示词不合规"}
	}`, nil)
	defer server.Close()

	p := NewSeedanceProvider(server.URL+"/api/v3", "test-ark-key")
	result, err := p.Query(context.Background(), "cgt-x")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.VisualTaskStatusFailed {
		t.Errorf("状态应为 failed: %s", result.Status)
	}
	if result.ErrorMessage == "" {
		t.Error("失败原因应带出 error.message")
	}
}

func TestSeedanceStatusMapping(t *testing.T) {
	cases := map[string]model.VisualTaskStatus{
		"queued":    model.VisualTaskStatusPending,
		"running":   model.VisualTaskStatusRunning,
		"succeeded": model.VisualTaskStatusSucceeded,
		"failed":    model.VisualTaskStatusFailed,
		"cancelled": model.VisualTaskStatusCancelled,
		"unknown":   model.VisualTaskStatusPending,
	}
	for in, want := range cases {
		if got := mapSeedanceStatus(in); got != want {
			t.Errorf("mapSeedanceStatus(%q) = %s, want %s", in, got, want)
		}
	}
}
