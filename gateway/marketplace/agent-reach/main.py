"""
Agent-Reach Marketplace Module

Eleball 集市技能模块标准接口：
- GET  /health           健康检查与能力声明
- POST /execute          执行指定 action（execute 传输，凭证经 params.credentials 注入）
- POST /mcp              MCP Streamable HTTP JSON-RPC（mcp_http 传输，凭证经请求头注入）
- POST /                 同 /mcp（网关 mcp_http 经 mcpModuleBaseURL 把端点收敛为根路径）

凭证来源随传输协议不同：
- execute：网关把 SKU manifest 声明的凭证注入 params.credentials（多用户按 user_id 隔离 cookie）。
- mcp_http：网关把 module.json mcp_server_config.headers 中的 ${credentials.KEY} 模板
  替换为用户配置的凭证值后作为 HTTP 请求头注入；模块从请求头读取并写入 cookie 目录。
  claw 单用户场景使用固定 user_id（AGENT_REACH_USER_ID，默认 claw）；多用户云端走 execute 传输。
"""

import json
import os
import re
import subprocess
from pathlib import Path
from typing import Any

import uvicorn
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel

app = FastAPI(title="Eleball Agent-Reach Module")

MODULE_ID = "agent-reach"
VERSION = "1.0.0"

# 模块支持的能力清单（与 ToolManifest actions / MCP 工具名对应）
CAPABILITIES = [
    "web_read",
    "search",
    "youtube_subtitles",
    "bilibili_search",
    "github_repo",
    "github_search",
    "rss_read",
    "social_search",
    "social_read",
]

# 需要 Cookie 的平台（社交 + B站 + YouTube）
COOKIE_PLATFORMS = {"twitter", "reddit", "xiaohongshu", "bilibili", "youtube"}

DATA_ROOT = Path(os.environ.get("AGENT_REACH_DATA_ROOT", "/data/cookies"))

# mcp_http 传输下 claw 单用户的固定 user_id（cookie 隔离目录）。
# 网关 mcp_http 不转发 user_id，claw 单用户故用固定值；多用户云端走 execute 传输（/execute 带 user_id）。
MCP_USER_ID = os.environ.get("AGENT_REACH_USER_ID", "claw")

# mcp_http 凭证请求头 -> 内部凭证 key 的映射（与 module.json mcp_server_config.headers 对应）。
# 网关把 ${credentials.KEY} 替换为凭证值后以这些头名注入；空头值视为未配置，跳过。
CREDENTIAL_HEADERS = {
    "X-Twitter-Cookie": "twitter_cookie",
    "X-Reddit-Cookie": "reddit_cookie",
    "X-Xiaohongshu-Cookie": "xiaohongshu_cookie",
    "X-Bilibili-Cookie": "bilibili_cookie",
    "X-YouTube-Cookie": "youtube_cookie",
    "X-Github-Token": "github_token",
}

