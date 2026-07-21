package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// searchHTTPClient 所有搜索源统一使用带超时的 HTTP 客户端，避免上游挂起导致 Agent 工作流无限等待
var searchHTTPClient = &http.Client{Timeout: 15 * time.Second}

// SearchProvider 搜索服务抽象，便于切换国内/国际搜索源
type SearchProvider interface {
	Search(ctx context.Context, query string) (map[string]interface{}, error)
	Name() string
}

// NewSearchProvider 创建搜索提供者
// 优先返回第一个已配置可用 key 的源；没有任何源可用时返回 DummyProvider 并提示配置
func NewSearchProvider() SearchProvider {
	return GetSearchProvider(GetFirstAvailableSearchProvider())
}

// GetSearchProvider 根据名称创建搜索提供者
// 支持：bing、baidu、searxng、duckduckgo；默认返回提示配置的 DummyProvider
func GetSearchProvider(name string) SearchProvider {
	provider := strings.ToLower(strings.TrimSpace(name))
	switch provider {
	case "bing":
		return &BingSearchProvider{}
	case "baidu":
		return &BaiduSearchProvider{}
	case "searxng":
		return &SearXNGProvider{}
	case "duckduckgo":
		return &DuckDuckGoProvider{}
	default:
		return &DummySearchProvider{}
	}
}

// SearchProviderItem 前端可选项，包含展示名称
type SearchProviderItem struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// ListSearchProviders 返回前端可选择的搜索源列表
// 目前只开放 baidu（国内免费额度）和 bing（付费稳定）
func ListSearchProviders() []SearchProvider {
	return []SearchProvider{
		&BaiduSearchProvider{},
		&BingSearchProvider{},
	}
}

// IsSearchProviderAvailable 判断指定搜索源是否已配置可用
// 前端下拉框只展示已配置 key 的源，未配置的不展示
func IsSearchProviderAvailable(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "baidu":
		return os.Getenv("BAIDU_API_KEY") != ""
	case "bing":
		return os.Getenv("BING_SEARCH_API_KEY") != ""
	case "searxng":
		return os.Getenv("SEARXNG_URL") != ""
	case "duckduckgo":
		// DuckDuckGo 无需 API Key，但国内云服务器通常无法稳定访问，
		// 因此不作为前端可选项，也不作为默认源；后端保留协议兼容
		return false
	default:
		return false
	}
}

// GetFirstAvailableSearchProvider 返回第一个已配置可用 key 的搜索源名称
// 用于 Agent 工作流默认搜索源兜底，避免依赖已弃用的 SEARCH_PROVIDER 环境变量
func GetFirstAvailableSearchProvider() string {
	providers := []string{"baidu", "bing", "searxng", "duckduckgo"}
	for _, name := range providers {
		if IsSearchProviderAvailable(name) {
			return name
		}
	}
	return ""
}

// ListAvailableSearchProviders 返回前端可选择的、已配置可用的搜索源列表
// 前端目前只开放 baidu / bing，因此仅检查这两个源是否已配置 key
func ListAvailableSearchProviders() []SearchProviderItem {
	all := []SearchProviderItem{
		{Name: "baidu", Label: "百度"},
		{Name: "bing", Label: "Bing"},
	}
	var available []SearchProviderItem
	for _, item := range all {
		if IsSearchProviderAvailable(item.Name) {
			available = append(available, item)
		}
	}
	return available
}

// SearchResult 统一搜索结果项
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// DummySearchProvider 未配置搜索源时的占位提供者
type DummySearchProvider struct{}

func (d *DummySearchProvider) Name() string { return "dummy" }
func (d *DummySearchProvider) Search(ctx context.Context, query string) (map[string]interface{}, error) {
	return map[string]interface{}{
		"results": []SearchResult{
			{
				Title:   "搜索服务未配置",
				URL:     "",
				Snippet: fmt.Sprintf("当前未配置可用搜索源。请设置 BAIDU_API_KEY（百度千帆 AI 搜索，每日 100 次免费额度）或 BING_SEARCH_API_KEY（Bing Web Search）。query=%s", query),
			},
		},
	}, nil
}

// BingSearchProvider Bing Web Search API（国内云服务器可访问，需 API Key）
type BingSearchProvider struct{}

