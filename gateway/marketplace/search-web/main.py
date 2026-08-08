"""
Eleball-claw 内置模块：search-web（联网搜索）

双重身份（见 docs/marketing/claw-implementation-plan.md §E）：
1. claw 对话页联网搜索工具：激活后注入聊天窗搜索源选择。
2. 开发者范例：可借鉴本模块源码开发自己的秘技（标准 /health + /execute）。

复用上游 builtin SearchWeb 的搜索逻辑（gateway/internal/service/search_provider.go），
支持 baidu / bing / searxng / duckduckgo / exa 五个搜索源（exa 经 mcporter 调用 Exa，keyless 兜底）。

**选源契约**（不写死源）：
上游先调 `action=list_sources`（或读 /health 的 providers 字段）获取可用源列表，
再选目标源传给 `action=search` 的 `provider` 参数执行。未传 provider 时模块按
优先级兜底（仅作防御，上游应显式选源）。

标准接口（见 docs/tool-driver-guide.md §9）：
- GET  /health  上报状态、能力与可用源
- POST /execute 执行 action（list_sources / search / fetch / web_read）
"""

import os
import re
import json
import shutil
import logging
import subprocess
from typing import Any
from urllib.parse import urlparse, parse_qs

import requests
from bs4 import BeautifulSoup
from fastapi import FastAPI, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("search-web-module")

app = FastAPI(title="Eleball search-web Module", version="1.0.0")

MODULE_ID = "search-web"
VERSION = "1.0.0"
CAPABILITIES = ["list_sources", "search", "fetch", "web_read"]


class ModuleError(Exception):
    """模块标准错误：携带 error_code + error_message + 可选 upstream_status。

    标准错误码（供网关透传给 LLM 与调试者精确理解失败原因）：
      credential_missing   必填凭证未配置
      credential_invalid   凭证被上游拒绝（401/403）
      upstream_error       上游返回非 200（其它 4xx/5xx）
      upstream_timeout     上游调用超时
      parameter_invalid    参数缺失/非法
      unsupported_action   未知 action
      unsupported_provider 未知 provider
      module_internal_error 模块内部异常
    """

    def __init__(self, code: str, message: str, upstream_status: int | None = None):
        super().__init__(message)
        self.code = code
        self.message = message
        self.upstream_status = upstream_status

# 搜索 HTTP 客户端统一超时（与上游 searchHTTPClient 15s 对齐）
SEARCH_TIMEOUT = 15
USER_AGENT = (
    "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:125.0) "
    "Gecko/20100101 Firefox/125.0"
)


# ====== 搜索源注册表 ======
# 对齐上游 search_provider.go：available 判断与 IsSearchProviderAvailable 一致；
# recommended 对齐 ListAvailableSearchProviders（前端只推荐 baidu/bing）。

class SourceMeta(BaseModel):
    name: str
    label: str
    available: bool
    recommended: bool
    description: str


def _is_available(name: str) -> bool:
    """判断指定源是否已配置可用（对齐上游 IsSearchProviderAvailable）。"""
    n = name.lower()
    if n == "baidu":
        return bool(os.environ.get("BAIDU_API_KEY"))
    if n == "bing":
        return bool(os.environ.get("BING_SEARCH_API_KEY"))
    if n == "searxng":
        return bool(os.environ.get("SEARXNG_URL"))
    if n == "duckduckgo":
        # 无需 key，国际网络可用；国内服务器不稳定，不作为推荐项但始终 available
        return True
    if n == "exa":
        # keyless，经 mcporter 调用 Exa MCP；可用性取决于容器内是否预装 mcporter 并注册 exa
        return shutil.which("mcporter") is not None
    return False


# 源定义（顺序即兜底优先级）
_SOURCES = [
    {"name": "baidu", "label": "百度", "recommended": True,
     "description": "百度千帆 AI 搜索，每日 100 次免费额度（需 BAIDU_API_KEY）"},
    {"name": "bing", "label": "Bing", "recommended": True,
     "description": "Bing Web Search API（需 BING_SEARCH_API_KEY）"},
    {"name": "searxng", "label": "SearXNG", "recommended": False,
     "description": "自建 SearXNG 实例（需 SEARXNG_URL）"},
    {"name": "duckduckgo", "label": "DuckDuckGo", "recommended": False,
     "description": "DuckDuckGo Lite，无需 key，国际网络可用"},
    {"name": "exa", "label": "Exa", "recommended": False,
     "description": "Exa 语义搜索，经 mcporter 接入，无需 API Key（keyless 兜底源）"},
]