# MCP 工具清单（9 个，name 即 action，与 CAPABILITIES 对应）。
# inputSchema 透传给网关 DeriveSKUs 合成 SKU 的 parameters。
MCP_TOOLS = [
    {
        "name": "web_read",
        "description": "读取任意网页正文为 markdown（经 Exa 渲染，适合公众号/新闻/文档）",
        "inputSchema": {
            "type": "object",
            "properties": {"query": {"type": "string", "description": "网页 URL 或域名"}},
            "required": ["query"],
        },
    },
    {
        "name": "search",
        "description": "全网语义搜索（Exa，经 mcporter 零配置接入，无需 API Key）；platform=bilibili 时走B站视频搜索",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "搜索关键词"},
                "limit": {"type": "integer", "description": "返回条数", "default": 5},
                "platform": {"type": "string", "enum": ["bilibili"], "description": "可选，指定 bilibili 走B站搜索"},
            },
            "required": ["query"],
        },
    },
    {
        "name": "youtube_subtitles",
        "description": "提取 YouTube 视频字幕（en/zh-CN/zh-TW/ja）",
        "inputSchema": {
            "type": "object",
            "properties": {"query": {"type": "string", "description": "YouTube 视频 URL"}},
            "required": ["query"],
        },
    },
    {
        "name": "bilibili_search",
        "description": "搜索B站视频",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "搜索关键词"},
                "limit": {"type": "integer", "description": "返回条数", "default": 5},
            },
            "required": ["query"],
        },
    },
    {
        "name": "github_repo",
        "description": "查看 GitHub 仓库信息（名称/描述/星数/语言/默认分支）",
        "inputSchema": {
            "type": "object",
            "properties": {"query": {"type": "string", "description": "仓库名 owner/repo"}},
            "required": ["query"],
        },
    },
    {
        "name": "github_search",
        "description": "按星数搜索 GitHub 仓库",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "搜索关键词"},
                "limit": {"type": "integer", "description": "返回条数", "default": 5},
            },
            "required": ["query"],
        },
    },
    {
        "name": "rss_read",
        "description": "读取 RSS/Atom 订阅源（返回最近 20 条标题/链接/摘要）",
        "inputSchema": {
            "type": "object",
            "properties": {"query": {"type": "string", "description": "RSS/Atom 订阅源 URL"}},
            "required": ["query"],
        },
    },
    {
        "name": "social_search",
        "description": "搜索社媒内容（Twitter/小红书/Reddit/B站）",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "搜索关键词"},
                "social_platform": {"type": "string", "enum": ["twitter", "xiaohongshu", "reddit", "bilibili"], "description": "社交平台"},
                "limit": {"type": "integer", "description": "返回条数", "default": 5},
            },
            "required": ["query", "social_platform"],
        },
    },
    {
        "name": "social_read",
        "description": "读取社媒帖子详情（Twitter/小红书/Reddit/B站）",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "帖子链接或关键词"},
                "social_platform": {"type": "string", "enum": ["twitter", "xiaohongshu", "reddit", "bilibili"], "description": "社交平台"},
                "limit": {"type": "integer", "description": "返回条数", "default": 5},
            },
            "required": ["query", "social_platform"],
        },
    },
]


class ExecuteRequest(BaseModel):
    action: str
    params: dict[str, Any] = {}
    user_id: str


def user_cookie_dir(user_id: str) -> Path:
    d = DATA_ROOT / user_id
    d.mkdir(parents=True, exist_ok=True, mode=0o700)
    return d


def shell_safe(value: str) -> None:
    """简单校验，防止 shell 元字符注入"""
    if re.search(r"[;&|`$()<>\\\n]", value):
        raise ValueError("参数包含非法 shell 字符")


def run(cmd: list[str], user_id: str, timeout: int = 60, github_token: str = "", use_user_home: bool = True) -> dict:
    """在隔离的 HOME 目录下执行命令。

    use_user_home=False 时把 HOME 指向容器 /root--
    供 keyless 工具（如经 mcporter 调用的 Exa 搜索）使用构建期以 root 注册的 mcporter 配置，
    这些工具不需要任何用户凭证/Cookie。容器以 root 运行（Dockerfile 无 USER 指令）。
    """
    env = os.environ.copy()
    if use_user_home:
        env["HOME"] = str(user_cookie_dir(user_id))
    else:
        env["HOME"] = "/root"
    if github_token:
        env["GH_TOKEN"] = github_token
    try:
        proc = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            env=env,
            timeout=timeout,
            check=False,
        )
        if proc.returncode != 0:
            return {
                "content": "",
                "error": f"exit={proc.returncode}\n{proc.stderr or proc.stdout}",
                "error_code": "agent_reach_execution_failed",
            }
        return {"content": proc.stdout, "sources": extract_urls(proc.stdout)}
    except subprocess.TimeoutExpired:
        return {
            "content": "",
            "error": "执行超时",
            "error_code": "agent_reach_timeout",
        }
    except FileNotFoundError as e:
        return {
            "content": "",
            "error": f"命令不存在: {e}",
            "error_code": "agent_reach_command_missing",
        }


def extract_urls(text: str) -> list[str]:
    urls = []
    for word in text.split():
        if word.startswith("http://") or word.startswith("https://"):
            urls.append(word.rstrip(",.;:]"))
    return urls


