"""
MCP Hello 示例服务器（最小 Streamable HTTP 实现）。
仅用于演示 MCP 工具如何通过 claw 网关的 mcp driver 接入：
  - GET  /health       普通健康检查
  - POST /mcp          JSON-RPC tools/list + tools/call
运行：uvicorn main:app --host 0.0.0.0 --port 8081
"""

import json
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

app = FastAPI(title="MCP Hello Example")


def make_error(id_: object, code: int, message: str):
    return JSONResponse({
        "jsonrpc": "2.0",
        "id": id_,
        "error": {"code": code, "message": message},
    })


def make_result(id_: object, result: dict):
    return JSONResponse({
        "jsonrpc": "2.0",
        "id": id_,
        "result": result,
    })


@app.get("/health")
def health():
    return {
        "module_id": "mcp-hello",
        "status": "ok",
        "version": "1.0.0",
        "capabilities": ["hello", "echo"],
    }


@app.post("/mcp")
async def mcp_rpc(request: Request):
    try:
        body = await request.json()
    except Exception as e:
        return make_error(None, -32700, f"Parse error: {e}")

    req_id = body.get("id")
    method = body.get("method")
    params = body.get("params", {})

    if method == "tools/list":
        return make_result(req_id, {
            "tools": [
                {
                    "name": "hello",
                    "description": "返回问候语",
                    "inputSchema": {
                        "type": "object",
                        "properties": {
                            "name": {"type": "string", "description": "称呼"},
                        },
                        "required": ["name"],
                    },
                },
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
            ]
        })

    if method == "tools/call":
        name = params.get("name")
        arguments = params.get("arguments", {})
        if name == "hello":
            user = arguments.get("name", "world")
            return make_result(req_id, {
                "content": [
                    {"type": "text", "text": f"Hello, {user}!"}
                ]
            })
        if name == "echo":
            msg = arguments.get("message", "")
            return make_result(req_id, {
                "content": [
                    {"type": "text", "text": msg}
                ]
            })
        return make_result(req_id, {
            "isError": True,
            "content": [
                {"type": "text", "text": f"Unknown tool: {name}"}
            ]
        })

    return make_error(req_id, -32601, f"Method not found: {method}")


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8081)
