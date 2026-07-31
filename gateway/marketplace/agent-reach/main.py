"""
Agent-Reach Marketplace Module

Eleball 集市技能模块标准接口：
- GET  /health           健康检查与能力声明
- POST /execute          执行指定 action

Cookie / API Key 等用户凭证由网关根据 SKU manifest 的 credentials 声明注入到
params.credentials 中。模块按用户隔离存储在 /data/cookies/{user_id}/，
通过为子进程设置 HOME 实现。
"""

import json
import os
import re
import subprocess
from pathlib import Path
from typing import Any

import requests
import uvicorn
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

app = FastAPI(title="Eleball Agent-Reach Module")

MODULE_ID = "agent-reach"
VERSION = "1.0.0"

# 模块支持的能力清单（与 ToolManifest actions 对应）
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


def run(cmd: list[str], user_id: str, timeout: int = 60, github_token: str = "") -> dict:
    """在隔离的 HOME 目录下执行命令"""
    env = os.environ.copy()
    env["HOME"] = str(user_cookie_dir(user_id))
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
        return ["curl", "-s", "-L", "--max-time", "30", f"https://r.jina.ai/{url}"]

    if action == "search":
        # 兼容 LLM 直接指定 platform 的情况（如B站视频搜索）
        platform = str(params.get("platform", "")).lower()
        if platform == "bilibili":
            return ["bili", "search", query, "--type", "video", "-n", str(limit)]
        # 默认走 exa HTTP API（由 execute 直接处理，需 exa_api_key 凭证）
        return None

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


def do_search(query: str, limit: int, exa_key: str) -> dict:
    """调用 Exa HTTP API 执行全网语义搜索。exa_key 由网关注入 params.credentials.exa_api_key。"""
    if not exa_key:
        return {
            "content": "",
            "error": "缺少 Exa API Key，请在秘技凭证配置中填写 exa_api_key",
            "error_code": "agent_reach_credential_missing",
        }
    if not query:
        raise HTTPException(status_code=400, detail="query 不能为空")
    try:
        resp = requests.post(
            "https://api.exa.ai/search",
            headers={"x-api-key": exa_key, "Content-Type": "application/json"},
            json={"query": query, "numResults": limit, "contents": {"text": {"maxCharacters": 1000}}},
            timeout=30,
        )
        if resp.status_code != 200:
            return {
                "content": "",
                "error": f"exa HTTP {resp.status_code}: {resp.text[:200]}",
                "error_code": "agent_reach_execution_failed",
            }
        results = resp.json().get("results", [])
        lines, sources = [], []
        for r in results:
            title = r.get("title", "")
            url = r.get("url", "")
            text = r.get("text", "")
            sources.append(url)
            lines.append(f"## {title}\n{url}\n{text}")
        return {"content": "\n\n".join(lines), "sources": sources}
    except requests.RequestException as e:
        return {
            "content": "",
            "error": f"exa 请求失败: {e}",
            "error_code": "agent_reach_execution_failed",
        }


# ---------------------------------------------------------------------------
# 标准模块接口
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

    # search 默认走 exa HTTP API（需 exa_api_key 凭证，由网关注入 params.credentials）
    if cmd is None:
        exa_key = credentials.get("exa_api_key") or ""
        return do_search(
            query=str(req.params.get("query", "")),
            limit=int(req.params.get("limit", 5) or 5),
            exa_key=exa_key,
        )

    timeout = 120 if req.action in ("youtube_subtitles", "social_search", "social_read") else 60
    result = run(cmd, req.user_id, timeout, github_token=github_token)
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


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8080)