def _parse_cookie_pairs(cookie: str) -> dict[str, str]:
    """从 'a=1; b=2' 或 JSON 对象中解析键值对。"""
    cookie = cookie.strip()
    if not cookie:
        return {}
    if cookie.startswith("{"):
        try:
            return json.loads(cookie)
        except json.JSONDecodeError:
            return {}
    pairs: dict[str, str] = {}
    for part in cookie.replace(";", " ").split():
        if "=" in part:
            k, v = part.split("=", 1)
            pairs[k.strip()] = v.strip()
    return pairs


def _write_netscape_cookies(home: Path, domain: str, cookie: str, filename: str) -> Path:
    """将 Cookie Header String 写成 Netscape cookies.txt，供 yt-dlp 等工具使用。"""
    path = home / filename
    pairs = _parse_cookie_pairs(cookie)
    lines = ["# Netscape HTTP Cookie File"]
    for name, value in pairs.items():
        lines.append(f"{domain}\tTRUE\t/\tFALSE\t0\t{name}\t{value}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return path


def save_cookies(user_id: str, cookies: dict[str, str]) -> None:
    """将网关传入的 Cookie 写入用户隔离目录，供上游 CLI 读取"""
    if not cookies:
        return
    home = user_cookie_dir(user_id)
    env = {**os.environ, "HOME": str(home)}
    for platform, cookie in cookies.items():
        if not cookie:
            continue
        if platform == "twitter":
            subprocess.run(
                ["agent-reach", "configure", "twitter-cookies", cookie],
                capture_output=True,
                text=True,
                env=env,
                check=False,
            )
        elif platform in ("xiaohongshu", "xhs"):
            subprocess.run(
                ["agent-reach", "configure", "xhs-cookies", cookie],
                capture_output=True,
                text=True,
                env=env,
                check=False,
            )
        elif platform == "reddit":
            config_dir = home / ".config" / "rdt-cli"
            config_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
            (config_dir / "credential.json").write_text(cookie, encoding="utf-8")
        elif platform == "bilibili":
            # 供 bilibili-cli 读取：优先写入 credential.json（键值对形式）
            pairs = _parse_cookie_pairs(cookie)
            bili_dir = home / ".bilibili-cli"
            bili_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
            (bili_dir / "credential.json").write_text(
                json.dumps(pairs, ensure_ascii=False, indent=2), encoding="utf-8"
            )
            # 同时保留 Netscape cookies.txt 备用（yt-dlp / curl 等）
            _write_netscape_cookies(home, ".bilibili.com", cookie, "bilibili_cookies.txt")
        elif platform == "youtube":
            _write_netscape_cookies(home, ".youtube.com", cookie, "youtube_cookies.txt")


def build_command(action: str, params: dict, user_id: str) -> list[str]:
    query = str(params.get("query", ""))
    if not query:
        raise ValueError("query 不能为空")
    shell_safe(query)
    limit = int(params.get("limit", 5))

    if action == "web_read":
        url = query if query.startswith("http://") or query.startswith("https://") else f"https://{query}"
        # 经 Exa web_fetch 读取网页正文为 markdown（mcporter 零配置接入，同 search 通道，国内可达）。
        # 旧方案 curl https://r.jina.ai/{url}：r.jina.ai（Jina Reader，Cloudflare）国内 TCP 不可达，
        # 表现为 exit=7 或网关 30s 超时；search 走 mcp.exa.ai 可达，故 web_read 改走同一通道。
        urls_json = json.dumps([url])
        call = f"exa.web_fetch_exa(urls: {urls_json}, maxCharacters: 20000)"
        return ["mcporter", "call", call]

    if action == "search":
        # 兼容 LLM 直接指定 platform 的情况（如B站视频搜索）
        platform = str(params.get("platform", "")).lower()
        if platform == "bilibili":
            return ["bili", "search", query, "--type", "video", "-n", str(limit)]
        # 默认走 Exa MCP（经 mcporter 零配置接入，无需 API Key；exa server 在镜像构建期注册）
        q = json.dumps(query)
        call = f"exa.web_search_exa(query: {q}, numResults: {limit})"
        return ["mcporter", "call", call]

    if action == "youtube_subtitles":
        cookies_file = user_cookie_dir(user_id) / "youtube_cookies.txt"
        cmd = [
            "yt-dlp",
            "--dump-json",
            "--write-sub",
            "--skip-download",
            "--sub-langs", "en,zh-CN,zh-TW,ja",
            "-o", "/tmp/yt_%(id)s",
        ]
        if cookies_file.exists():
            cmd.extend(["--cookies", str(cookies_file)])
        cmd.append(query)
        return cmd

    if action == "bilibili_search":
        return ["bili", "search", query, "--type", "video", "-n", str(limit)]

    if action == "github_repo":
        return ["gh", "repo", "view", query, "--json", "name,description,url,stargazerCount,primaryLanguage,defaultBranch"]

    if action == "github_search":
        return ["gh", "search", "repos", query, "--sort", "stars", "--limit", str(limit), "--json", "name,owner,description,url,stargazerCount"]

    if action == "rss_read":
        script = (
            "import feedparser, json, sys; "
            f"d = feedparser.parse({json.dumps(query)}); "
            "items = [{\"title\": e.get(\"title\"), \"link\": e.get(\"link\"), \"summary\": (e.get(\"summary\") or \"\")[:500]} for e in d.entries[:20]]; "
            "print(json.dumps({\"feed_title\": d.feed.get(\"title\"), \"items\": items}, ensure_ascii=False))"
        )
        return ["python3", "-c", script]

    if action in ("social_search", "social_read"):
        platform = params.get("social_platform", "")
        if platform == "bilibili":
            return ["bili", "search", query, "--type", "video", "-n", str(limit), "--json"]
        if platform == "twitter":
            return ["twitter", "search", query, "-n", str(limit)]
        if platform == "reddit":
            return ["rdt", "search", query, "--limit", str(limit)]
        if platform in ("xiaohongshu", "xhs"):
            return ["xhs", "search", query, "--limit", str(limit)]
        raise ValueError(f"不支持的社交平台: {platform}")

    raise ValueError(f"不支持的 action: {action}")


# ---------------------------------------------------------------------------
# 标准模块接口（execute 传输）
# ---------------------------------------------------------------------------

@app.get("/health")
def health():
    return {
        "module_id": MODULE_ID,
        "version": VERSION,
        "status": "ok",
        "capabilities": CAPABILITIES,
    }


@app.post("/execute")
def execute(req: ExecuteRequest):
    # 从 params.credentials 读取网关注入的用户凭证（Cookie / Token 等）
    credentials = req.params.get("credentials") or {}
    if not isinstance(credentials, dict):
        credentials = {}

    # 将凭证 key 归一化为平台名，例如 youtube_cookie -> youtube
    cookies = _normalize_credentials(credentials)
    if cookies:
        save_cookies(req.user_id, cookies)

    # GitHub Token 通过环境变量 GH_TOKEN 传递给 gh CLI
    github_token = credentials.get("github_token") or credentials.get("gh_token") or ""

    try:
        cmd = build_command(req.action, req.params, req.user_id)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))

    # search / web_read 经 mcporter 调 Exa（keyless，无需用户凭证），用容器默认 HOME 以读取构建期注册的 mcporter 配置
    use_user_home = req.action not in ("search", "web_read")
    timeout = 120 if req.action in ("youtube_subtitles", "social_search", "social_read") else 60
    result = run(cmd, req.user_id, timeout, github_token=github_token, use_user_home=use_user_home)
    return result


