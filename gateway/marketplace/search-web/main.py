"""
Eleball-claw 内置模块：search-web（联网搜索）

双重身份（见 docs/marketing/claw-implementation-plan.md §E）：
1. claw 对话页联网搜索工具：激活后注入聊天窗搜索源选择。
2. 开发者范例：可借鉴本模块源码开发自己的秘技（标准 /health + /execute）。

复用上游 builtin SearchWeb 的搜索逻辑（gateway/internal/service/search_provider.go），
支持 baidu / bing / searxng / duckduckgo 四个搜索源，按环境变量选择可用源。

标准接口（见 docs/tool-driver-guide.md §9）：
- GET  /health  上报状态与能力
- POST /execute 执行 action（search / fetch）
"""

import os
import re
import logging
from typing import Any
from urllib.parse import quote, parse_qs, urlparse

import requests
from bs4 import BeautifulSoup
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("search-web-module")

app = FastAPI(title="Eleball search-web Module", version="1.0.0")

MODULE_ID = "search-web"
CAPABILITIES = ["search", "fetch"]

# 搜索 HTTP 客户端统一超时（与上游 searchHTTPClient 15s 对齐）
SEARCH_TIMEOUT = 15
USER_AGENT = (
    "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:125.0) "
    "Gecko/20100101 Firefox/125.0"
)


# ====== 请求/响应模型 ======

class ExecuteRequest(BaseModel):
    action: str = Field(..., description="操作名：search / fetch")
    params: dict[str, Any] = Field(default_factory=dict, description="业务参数")
    user_id: str = Field(default="", description="当前用户 ID（本模块不使用）")


class HealthResponse(BaseModel):
    module_id: str
    version: str
    status: str
    capabilities: list[str]


# ====== 搜索结果项（对齐上游 SearchResult）======

class SearchResult(BaseModel):
    title: str
    url: str
    snippet: str


# ====== 搜索源 ======

def _search_baidu(query: str) -> list[SearchResult]:
    api_key = os.environ.get("BAIDU_API_KEY", "")
    if not api_key:
        raise RuntimeError("百度 AI 搜索未配置：未设置 BAIDU_API_KEY")
    endpoint = os.environ.get(
        "BAIDU_SEARCH_ENDPOINT",
        "https://qianfan.baidubce.com/v2/ai_search/web_search",
    )
    body = {
        "messages": [{"role": "user", "content": query}],
        "edition": "standard",
        "search_source": "baidu_search_v2",
        "search_recency_filter": "year",
        "resource_type_filter": [{"type": "web", "top_k": 5}],
    }
    resp = requests.post(
        endpoint,
        json=body,
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {api_key}",
        },
        timeout=SEARCH_TIMEOUT,
    )
    if resp.status_code != 200:
        raise RuntimeError(f"百度 AI 搜索 API 返回 {resp.status_code}: {resp.text}")
    refs = resp.json().get("references", [])
    results = []
    for v in refs:
        snippet = v.get("snippet") or v.get("content", "")
        results.append(SearchResult(title=v.get("title", ""), url=v.get("url", ""), snippet=snippet))
    return results


def _search_bing(query: str) -> list[SearchResult]:
    api_key = os.environ.get("BING_SEARCH_API_KEY", "")
    if not api_key:
        raise RuntimeError("Bing 搜索未配置：未设置 BING_SEARCH_API_KEY")
    endpoint = os.environ.get(
        "BING_SEARCH_ENDPOINT", "https://api.bing.microsoft.com/v7.0/search"
    )
    resp = requests.get(
        endpoint,
        params={"q": query, "count": 5, "mkt": "zh-CN"},
        headers={"Ocp-Apim-Subscription-Key": api_key},
        timeout=SEARCH_TIMEOUT,
    )
    if resp.status_code != 200:
        raise RuntimeError(f"Bing API 返回 {resp.status_code}: {resp.text}")
    values = resp.json().get("webPages", {}).get("value", [])
    return [
        SearchResult(title=v.get("name", ""), url=v.get("url", ""), snippet=v.get("snippet", ""))
        for v in values
    ]


