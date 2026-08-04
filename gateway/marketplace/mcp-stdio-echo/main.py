#!/usr/bin/env python3
"""MCP stdio Echo 示例服务器（最小 stdio JSON-RPC 实现）。

仅用于演示本地 stdio MCP 如何经网关 SkillRuntimeManager 拉起、探活、调用：
  - stdin/stdout 换行分隔 JSON（NDJSON）的 JSON-RPC 2.0
  - initialize handshake -> tools/list -> tools/call
  - 暴露 echo（回显）、ping（返回 pong）两个工具

运行：python main.py（由网关 process deployment 自动 spawn，无需手动启动）
"""

import json
import sys


def make_result(req_id, result):
    return {"jsonrpc": "2.0", "id": req_id, "result": result}


def make_error(req_id, code, message):
    return {"jsonrpc": "2.0", "id": req_id, "error": {"code": code, "message": message}}


def tools_list():
    return [
        {
            "name": "echo",
            "description": "回显输入内容",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "message": {"type": "string", "description": "任意内容"},
                },
                "required": ["message"],
            },
        },
        {
            "name": "ping",
            "description": "返回 pong",
            "inputSchema": {"type": "object", "properties": {}},
        },
    ]


def tools_call(name, arguments):
    if name == "echo":
        msg = arguments.get("message", "")
        return {"content": [{"type": "text", "text": msg}]}
    if name == "ping":
        return {"content": [{"type": "text", "text": "pong"}]}
    return {"isError": True, "content": [{"type": "text", "text": f"Unknown tool: {name}"}]}


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except Exception as e:
            sys.stdout.write(json.dumps(make_error(None, -32700, f"Parse error: {e}")) + "\n")
            sys.stdout.flush()
            continue

        req_id = req.get("id")
        method = req.get("method")
        params = req.get("params", {}) or {}

        if method == "initialize":
            resp = make_result(req_id, {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "mcp-stdio-echo", "version": "1.0.0"},
            })
        elif method == "notifications/initialized":
            # notification（无 id），无需响应
            continue
        elif method == "tools/list":
            resp = make_result(req_id, {"tools": tools_list()})
        elif method == "tools/call":
            resp = make_result(req_id, tools_call(params.get("name"), params.get("arguments", {})))
        else:
            resp = make_error(req_id, -32601, f"Method not found: {method}")

        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