def _normalize_credentials(credentials: dict[str, Any]) -> dict[str, str]:
    """将 SKU manifest 里的凭证 key 映射为模块内部平台名。"""
    mapping = {
        "twitter_cookie": "twitter",
        "reddit_cookie": "reddit",
        "xiaohongshu_cookie": "xiaohongshu",
        "xhs_cookie": "xiaohongshu",
        "bilibili_cookie": "bilibili",
        "youtube_cookie": "youtube",
    }
    result: dict[str, str] = {}
    for key, value in credentials.items():
        if not isinstance(value, str) or not value:
            continue
        platform = mapping.get(key, key)
        if platform in COOKIE_PLATFORMS:
            result[platform] = value
    return result


# ---------------------------------------------------------------------------
# MCP Streamable HTTP 接口（mcp_http 传输，claw）
# 凭证经网关 mcp_server_config.headers 模板（${credentials.KEY}）注入为请求头，
# 模块从请求头读取后复用 /execute 的 save_cookies/build_command/run 逻辑。
# ---------------------------------------------------------------------------

def _mcp_result(req_id: Any, result: dict) -> JSONResponse:
    return JSONResponse({"jsonrpc": "2.0", "id": req_id, "result": result})


def _mcp_error(req_id: Any, code: int, message: str) -> JSONResponse:
    return JSONResponse({"jsonrpc": "2.0", "id": req_id, "error": {"code": code, "message": message}})