def _search_searxng(query: str) -> list[SearchResult]:
    base_url = os.environ.get("SEARXNG_URL", "").rstrip("/")
    if not base_url:
        raise RuntimeError("SearXNG 未配置：未设置 SEARXNG_URL")
    resp = requests.get(
        f"{base_url}/search",
        params={"q": query, "format": "json"},
        timeout=SEARCH_TIMEOUT,
    )
    if resp.status_code != 200:
        raise RuntimeError(f"SearXNG 返回 {resp.status_code}: {resp.text}")
    items = resp.json().get("results", [])
    return [
        SearchResult(title=v.get("title", ""), url=v.get("url", ""), snippet=v.get("content", ""))
        for v in items
    ]


def _search_duckduckgo(query: str) -> list[SearchResult]:
    """DuckDuckGo Lite（国际网络可用，国内服务器可能不稳定）。"""
    resp = requests.get(
        "https://html.duckduckgo.com/html/",
        params={"q": query},
        headers={"User-Agent": USER_AGENT},
        timeout=SEARCH_TIMEOUT,
    )
    soup = BeautifulSoup(resp.text, "html.parser")
    results = []
    for a in soup.select("a.result__a"):
        title = a.get_text(strip=True)
        href = a.get("href", "")
        # DDG 跳转链接：//duckduckgo.com/l/?uddg=<real_url>
        if href.startswith("//duckduckgo.com/l/?uddg="):
            parsed = urlparse("https:" + href)
            real = parse_qs(parsed.query).get("uddg", [""])[0]
            if real:
                href = real
        if title and href:
            results.append(SearchResult(title=title, url=href, snippet=title))
        if len(results) >= 5:
            break
    if not results:
        results.append(SearchResult(
            title="未找到结果",
            url="",
            snippet="DuckDuckGo 未返回有效结果，建议配置 BAIDU_API_KEY 或 BING_SEARCH_API_KEY",
        ))
    return results


def _first_available_provider() -> str:
    """返回第一个已配置可用的搜索源名称（对齐上游 GetFirstAvailableSearchProvider）。"""
    if os.environ.get("BAIDU_API_KEY"):
        return "baidu"
    if os.environ.get("BING_SEARCH_API_KEY"):
        return "bing"
    if os.environ.get("SEARXNG_URL"):
        return "searxng"
    return "duckduckgo"


def do_search(query: str, provider: str | None = None) -> list[SearchResult]:
    name = (provider or _first_available_provider()).lower().strip()
    if name == "baidu":
        return _search_baidu(query)
    if name == "bing":
        return _search_bing(query)
    if name == "searxng":
        return _search_searxng(query)
    if name == "duckduckgo":
        return _search_duckduckgo(query)
    raise HTTPException(status_code=400, detail=f"不支持的搜索源: {name}")


def do_fetch(url: str) -> dict[str, Any]:
    """抓取网页正文（FetchURL 等价能力）。"""
    resp = requests.get(url, headers={"User-Agent": USER_AGENT}, timeout=SEARCH_TIMEOUT)
    resp.encoding = resp.apparent_encoding
    soup = BeautifulSoup(resp.text, "html.parser")
    # 去脚本/样式后取文本
    for tag in soup(["script", "style", "noscript"]):
        tag.decompose()
    title = soup.title.get_text(strip=True) if soup.title else ""
    text = soup.get_text(separator="\n", strip=True)
    return {"title": title, "url": url, "content": text[:4000]}


# ====== 标准接口 ======

@app.get("/health", response_model=HealthResponse)
def health() -> HealthResponse:
    return HealthResponse(
        module_id=MODULE_ID,
        version="1.0.0",
        status="ok",
        capabilities=CAPABILITIES,
    )


@app.post("/execute")
def execute(req: ExecuteRequest) -> dict[str, Any]:
    action = (req.action or "").strip()
    params = req.params or {}
    try:
        if action == "search":
            query = params.get("query") or params.get("q")
            if not query:
                raise HTTPException(status_code=400, detail="search 需要参数 query")
            provider = params.get("provider")
            results = do_search(str(query), provider)
            return {"results": [r.model_dump() for r in results]}
        if action == "fetch":
            url = params.get("url")
            if not url:
                raise HTTPException(status_code=400, detail="fetch 需要参数 url")
            return do_fetch(str(url))
        raise HTTPException(status_code=400, detail=f"不支持的 action: {action}")
    except HTTPException:
        raise
    except Exception as e:
        logger.exception("execute 失败")
        raise HTTPException(status_code=500, detail=str(e))


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("PORT", "8091")))
