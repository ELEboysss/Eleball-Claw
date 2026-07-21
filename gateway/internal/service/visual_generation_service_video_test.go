package service

import (
	"testing"

	"github.com/eleball/gateway/internal/model"
)

func TestExtractResultURLForMedia_VideoRequiresCoverURL(t *testing.T) {
	// 视频任务没有 cover_url 时，不能回退到视频 URL 作为 image 参考。
	result := &VisualResult{
		URL:     "https://example.com/video.mp4",
		CoverURL: "",
	}
	if got := extractResultURLForMedia(result, model.VisualMediaTypeVideo); got != "" {
		t.Fatalf("视频无封面时应返回空，实际 %s", got)
	}

	// 有 cover_url 时返回封面 URL。
	result.CoverURL = "https://example.com/cover.jpg"
	if got := extractResultURLForMedia(result, model.VisualMediaTypeVideo); got != result.CoverURL {
		t.Fatalf("视频有封面时应返回封面 URL，实际 %s", got)
	}
}

func TestExtractResultURLForMedia_ImageUsesURL(t *testing.T) {
	result := &VisualResult{
		URL: "https://example.com/image.png",
	}
	if got := extractResultURLForMedia(result, model.VisualMediaTypeImage); got != result.URL {
		t.Fatalf("图片应返回 URL，实际 %s", got)
	}
}
