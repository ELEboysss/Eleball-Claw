"""
Eleball 集市模块：Firecrawl 网页抓取能力封装

作为标准 Eleball Marketplace Module 运行，对外暴露：
- GET  /health
- POST /execute

内部通过 FIRECRAWL_BASE_URL 调用 Firecrawl 自托管服务的 /v1/scrape API，
将任意网页转换为干净 Markdown 或结构化数据，供 Agent 工作流使用。
"""

import os
import logging
from typing import Any

import requests
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("firecrawl-module")

app = FastAPI(title="Eleball Firecrawl Module", version="1.0.0")

FIRECRAWL_BASE_URL = os.environ.get("FIRECRAWL_BASE_URL", "https://api.firecrawl.dev").rstrip("/")
# Firecrawl Cloud API 需要 API Key；本地自托管实例通常可留空
FIRECRAWL_API_KEY = os.environ.get("FIRECRAWL_API_KEY", "")

MODULE_ID = "firecrawl"
CAPABILITIES = ["scrape", "crawl", "extract"]


class ExecuteRequest(BaseModel):
    action: str = Field(..., description="操作名：scrape / crawl / extract")
    params: dict[str, Any] = Field(default_factory=dict, description="Firecrawl API 参数")
    user_id: str = Field(default="", description="当前用户 ID")


class ExecuteResponse(BaseModel):
    status: str = "ok"
    result: dict[str, Any] | None = None
    error: str | None = None


def _firecrawl_headers(api_key: str = "") -> dict[str, str]:
    headers = {"Content-Type": "application/json"}
    key = api_key or FIRECRAWL_API_KEY
    if key:
        # Firecrawl Cloud 使用 x-api-key 而非 Authorization: Bearer
        headers["x-api-key"] = key
    return headers


def _extract_api_key(params: dict[str, Any]) -> str:
    """从请求参数中提取 Firecrawl API Key：优先取 credentials.firecrawl_api_key，回退环境变量。"""
    credentials = params.get("credentials") or {}
    if isinstance(credentials, dict):
        return credentials.get("firecrawl_api_key", "") or ""
    return ""


def _check_firecrawl() -> tuple[bool, str]:
    """探测 Firecrawl 后端是否可用"""
    try:
        # Firecrawl 自托管没有统一健康接口，直接访问根路径或尝试一次轻量请求
        resp = requests.get(f"{FIRECRAWL_BASE_URL}/", timeout=5)
        return resp.status_code < 500, f"HTTP {resp.status_code}"
    except Exception as e:
        return False, str(e)


@app.get("/health")
def health() -> dict[str, Any]:
    online, detail = _check_firecrawl()
    status = "ok" if online else "degraded"
    return {
        "module_id": MODULE_ID,
        "version": "1.0.0",
        "status": status,
        "capabilities": CAPABILITIES,
        "detail": detail,
    }


@app.post("/execute")
def execute(req: ExecuteRequest) -> ExecuteResponse:
    action = req.action
    params = req.params

    if action == "scrape":
        return _do_scrape(params)
    if action == "crawl":
        return _do_crawl(params)
    if action == "extract":
        return _do_extract(params)

    raise HTTPException(status_code=400, detail=f"不支持的 action: {action}")


def _do_scrape(params: dict[str, Any]) -> ExecuteResponse:
    """调用 Firecrawl /v1/scrape，将单个网页转为 Markdown / JSON"""
    url = params.get("url") or params.get("query")
    if not url:
        return ExecuteResponse(status="error", error="缺少 url 参数")

    payload = {
        "url": url,
        "formats": params.get("formats", ["markdown"]),
    }
    # 允许透传其他 Firecrawl 参数
    for key in ["onlyMainContent", "includeTags", "excludeTags", "headers", "timeout"]:
        if key in params:
            payload[key] = params[key]

    try:
        resp = requests.post(
            f"{FIRECRAWL_BASE_URL}/v1/scrape",
            headers=_firecrawl_headers(_extract_api_key(params)),
            json=payload,
            timeout=120,
        )
        resp.raise_for_status()
        data = resp.json()

        if not data.get("success"):
            return ExecuteResponse(status="error", error=data.get("error", "Firecrawl 返回失败"))

        markdown = data.get("data", {}).get("markdown", "")
        metadata = data.get("data", {}).get("metadata", {})
        return ExecuteResponse(
            status="ok",
            result={
                "content": markdown,
                "title": metadata.get("title", ""),
                "source_url": metadata.get("sourceURL", url),
                "description": metadata.get("description", ""),
                "language": metadata.get("language", ""),
            },
        )
    except requests.exceptions.Timeout:
        return ExecuteResponse(status="error", error="Firecrawl 请求超时")
    except requests.exceptions.ConnectionError as e:
        logger.error("Firecrawl 连接失败: %s", e)
        return ExecuteResponse(status="error", error="Firecrawl 服务未启动或无法连接")
    except Exception as e:
        logger.exception("Firecrawl scrape 异常")
        return ExecuteResponse(status="error", error=str(e))


def _do_crawl(params: dict[str, Any]) -> ExecuteResponse:
    """调用 Firecrawl /v1/crawl，对网站做批量爬取"""
    url = params.get("url")
    if not url:
        return ExecuteResponse(status="error", error="缺少 url 参数")

    payload = {"url": url}
    for key in ["limit", "includePaths", "excludePaths", "maxDepth", "scrapeOptions"]:
        if key in params:
            payload[key] = params[key]

    try:
        resp = requests.post(
            f"{FIRECRAWL_BASE_URL}/v1/crawl",
            headers=_firecrawl_headers(_extract_api_key(params)),
            json=payload,
            timeout=30,
        )
        resp.raise_for_status()
        data = resp.json()

        if not data.get("success"):
            return ExecuteResponse(status="error", error=data.get("error", "Firecrawl crawl 失败"))

        return ExecuteResponse(
            status="ok",
            result={
                "job_id": data.get("id"),
                "status": "started",
                "check_url": data.get("url"),
            },
        )
    except Exception as e:
        logger.exception("Firecrawl crawl 异常")
        return ExecuteResponse(status="error", error=str(e))


def _do_extract(params: dict[str, Any]) -> ExecuteResponse:
    """调用 Firecrawl /v1/extract，按 JSON Schema 提取结构化数据"""
    urls = params.get("urls") or ([params.get("url")] if params.get("url") else None)
    schema = params.get("schema")
    if not urls:
        return ExecuteResponse(status="error", error="缺少 urls 参数")

    payload = {"urls": urls}
    if schema:
        payload["schema"] = schema
    if "prompt" in params:
        payload["prompt"] = params["prompt"]

    try:
        resp = requests.post(
            f"{FIRECRAWL_BASE_URL}/v1/extract",
            headers=_firecrawl_headers(_extract_api_key(params)),
            json=payload,
            timeout=120,
        )
        resp.raise_for_status()
        data = resp.json()

        if not data.get("success"):
            return ExecuteResponse(status="error", error=data.get("error", "Firecrawl extract 失败"))

        return ExecuteResponse(status="ok", result=data.get("data", {}))
    except Exception as e:
        logger.exception("Firecrawl extract 异常")
        return ExecuteResponse(status="error", error=str(e))


if __name__ == "__main__":
    import uvicorn

    port = int(os.environ.get("PORT", "8080"))
    uvicorn.run(app, host="0.0.0.0", port=port)