def list_sources() -> list[SourceMeta]:
    """返回全部源及其可用/推荐状态（对齐上游 ListAvailableSearchProviders 扩展）。"""
    return [
        SourceMeta(
            name=s["name"],
            label=s["label"],
            available=_is_available(s["name"]),
            recommended=s["recommended"],
            description=s["description"],
        )
        for s in _SOURCES
    ]


def _first_available_provider() -> str:
    """返回第一个 available 的源名（仅作 provider 未传时的兜底）。"""
    for s in _SOURCES:
        if _is_available(s["name"]):
            return s["name"]
    return "duckduckgo"  # duckduckgo 始终 available


def _first_available_provider_with_credentials(credentials: dict[str, str] | None = None) -> str:
    """优先按用户凭证（params["credentials"]）选可用源，再回落 env，最后 duckduckgo。
    避免用户配了 baidu_api_key 但模块容器 env 没设 BAIDU_API_KEY 时误选 duckduckgo。"""
    for name in ("baidu", "bing", "searxng"):
        if _provider_key_available(name, credentials):
            return name
    return "duckduckgo"


# ====== 请求/响应模型 ======

class ExecuteRequest(BaseModel):
    action: str = Field(..., description="操作名：list_sources / search / fetch / web_read")
    params: dict[str, Any] = Field(default_factory=dict, description="业务参数")
    user_id: str = Field(default="", description="当前用户 ID（本模块不使用）")


class HealthResponse(BaseModel):
    module_id: str
    version: str
    status: str
    capabilities: list[str]
    providers: list[SourceMeta]


class SearchResult(BaseModel):
    title: str
    url: str
    snippet: str


# ====== 凭证读取（对齐 stt 模块模式：params.credentials 优先，环境变量回退）======

def _read_credentials(params: dict[str, Any]) -> dict[str, str]:
    """读取用户在秘技卡片配置的凭证（网关经 params["credentials"] 注入）。"""
    creds = params.get("credentials") or {}
    return {k: str(v) for k, v in creds.items() if v}


def _provider_key_available(name: str, credentials: dict[str, str] | None) -> bool:
    """判定指定源本次调用是否有可用 key：用户凭证优先，环境变量回退。

    list_sources/_is_available 无 SKU 上下文仍按 env 判定；do_search 带每次调用的
    credentials，故此处单独判定，缺 key 时给出明确的配置引导错误。
    """
    creds = credentials or {}
    if name == "baidu":
        return bool(creds.get("baidu_api_key") or os.environ.get("BAIDU_API_KEY"))
    if name == "bing":
        return bool(creds.get("bing_search_api_key") or os.environ.get("BING_SEARCH_API_KEY"))
    return _is_available(name)


# 缺 key 时的引导文案
_KEY_MISSING_HINTS = {
    "baidu": "未配置百度千帆 API Key，请在秘技卡片配置凭证（baidu_api_key）",
    "bing": "未配置必应 Bing Search API Key，请在秘技卡片配置凭证（bing_search_api_key）",
    "exa": "Exa 搜索不可用：容器未预装 mcporter（keyless 源，无需 API Key）",
}


# ====== 各源搜索实现 ======

def _search_baidu(query: str, credentials: dict[str, str] | None = None) -> list[SearchResult]:
    api_key = (credentials or {}).get("baidu_api_key") or os.environ.get("BAIDU_API_KEY", "")
    if not api_key:
        raise ModuleError("credential_missing", _KEY_MISSING_HINTS["baidu"])
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
    try:
        resp = requests.post(
            endpoint,
            json=body,
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {api_key}",
            },
            timeout=SEARCH_TIMEOUT,
        )
    except requests.exceptions.Timeout:
        raise ModuleError("upstream_timeout", "百度 AI 搜索调用超时")
    if resp.status_code != 200:
        code = "credential_invalid" if resp.status_code in (401, 403) else "upstream_error"
        raise ModuleError(code, f"百度 AI 搜索 API 返回 {resp.status_code}: {resp.text}", resp.status_code)
    refs = resp.json().get("references", [])
    results = []
    for v in refs:
        snippet = v.get("snippet") or v.get("content", "")
        results.append(SearchResult(title=v.get("title", ""), url=v.get("url", ""), snippet=snippet))
    return results