func (b *BingSearchProvider) Name() string { return "bing" }
func (b *BingSearchProvider) Search(ctx context.Context, query string) (map[string]interface{}, error) {
	apiKey := os.Getenv("BING_SEARCH_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("Bing 搜索未配置：未设置 BING_SEARCH_API_KEY")
	}
	endpoint := getEnvDefault("BING_SEARCH_ENDPOINT", "https://api.bing.microsoft.com/v7.0/search")

	reqURL := endpoint + "?q=" + url.QueryEscape(query) + "&count=5&mkt=zh-CN"
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", apiKey)

	resp, err := searchHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bing API 返回 %d: %s", resp.StatusCode, string(body))
	}

	var bingResp struct {
		WebPages struct {
			Value []struct {
				Name    string `json:"name"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			} `json:"value"`
		} `json:"webPages"`
	}
	if err := json.Unmarshal(body, &bingResp); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(bingResp.WebPages.Value))
	for _, v := range bingResp.WebPages.Value {
		results = append(results, SearchResult{Title: v.Name, URL: v.URL, Snippet: v.Snippet})
	}
	return map[string]interface{}{"results": results}, nil
}

// BaiduSearchProvider 百度 AI 搜索 API（千帆 AppBuilder，每日 100 次免费额度）
type BaiduSearchProvider struct{}

func (b *BaiduSearchProvider) Name() string { return "baidu" }
func (b *BaiduSearchProvider) Search(ctx context.Context, query string) (map[string]interface{}, error) {
	apiKey := os.Getenv("BAIDU_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("百度 AI 搜索未配置：未设置 BAIDU_API_KEY")
	}
	endpoint := getEnvDefault("BAIDU_SEARCH_ENDPOINT", "https://qianfan.baidubce.com/v2/ai_search/web_search")

	body := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "user", "content": query},
		},
		"edition":               "standard",
		"search_source":         "baidu_search_v2",
		"search_recency_filter": "year",
		"resource_type_filter": []map[string]interface{}{
			{"type": "web", "top_k": 5},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := searchHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("百度 AI 搜索 API 返回 %d: %s", resp.StatusCode, string(respBody))
	}

	var baiduResp struct {
		References []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
			Content string `json:"content"`
		} `json:"references"`
	}
	if err := json.Unmarshal(respBody, &baiduResp); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(baiduResp.References))
	for _, v := range baiduResp.References {
		snippet := v.Snippet
		if snippet == "" {
			snippet = v.Content
		}
		results = append(results, SearchResult{Title: v.Title, URL: v.URL, Snippet: snippet})
	}
	return map[string]interface{}{"results": results}, nil
}

// SearXNGProvider 自建 SearXNG 实例（国内云服务器私有化部署，稳定可控）
type SearXNGProvider struct{}

func (s *SearXNGProvider) Name() string { return "searxng" }
func (s *SearXNGProvider) Search(ctx context.Context, query string) (map[string]interface{}, error) {
	baseURL := strings.TrimRight(os.Getenv("SEARXNG_URL"), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("SearXNG 未配置：未设置 SEARXNG_URL")
	}
	reqURL := baseURL + "/search?q=" + url.QueryEscape(query) + "&format=json"
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := searchHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SearXNG 返回 %d: %s", resp.StatusCode, string(body))
	}

	var searxResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &searxResp); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(searxResp.Results))
	for _, v := range searxResp.Results {
		results = append(results, SearchResult{Title: v.Title, URL: v.URL, Snippet: v.Content})
	}
	return map[string]interface{}{"results": results}, nil
}

// DuckDuckGoProvider DuckDuckGo Lite（国际网络可用，国内云服务器可能不稳定）
type DuckDuckGoProvider struct{}

func (d *DuckDuckGoProvider) Name() string { return "duckduckgo" }
func (d *DuckDuckGoProvider) Search(ctx context.Context, query string) (map[string]interface{}, error) {
	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:125.0) Gecko/20100101 Firefox/125.0")

	resp, err := searchHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	results := parseDuckDuckGoResults(string(body))
	if len(results) == 0 {
		return map[string]interface{}{
			"results": []SearchResult{
				{Title: "未找到结果", URL: "", Snippet: "DuckDuckGo 未返回有效结果，建议在国内云服务器配置 BAIDU_API_KEY 或 BING_SEARCH_API_KEY"},
			},
		}, nil
	}
	return map[string]interface{}{"results": results}, nil
}

func parseDuckDuckGoResults(html string) []SearchResult {
	var results []SearchResult
	re := regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	matches := re.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		title := stripHTMLTags(m[2])
		href := m[1]
		if strings.HasPrefix(href, "//duckduckgo.com/l/?uddg=") {
			if u, err := url.Parse("https:" + href); err == nil {
				if real := u.Query().Get("uddg"); real != "" {
					href = real
				}
			}
		}
		results = append(results, SearchResult{Title: title, URL: href, Snippet: title})
		if len(results) >= 5 {
			break
		}
	}
	return results
}
