#!/usr/bin/env python3
"""Firecrawl MCP HTTP 服务器（cloud + claw 双端同构）。

经 MCP Streamable HTTP JSON-RPC 暴露 scrape / crawl / crawl_status / extract 四工具，内部调用 Firecrawl API。
transport=mcp_http + deployment=docker：网关经 POST / 发 JSON-RPC，探活走 initialize + tools/list。
API Key 由网关经 mcp_server_config.headers 的 X-Firecrawl-Key（${credentials.firecrawl_api_key}）
逐请求注入；FIRECRAWL_BASE_URL 取 env（非密配置）。tools/list 不需 Key，tools/call 缺 Key 报错。
crawl 为异步任务：返回 job_id 后经 crawl_status（GET /v1/crawl/{id}）轮询结果。

仅依赖标准库（urllib + http.server），无需 pip install（对标 mcp-stdio-echo 零依赖，比 agent-reach 更轻）。
auto_sku=true：网关探活拿到 tools/list 后自动派生三份可购买 SKU，免手写 skus/*.json。

运行：python main.py（容器内监听 0.0.0.0:8080；云端经 eleball-net DNS http://firecrawl:8080、
claw 经宿主机端口映射 http://localhost:8095 访问）。
"""

import datetime
import json
import os
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

FIRECRAWL_BASE_URL = os.environ.get("FIRECRAWL_BASE_URL", "https://api.firecrawl.dev").rstrip("/")
PORT = int(os.environ.get("PORT", "8080"))


def make_result(req_id, result):
    return {"jsonrpc": "2.0", "id": req_id, "result": result}


def make_error(req_id, code, message):
    return {"jsonrpc": "2.0", "id": req_id, "error": {"code": code, "message": message}}


def tools_list():
    return [
        {
            "name": "scrape",
            "title": "Firecrawl Scrape",
            "description": "基于 Firecrawl 的网页抓取：将单个网页转换为干净 Markdown，返回标题、URL、描述等元数据",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "url": {"type": "string", "description": "要抓取的网页 URL"},
                    "formats": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "输出格式，默认 markdown",
                        "default": ["markdown"],
                    },
                    "onlyMainContent": {"type": "boolean", "description": "仅返回正文"},
                },
                "required": ["url"],
            },
        },
        {
            "name": "crawl",
            "title": "Firecrawl Crawl",
            "description": "基于 Firecrawl 的批量爬取：对指定网站启动批量爬取任务（异步），返回任务 ID。拿到 job_id 后必须调用 crawl_status 工具轮询进度与抓取结果。",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "url": {"type": "string", "description": "爬取起始 URL"},
                    "limit": {"type": "integer", "description": "最大抓取页数", "default": 10},
                },
                "required": ["url"],
            },
        },
        {
            "name": "crawl_status",
            "title": "Firecrawl Crawl Status",
            "description": "查询 crawl 批量爬取任务的状态与结果。crawl 为异步任务：调用 crawl 拿到 job_id 后，用本工具轮询（传 id=job_id）；status=completed 时 data 为各页抓取结果（长文已截断）。",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "id": {"type": "string", "description": "crawl 返回的 job_id"},
                },
                "required": ["id"],
            },
        },
        {
            "name": "extract",
            "title": "Firecrawl Extract",
            "description": "基于 Firecrawl 的结构化提取：按 JSON Schema 从网页中提取结构化数据",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "urls": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "要提取的 URL 列表",
                    },
                    "schema": {"type": "object", "description": "JSON Schema 定义输出结构"},
                    "prompt": {"type": "string", "description": "自然语言提取提示"},
                },
                "required": ["urls"],
            },
        },
    ]