def _credentials_from_headers(request: Request) -> dict[str, str]:
    """从 mcp_http 请求头读取凭证（网关经 mcp_server_config.headers 模板注入）。空头值视为未配置，跳过。"""
    creds: dict[str, str] = {}
    for header, key in CREDENTIAL_HEADERS.items():
        value = request.headers.get(header, "")
        if value:
            creds[key] = value
    return creds


def _result_to_mcp(req_id: Any, result: dict) -> JSONResponse:
    """把 execute 风格的 {content,error,...} 结果转为 MCP tools/call 响应。"""
    if result.get("error"):
        return _mcp_result(req_id, {"isError": True, "content": [{"type": "text", "text": str(result["error"])}]})
    return _mcp_result(req_id, {"content": [{"type": "text", "text": str(result.get("content", ""))}]})


def _handle_tool_call(req_id: Any, name: str, arguments: dict, request: Request) -> JSONResponse:
    """执行 MCP tools/call：从请求头取凭证 -> save_cookies -> build_command -> run。"""
    if name not in CAPABILITIES:
        return _mcp_result(req_id, {"isError": True, "content": [{"type": "text", "text": f"未知工具: {name}"}]})

    credentials = _credentials_from_headers(request)
    cookies = _normalize_credentials(credentials)
    if cookies:
        save_cookies(MCP_USER_ID, cookies)
    github_token = credentials.get("github_token") or credentials.get("gh_token") or ""

    params = dict(arguments or {})
    try:
        cmd = build_command(name, params, MCP_USER_ID)
    except ValueError as e:
        return _mcp_result(req_id, {"isError": True, "content": [{"type": "text", "text": str(e)}]})

    # search / web_read 经 mcporter 调 Exa（keyless，无需用户凭证），用容器默认 HOME 以读取构建期注册的 mcporter 配置
    use_user_home = name not in ("search", "web_read")
    timeout = 120 if name in ("youtube_subtitles", "social_search", "social_read") else 60
    result = run(cmd, MCP_USER_ID, timeout, github_token=github_token, use_user_home=use_user_home)
    return _result_to_mcp(req_id, result)


async def mcp_rpc(request: Request) -> JSONResponse:
    """MCP Streamable HTTP JSON-RPC 入口（initialize / notifications/initialized / tools/list / tools/call）。"""
    try:
        body = await request.json()
    except Exception as e:
        return _mcp_error(None, -32700, f"Parse error: {e}")

    req_id = body.get("id")
    method = body.get("method", "")
    params = body.get("params", {}) or {}

    if method == "initialize":
        return _mcp_result(req_id, {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": MODULE_ID, "version": VERSION},
        })

    if method == "notifications/initialized":
        # 通知无 id；返回空 result 以满足网关 do() 的 JSON 解码
        return _mcp_result(req_id, {})

    if method == "tools/list":
        return _mcp_result(req_id, {"tools": MCP_TOOLS})

    if method == "tools/call":
        return _handle_tool_call(req_id, params.get("name", ""), params.get("arguments", {}), request)

    return _mcp_error(req_id, -32601, f"Method not found: {method}")


# 网关 mcp_http 经 mcpModuleBaseURL 把端点收敛为根路径，故根路径与 /mcp 均挂同一处理函数。
app.add_api_route("/mcp", mcp_rpc, methods=["POST"])
app.add_api_route("/", mcp_rpc, methods=["POST"])


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8080)
