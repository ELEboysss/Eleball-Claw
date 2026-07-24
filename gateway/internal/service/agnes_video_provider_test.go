package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeAgnesNumFrames Agnes 要求 num_frames = 8*n+1，规整结果必须恒满足。
// 覆盖 duration×fps 常见产物（5s/10s@24fps = 120/240 等历史报错场景）。
func TestNormalizeAgnesNumFrames(t *testing.T) {
	cases := map[int]int{
		120: 121, // 5s@24fps：历史报错 num_frames must be 8*n+1 的直接来源
		240: 241, // 10s@24fps
		81:  81,  // 已合法
		1:   9,   // 下限保护
		8:   9,
		116: 113, // 就近向下
		118: 121, // 就近向上
	}
	for in, want := range cases {
		assert.Equal(t, want, normalizeAgnesNumFrames(in), "frames=%d", in)
	}
	// 任意输入规整后恒满足 8*n+1
	for f := 1; f <= 500; f++ {
		got := normalizeAgnesNumFrames(f)
		assert.Equal(t, 0, (got-1)%8, "frames=%d got=%d 不满足 8*n+1", f, got)
	}
}

// TestAgnesVideoCreateTaskNormalizesNumFrames 端到端：duration=5s 时
// 发往上游的 num_frames 必须是合法的 8*n+1（121），而非裸乘积 120。
func TestAgnesVideoCreateTaskNormalizesNumFrames(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"t1","status":"pending"}`))
	}))
	defer srv.Close()

	p := NewAgnesVideoProvider(srv.URL, srv.URL, "sk-test")
	_, err := p.Create(context.Background(), &VisualCreateRequest{
		Model:  "agnes-video-v2.0",
		Prompt: "test",
		Params: map[string]interface{}{"duration": float64(5)},
	})
	require.NoError(t, err)
	require.NotNil(t, gotBody)
	frames := int(gotBody["num_frames"].(float64))
	assert.Equal(t, 121, frames)
	assert.Equal(t, 0, (frames-1)%8, "num_frames 必须满足 8*n+1")
}
