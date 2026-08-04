#!/usr/bin/env python3
"""Firecrawl stdio MCP 服务器。

经 stdio JSON-RPC（NDJSON）暴露 scrape / crawl / extract 三工具，内部调用 Firecrawl API
（FIRECRAWL_BASE_URL + FIRECRAWL_API_KEY 环境变量，由网关 env 模板
${credentials.firecrawl_api_key} 在 spawn 时注入，见阶段 D1）。

仅依赖标准库（urllib），无需 pip install，降低用户安装成本（对标 mcp-stdio-echo 的零依赖）。
auto_sku=true：网关探活拿到 tools/list 后自动派生三份可购买 SKU，免手写 skus/*.json。

运行：python main.py（由网关 process deployment 自动 spawn，无需手动启动）。
"""

import json
import os
import sys
import urllib.error
import urllib.request

FIRECRAWL_BASE_URL = os.environ.get("FIRECRAWL_BASE_URL", "https://api.firecrawl.dev").rstrip("/")
FIRECRAWL_API_KEY = os.environ.get("FIRECRAWL_API_KEY", "")


def make_result(req_id, result):
    return {"jsonrpc": "2.0", "id": req_id, "result": result}


def make_error(req_id, code, message):
    return {"jsonrpc": "2.0", "id": req_id, "error": {"code": code, "message": message}}


def tools_list():
    return [
        {
            "name": "scrape",
            "description": "将单个网页转换为干净 Markdown，返回标题、URL、描述等元数据",
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
            "description": "对指定网站启动批量爬取任务，返回任务 ID",
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
            "name": "extract",
            "description": "按 JSON Schema 从网页中提取结构化数据",
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


def _firecrawl_request(path, payload):
    """调用 Firecrawl API，返回 (data, error)。"""
    if not FIRECRAWL_API_KEY:
        return None, "缺少 FIRECRAWL_API_KEY（请在模块凭证配置 firecrawl_api_key）"
    url = FIRECRAWL_BASE_URL + path
    body = json.dumps(payload).encode("utf-8")
    headers = {"Content-Type": "application/json", "x-api-key": FIRECRAWL_API_KEY}
    req = urllib.request.Request(url, data=body, headers=headers, method="POST")
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


def _do_scrape(args):
    url = args.get("url")
    if not url:
        return _error_text("缺少 url 参数")
    payload = {"url": url, "formats": args.get("formats", ["markdown"])}
    if "onlyMainContent" in args:
        payload["onlyMainContent"] = args["onlyMainContent"]
    data, err = _firecrawl_request("/v1/scrape", payload)
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


def _do_crawl(args):
    url = args.get("url")
    if not url:
        return _error_text("缺少 url 参数")
    payload = {"url": url}
    if "limit" in args:
        payload["limit"] = args["limit"]
    data, err = _firecrawl_request("/v1/crawl", payload)
    if err:
        return _error_text(err)
    if not data.get("success"):
        return _error_text(data.get("error", "Firecrawl crawl 失败"))
    result = {"job_id": data.get("id"), "status": "started", "check_url": data.get("url")}
    return _text(json.dumps(result, ensure_ascii=False))


def _do_extract(args):
    urls = args.get("urls")
    if not urls:
        return _error_text("缺少 urls 参数")
    payload = {"urls": urls}
    if "schema" in args:
        payload["schema"] = args["schema"]
    if "prompt" in args:
        payload["prompt"] = args["prompt"]
    data, err = _firecrawl_request("/v1/extract", payload)
    if err:
        return _error_text(err)
    if not data.get("success"):
        return _error_text(data.get("error", "Firecrawl extract 失败"))
    return _text(json.dumps(data.get("data", {}), ensure_ascii=False))


def tools_call(name, arguments):
    if name == "scrape":
        return _do_scrape(arguments)
    if name == "crawl":
        return _do_crawl(arguments)
    if name == "extract":
        return _do_extract(arguments)
    return _error_text("Unknown tool: %s" % name)


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except Exception as e:
            sys.stdout.write(json.dumps(make_error(None, -32700, "Parse error: %s" % e)) + "\n")
            sys.stdout.flush()
            continue

        req_id = req.get("id")
        method = req.get("method")
        params = req.get("params", {}) or {}

        if method == "initialize":
            resp = make_result(req_id, {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "firecrawl", "version": "1.0.0"},
            })
        elif method == "notifications/initialized":
            continue
        elif method == "tools/list":
            resp = make_result(req_id, {"tools": tools_list()})
        elif method == "tools/call":
            resp = make_result(req_id, tools_call(params.get("name"), params.get("arguments", {})))
        else:
            resp = make_error(req_id, -32601, "Method not found: %s" % method)

        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