def _firecrawl_request(path, payload, api_key, method="POST"):
    """调用 Firecrawl API，返回 (data, error)。api_key 由网关经 X-Firecrawl-Key 头逐请求注入。"""
    if not api_key:
        return None, "缺少 firecrawl_api_key（请在模块凭证配置 firecrawl_api_key）"
    url = FIRECRAWL_BASE_URL + path
    body = json.dumps(payload).encode("utf-8") if payload is not None else None
    # Firecrawl API 鉴权只认 Authorization: Bearer <key>（官方文档/SDK 统一形态）；
    # 误用 x-api-key（Anthropic/Exa 风格）会被视为未提供 Key，一律 401。
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + api_key}
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            return json.loads(resp.read().decode("utf-8")), None
    except urllib.error.HTTPError as e:
        try:
            detail = e.read().decode("utf-8", errors="replace")
        except Exception:
            detail = str(e)
        return None, "Firecrawl HTTP %d: %s" % (e.code, detail)
    except urllib.error.URLError as e:
        return None, "Firecrawl 连接失败: %s" % e.reason
    except Exception as e:
        return None, "Firecrawl 请求异常: %s" % e


def _text(content):
    return {"content": [{"type": "text", "text": content}]}


def _error_text(msg):
    return {"isError": True, "content": [{"type": "text", "text": msg}]}


def _do_scrape(args, api_key):
    url = args.get("url")
    if not url:
        return _error_text("缺少 url 参数")
    payload = {"url": url, "formats": args.get("formats", ["markdown"])}
    if "onlyMainContent" in args:
        payload["onlyMainContent"] = args["onlyMainContent"]
    data, err = _firecrawl_request("/v1/scrape", payload, api_key)
    if err:
        return _error_text(err)
    if not data.get("success"):
        return _error_text(data.get("error", "Firecrawl 返回失败"))
    d = data.get("data", {})
    meta = d.get("metadata", {})
    result = {
        "markdown": d.get("markdown", ""),
        "title": meta.get("title", ""),
        "source_url": meta.get("sourceURL", url),
        "description": meta.get("description", ""),
        "language": meta.get("language", ""),
    }
    return _text(json.dumps(result, ensure_ascii=False))


def _do_crawl(args, api_key):
    url = args.get("url")
    if not url:
        return _error_text("缺少 url 参数")
    payload = {"url": url}
    if "limit" in args:
        payload["limit"] = args["limit"]
    data, err = _firecrawl_request("/v1/crawl", payload, api_key)
    if err:
        return _error_text(err)
    if not data.get("success"):
        return _error_text(data.get("error", "Firecrawl crawl 失败"))
    result = {
        "job_id": data.get("id"),
        "status": "started",
        "check_url": data.get("url"),
        "next_step": "crawl 为异步任务：请调用 crawl_status 工具（传 id=job_id）轮询进度；status=completed 时 data 即抓取结果。",
    }
    return _text(json.dumps(result, ensure_ascii=False))


# crawl_status 返回中每页 markdown 的截断长度：防整站结果（数十页长文）撑爆 LLM 上下文。
CRAWL_PAGE_MARKDOWN_MAX = 2000


def _truncate_crawl_pages(pages):
    """截断 crawl 结果各页的长 markdown，保留元数据。非列表原样返回。"""
    if not isinstance(pages, list):
        return pages
    out = []
    for p in pages:
        if isinstance(p, dict):
            p = dict(p)
            md = p.get("markdown")
            if isinstance(md, str) and len(md) > CRAWL_PAGE_MARKDOWN_MAX:
                p["markdown"] = md[:CRAWL_PAGE_MARKDOWN_MAX] + "\n...(内容过长已截断)"
        out.append(p)
    return out