def _search_bing(query: str, credentials: dict[str, str] | None = None) -> list[SearchResult]:
    api_key = (credentials or {}).get("bing_search_api_key") or os.environ.get("BING_SEARCH_API_KEY", "")
    if not api_key:
        raise ModuleError("credential_missing", _KEY_MISSING_HINTS["bing"])
    endpoint = os.environ.get(
        "BING_SEARCH_ENDPOINT", "https://api.bing.microsoft.com/v7.0/search"
    )
    try:
        resp = requests.get(
            endpoint,
            params={"q": query, "count": 5, "mkt": "zh-CN"},
            headers={"Ocp-Apim-Subscription-Key": api_key},
            timeout=SEARCH_TIMEOUT,
        )
    except requests.exceptions.Timeout:
        raise ModuleError("upstream_timeout", "Bing 搜索调用超时")
    if resp.status_code != 200:
        code = "credential_invalid" if resp.status_code in (401, 403) else "upstream_error"
        raise ModuleError(code, f"Bing API 返回 {resp.status_code}: {resp.text}", resp.status_code)
    values = resp.json().get("webPages", {}).get("value", [])
    return [
        SearchResult(title=v.get("name", ""), url=v.get("url", ""), snippet=v.get("snippet", ""))
        for v in values
    ]


def _search_searxng(query: str) -> list[SearchResult]:
    base_url = os.environ.get("SEARXNG_URL", "").rstrip("/")
    if not base_url:
        raise ModuleError("credential_missing", "SearXNG 未配置：未设置 SEARXNG_URL")
    try:
        resp = requests.get(
            f"{base_url}/search",
            params={"q": query, "format": "json"},
            timeout=SEARCH_TIMEOUT,
        )
    except requests.exceptions.Timeout:
        raise ModuleError("upstream_timeout", "SearXNG 调用超时")
    if resp.status_code != 200:
        raise ModuleError("upstream_error", f"SearXNG 返回 {resp.status_code}: {resp.text}", resp.status_code)
    items = resp.json().get("results", [])
    return [
        SearchResult(title=v.get("title", ""), url=v.get("url", ""), snippet=v.get("content", ""))
        for v in items
    ]


def _search_duckduckgo(query: str) -> list[SearchResult]:
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


# Exa 结果块分隔行（mcporter --output text 中独占一行的 ---）
_EXA_SEP = re.compile(r"(?m)^\s*---\s*$")


def _parse_exa_results(text: str) -> list[SearchResult]:
    """解析 mcporter --output text 的 Exa 搜索输出（Title/URL/Highlights 块，--- 分隔）。"""
    results: list[SearchResult] = []
    for block in _EXA_SEP.split(text):
        block = block.strip()
        if not block:
            continue
        title = url = ""
        snippet_parts: list[str] = []
        in_highlights = False
        for line in block.split("\n"):
            if line.startswith("Title: "):
                title = line[len("Title: "):]
                in_highlights = False
            elif line.startswith("URL: "):
                url = line[len("URL: "):]
                in_highlights = False
            elif line.startswith("Published: ") or line.startswith("Author: "):
                in_highlights = False
            elif line.startswith("Highlights:"):
                in_highlights = True
            elif in_highlights:
                snippet_parts.append(line)
        if not title and not url:
            continue
        results.append(SearchResult(title=title, url=url, snippet="\n".join(snippet_parts).strip()))
    return results


def _search_exa(query: str) -> list[SearchResult]:
    """Exa 语义搜索（经 mcporter 调用 Exa MCP，keyless）。"""
    if not shutil.which("mcporter"):
        raise ModuleError("credential_missing", _KEY_MISSING_HINTS["exa"])
    args = json.dumps({"query": query, "numResults": 5})
    try:
        proc = subprocess.run(
            ["mcporter", "call", "exa.web_search_exa", "--args", args, "--output", "text"],
            capture_output=True, text=True, timeout=SEARCH_TIMEOUT + 15,
        )
    except subprocess.TimeoutExpired:
        raise ModuleError("upstream_timeout", "Exa 搜索调用超时")
    if proc.returncode != 0:
        raise ModuleError("upstream_error", f"Exa 搜索调用失败：{proc.stderr.strip() or proc.stdout}")
    results = _parse_exa_results(proc.stdout)
    if not results:
        results.append(SearchResult(
            title="未找到结果", url="",
            snippet=f"Exa 未返回有效结果，query={query}",
        ))
    return results


