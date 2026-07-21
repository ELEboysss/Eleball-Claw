package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestBingSearchProvider_Search(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertEqual(t, "GET", r.Method)
		assertEqual(t, "test-key", r.Header.Get("Ocp-Apim-Subscription-Key"))
		resp := map[string]interface{}{
			"webPages": map[string]interface{}{
				"value": []map[string]string{
					{"name": "Go", "url": "https://go.dev", "snippet": "The Go programming language"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	os.Setenv("BING_SEARCH_API_KEY", "test-key")
	os.Setenv("BING_SEARCH_ENDPOINT", server.URL)
	defer os.Unsetenv("BING_SEARCH_API_KEY")
	defer os.Unsetenv("BING_SEARCH_ENDPOINT")

	provider := &BingSearchProvider{}
	result, err := provider.Search(context.Background(), "golang")
	if err != nil {
		t.Fatalf("Bing 搜索失败: %v", err)
	}
	results := result["results"].([]SearchResult)
	if len(results) != 1 {
		t.Fatalf("结果数量不对: %v", results)
	}
	if results[0].Title != "Go" {
		t.Fatalf("标题不对: %v", results[0].Title)
	}
}

func TestBingSearchProvider_MissingKey(t *testing.T) {
	os.Unsetenv("BING_SEARCH_API_KEY")
	provider := &BingSearchProvider{}
	_, err := provider.Search(context.Background(), "golang")
	if err == nil {
		t.Fatal("缺少 API Key 应报错")
	}
}

func TestSearXNGProvider_Search(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertEqual(t, "GET", r.Method)
		resp := map[string]interface{}{
			"results": []map[string]string{
				{"title": "Go", "url": "https://go.dev", "content": "The Go programming language"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	os.Setenv("SEARXNG_URL", server.URL)
	defer os.Unsetenv("SEARXNG_URL")

	provider := &SearXNGProvider{}
	result, err := provider.Search(context.Background(), "golang")
	if err != nil {
		t.Fatalf("SearXNG 搜索失败: %v", err)
	}
	results := result["results"].([]SearchResult)
	if len(results) != 1 {
		t.Fatalf("结果数量不对: %v", results)
	}
}

func TestSearXNGProvider_MissingURL(t *testing.T) {
	os.Unsetenv("SEARXNG_URL")
	provider := &SearXNGProvider{}
	_, err := provider.Search(context.Background(), "golang")
	if err == nil {
		t.Fatal("缺少 URL 应报错")
	}
}

func TestDuckDuckGoProvider_ParseResults(t *testing.T) {
	html := `<html><body><a class="result__a" href="https://go.dev">Go Programming Language</a></body></html>`
	results := parseDuckDuckGoResults(html)
	if len(results) != 1 {
		t.Fatalf("解析结果数量不对: %v", results)
	}
	if results[0].Title != "Go Programming Language" {
		t.Fatalf("标题解析不对: %v", results[0].Title)
	}
	if results[0].URL != "https://go.dev" {
		t.Fatalf("URL 解析不对: %v", results[0].URL)
	}
}

func TestDuckDuckGoProvider_ParseRedirectURL(t *testing.T) {
	html := `<html><body><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev">Go</a></body></html>`
	results := parseDuckDuckGoResults(html)
	if len(results) != 1 {
		t.Fatalf("解析结果数量不对: %v", results)
	}
	if results[0].URL != "https://go.dev" {
		t.Fatalf("重定向 URL 解析不对: %v", results[0].URL)
	}
}

func TestNewSearchProvider_Default(t *testing.T) {
	os.Unsetenv("BAIDU_API_KEY")
	os.Unsetenv("BING_SEARCH_API_KEY")
	os.Unsetenv("SEARXNG_URL")
	sp := NewSearchProvider()
	if sp.Name() != "dummy" {
		t.Fatalf("没有任何 key 时应为 dummy provider: %v", sp.Name())
	}
}

func TestGetSearchProvider(t *testing.T) {
	cases := []struct {
		name     string
		expected string
	}{
		{"baidu", "baidu"},
		{"Baidu", "baidu"},
		{"BING", "bing"},
		{"searxng", "searxng"},
		{"duckduckgo", "duckduckgo"},
		{"", "dummy"},
		{"unknown", "dummy"},
	}
	for _, c := range cases {
		sp := GetSearchProvider(c.name)
		if sp.Name() != c.expected {
			t.Fatalf("GetSearchProvider(%q) 期望 %s，实际 %s", c.name, c.expected, sp.Name())
		}
	}
}

func TestListSearchProviders(t *testing.T) {
	providers := ListSearchProviders()
	if len(providers) != 2 {
		t.Fatalf("前端可选搜索源应为 2 个，实际 %d", len(providers))
	}
	names := []string{providers[0].Name(), providers[1].Name()}
	if names[0] != "baidu" || names[1] != "bing" {
		t.Fatalf("可选源应为 baidu/bing，实际 %v", names)
	}
}

func TestNewSearchProvider_Baidu(t *testing.T) {
	os.Setenv("BAIDU_API_KEY", "test-key")
	defer os.Unsetenv("BAIDU_API_KEY")
	sp := NewSearchProvider()
	if sp.Name() != "baidu" {
		t.Fatalf("应创建 baidu provider: %v", sp.Name())
	}
}

func TestBaiduSearchProvider_Search(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertEqual(t, "POST", r.Method)
		assertEqual(t, "Bearer test-key", r.Header.Get("Authorization"))
		assertEqual(t, "application/json", r.Header.Get("Content-Type"))
		resp := map[string]interface{}{
			"references": []map[string]string{
				{"title": "Go", "url": "https://go.dev", "snippet": "The Go programming language", "content": "详情"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	os.Setenv("BAIDU_API_KEY", "test-key")
	os.Setenv("BAIDU_SEARCH_ENDPOINT", server.URL)
	defer os.Unsetenv("BAIDU_API_KEY")
	defer os.Unsetenv("BAIDU_SEARCH_ENDPOINT")

	provider := &BaiduSearchProvider{}
	result, err := provider.Search(context.Background(), "golang")
	if err != nil {
		t.Fatalf("百度 AI 搜索失败: %v", err)
	}
	results := result["results"].([]SearchResult)
	if len(results) != 1 {
		t.Fatalf("结果数量不对: %v", results)
	}
	if results[0].Title != "Go" {
		t.Fatalf("标题不对: %v", results[0].Title)
	}
}

func TestBaiduSearchProvider_FallbackContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"references": []map[string]string{
				{"title": "Go", "url": "https://go.dev", "content": "使用 content 兜底"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	os.Setenv("BAIDU_API_KEY", "test-key")
	os.Setenv("BAIDU_SEARCH_ENDPOINT", server.URL)
	defer os.Unsetenv("BAIDU_API_KEY")
	defer os.Unsetenv("BAIDU_SEARCH_ENDPOINT")

	provider := &BaiduSearchProvider{}
	result, err := provider.Search(context.Background(), "golang")
	if err != nil {
		t.Fatalf("百度 AI 搜索失败: %v", err)
	}
	results := result["results"].([]SearchResult)
	if results[0].Snippet != "使用 content 兜底" {
		t.Fatalf("content 兜底不对: %v", results[0].Snippet)
	}
}

func TestBaiduSearchProvider_MissingKey(t *testing.T) {
	os.Unsetenv("BAIDU_API_KEY")
	provider := &BaiduSearchProvider{}
	_, err := provider.Search(context.Background(), "golang")
	if err == nil {
		t.Fatal("缺少 API Key 应报错")
	}
}

func assertEqual(t *testing.T, expected, actual string) {
	if expected != actual {
		t.Fatalf("期望 %s，实际 %s", expected, actual)
	}
}