def _do_crawl_status(args, api_key):
    job_id = args.get("id")
    if not job_id:
        return _error_text("缺少 id 参数（crawl 返回的 job_id）")
    data, err = _firecrawl_request("/v1/crawl/%s" % job_id, None, api_key, method="GET")
    if err:
        return _error_text(err)
    result = {
        "status": data.get("status"),
        "total": data.get("total"),
        "completed": data.get("completed"),
        "credits_used": data.get("creditsUsed"),
        # 心跳字段：每次轮询返回必不同，网关 no-progress 循环检测据此识别「轮询在推进」，
        # 避免同 job_id 多次轮询被误判为无意义重复调用（同 tool+args+同返回才计 strike）。
        "polled_at": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
    }
    status = data.get("status")
    if status == "completed":
        result["data"] = _truncate_crawl_pages(data.get("data"))
    elif status in ("scraping",):
        # 进行中：附带进度与节奏提示。模型无法 sleep，密集轮询只会空烧步数预算；
        # 长任务正确姿势是告知进度后收尾，稍后由用户追问时再查。
        result["hint"] = "任务进行中（completed/total 见上）。任务推进需要时间，请勿立即连续轮询：可先向用户说明当前进度，稍后用户追问时再查；任务完成后 crawl_status 会返回 data。"
    elif status in ("failed", "cancelled"):
        result["data"] = _truncate_crawl_pages(data.get("data"))
        result["hint"] = "任务已%s。" % ("失败" if status == "failed" else "取消")
    return _text(json.dumps(result, ensure_ascii=False))


def _do_extract(args, api_key):
    urls = args.get("urls")
    if not urls:
        return _error_text("缺少 urls 参数")
    payload = {"urls": urls}
    if "schema" in args:
        payload["schema"] = args["schema"]
    if "prompt" in args:
        payload["prompt"] = args["prompt"]
    data, err = _firecrawl_request("/v1/extract", payload, api_key)
    if err:
        return _error_text(err)
    if not data.get("success"):
        return _error_text(data.get("error", "Firecrawl extract 失败"))
    return _text(json.dumps(data.get("data", {}), ensure_ascii=False))


def tools_call(name, arguments, api_key):
    if name == "scrape":
        return _do_scrape(arguments, api_key)
    if name == "crawl":
        return _do_crawl(arguments, api_key)
    if name == "crawl_status":
        return _do_crawl_status(arguments, api_key)
    if name == "extract":
        return _do_extract(arguments, api_key)
    return _error_text("Unknown tool: %s" % name)


def handle_rpc(req, api_key):
    """分发单条 JSON-RPC 请求，返回响应 dict；通知（无 id）返回 None。"""
    req_id = req.get("id")
    method = req.get("method")
    params = req.get("params", {}) or {}

    if method == "initialize":
        return make_result(req_id, {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "firecrawl", "version": "1.0.0"},
        })
    if method == "notifications/initialized":
        return None  # 通知无需响应
    if method == "tools/list":
        return make_result(req_id, {"tools": tools_list()})
    if method == "tools/call":
        return make_result(req_id, tools_call(params.get("name"), params.get("arguments", {}), api_key))
    return make_error(req_id, -32601, "Method not found: %s" % method)


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _send_json(self, status, obj):
        body = json.dumps(obj).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send_empty(self, status):
        self.send_response(status)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_GET(self):
        if self.path in ("/health", "/"):
            self._send_json(200, {
                "module_id": "firecrawl",
                "version": "1.0.0",
                "status": "ok",
                "capabilities": ["scrape", "crawl", "crawl_status", "extract"],
            })
            return
        self._send_json(404, {"error": "not found"})

    def do_POST(self):
        # / 与 /mcp 均接受 JSON-RPC（网关 mcp_http 协议 POST 到 endpoint 根）
        if self.path not in ("/", "/mcp"):
            self._send_json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(length) if length else b""
        try:
            req = json.loads(raw.decode("utf-8")) if raw else {}
        except Exception as e:
            self._send_json(200, make_error(None, -32700, "Parse error: %s" % e))
            return

        # API Key 由网关 mcp_server_config.headers 的 X-Firecrawl-Key 注入（逐请求）
        api_key = self.headers.get("X-Firecrawl-Key", "")

        # JSON-RPC 批量请求：逐项分发，过滤通知（None）
        if isinstance(req, list):
            resp = [r for r in (handle_rpc(item, api_key) for item in req) if r]
            if resp:
                self._send_json(200, resp)
            else:
                self._send_empty(200)
            return

        resp = handle_rpc(req, api_key)
        if resp is None:
            self._send_empty(200)  # 通知
            return
        self._send_json(200, resp)

    def log_message(self, fmt, *args):
        pass  # 静默默认访问日志


def main():
    server = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    server.serve_forever()


if __name__ == "__main__":
    main()