def do_search(query: str, provider: str | None = None,
              credentials: dict[str, str] | None = None) -> list[SearchResult]:
    """按指定源搜索；provider 为空时兜底选第一个可用源（上游应显式传 provider）。

    credentials 为本次调用的用户凭证（params["credentials"]），对 baidu/bing
    等需 key 的源优先生效；缺 key 时返回明确的配置引导错误。
    """
    name = (provider or _first_available_provider_with_credentials(credentials)).lower().strip()
    if not provider:
        logger.info("search 未传 provider，按凭证兜底使用 %s", name)
    if not _provider_key_available(name, credentials):
        hint = _KEY_MISSING_HINTS.get(
            name, f"搜索源 {name} 不可用（未配置凭据），请先调 list_sources 查看可用源")
        raise ModuleError("credential_missing", hint)
    if name == "baidu":
        return _search_baidu(query, credentials)
    if name == "bing":
        return _search_bing(query, credentials)
    if name == "searxng":
        return _search_searxng(query)
    if name == "duckduckgo":
        return _search_duckduckgo(query)
    if name == "exa":
        return _search_exa(query)
    raise ModuleError("unsupported_provider", f"不支持的搜索源: {name}")


def do_fetch(url: str) -> dict[str, Any]:
    """抓取网页正文（FetchURL 等价能力）。"""
    resp = requests.get(url, headers={"User-Agent": USER_AGENT}, timeout=SEARCH_TIMEOUT)
    resp.encoding = resp.apparent_encoding
    soup = BeautifulSoup(resp.text, "html.parser")
    for tag in soup(["script", "style", "noscript"]):
        tag.decompose()
    title = soup.title.get_text(strip=True) if soup.title else ""
    text = soup.get_text(separator="\n", strip=True)
    return {"title": title, "url": url, "content": text[:4000]}


def do_web_read(url: str) -> dict[str, Any]:
    """读取网页正文为 markdown（经 mcporter 调用 Exa web_fetch_exa，keyless）。
    与 fetch（纯 HTTP + BeautifulSoup）互补：适合公众号/新闻/文档等需渲染的页面。"""
    if not shutil.which("mcporter"):
        raise ModuleError("credential_missing", "WebRead 不可用：容器未预装 mcporter（keyless，无需 API Key）")
    if not url.startswith("http://") and not url.startswith("https://"):
        url = f"https://{url}"
    args = json.dumps({"urls": [url], "maxCharacters": 20000})
    try:
        proc = subprocess.run(
            ["mcporter", "call", "exa.web_fetch_exa", "--args", args, "--output", "text"],
            capture_output=True, text=True, timeout=30,
        )
    except subprocess.TimeoutExpired:
        raise ModuleError("upstream_timeout", "WebRead 调用超时")
    if proc.returncode != 0:
        raise ModuleError("upstream_error", f"WebRead 调用失败：{proc.stderr.strip() or proc.stdout}")
    content = proc.stdout.strip()
    if not content:
        raise ModuleError("upstream_error", f"WebRead 未返回内容，url={url}")
    return {"url": url, "content": content}


# ====== 标准接口 ======

@app.get("/health", response_model=HealthResponse)
def health() -> HealthResponse:
    return HealthResponse(
        module_id=MODULE_ID,
        version=VERSION,
        status="ok",
        capabilities=CAPABILITIES,
        providers=list_sources(),
    )


@app.post("/execute")
def execute(req: ExecuteRequest) -> dict[str, Any]:
    action = (req.action or "").strip()
    params = req.params or {}
    try:
        # list_sources：返回可用源列表（上游选源前先调此）
        if action == "list_sources":
            return {"sources": [s.model_dump() for s in list_sources()]}
        if action == "search":
            query = params.get("query") or params.get("q")
            if not query:
                raise ModuleError("parameter_invalid", "search 需要参数 query")
            provider = params.get("provider") or params.get("source")
            results = do_search(str(query), provider, _read_credentials(params))
            return {"provider": provider or _first_available_provider(), "results": [r.model_dump() for r in results]}
        if action == "fetch":
            url = params.get("url")
            if not url:
                raise ModuleError("parameter_invalid", "fetch 需要参数 url")
            return do_fetch(str(url))
        if action == "web_read":
            url = params.get("url") or params.get("query")
            if not url:
                raise ModuleError("parameter_invalid", "web_read 需要参数 url")
            return do_web_read(str(url))
        raise ModuleError("unsupported_action", f"不支持的 action: {action}")
    except ModuleError as e:
        # 返回结构化错误码（error_code/error_message/upstream_status），网关透传给 LLM 与调试者
        return JSONResponse(
            status_code=500,
            content={
                "error_code": e.code,
                "error_message": e.message,
                "upstream_status": e.upstream_status,
            },
        )
    except Exception as e:
        logger.exception("execute 失败")
        return JSONResponse(
            status_code=500,
            content={
                "error_code": "module_internal_error",
                "error_message": str(e),
                "upstream_status": None,
            },
        )


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("PORT", "8091")))
