"""
Eleball-claw 内置模块：stt（语音识别 / 百度 ASR）

claw 端 STT 下沉为集市模块（仿 search-web）。百度 ASR 的 app_id/api_key/secret_key
作为模块凭证：优先从 /execute 请求的 params.credentials 读取（web 字段配置后由
moduleDriver 注入），回退到环境变量（BAIDU_APP_ID/BAIDU_API_KEY/BAIDU_SECRET_KEY，
便于 docker-compose 直接配置）。

逻辑移植自 gateway/internal/service/stt_service.go（百度短语音识别 REST API：
oauth 取 access_token -> POST /server_api 传 base64 音频）。

标准接口（见 docs/tool-driver-guide.md §9）：
- GET  /health  上报状态与能力
- POST /execute action=transcribe，params={audio: base64 字符串, language?}
"""

import base64
import logging
import os
import time
from typing import Any

import requests
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("stt-module")

app = FastAPI(title="Eleball stt Module", version="1.0.0")

MODULE_ID = "stt"
VERSION = "1.0.0"
CAPABILITIES = ["transcribe"]

BAIDU_TOKEN_URL = "https://aip.baidubce.com/oauth/2.0/token"
BAIDU_ASR_BASE = os.environ.get("BAIDU_ASR_BASE", "https://vop.baidu.com")
MAX_AUDIO_MB = int(os.environ.get("STT_MAX_AUDIO_MB", "10"))

# access_token 缓存（进程内）
_token_cache: dict[str, Any] = {"token": "", "expire_at": 0.0}


class ExecuteRequest(BaseModel):
    action: str = Field(..., description="操作名：transcribe")
    params: dict[str, Any] = Field(default_factory=dict, description="业务参数")
    user_id: str = Field(default="", description="当前用户 ID（本模块不使用）")


class HealthResponse(BaseModel):
    module_id: str
    version: str
    status: str
    capabilities: list[str]


def _read_credentials(params: dict[str, Any]) -> tuple[str, str, str]:
    """读取百度 ASR 凭证：params.credentials 优先，环境变量回退。"""
    creds = params.get("credentials") or {}
    app_id = creds.get("baidu_app_id") or os.environ.get("BAIDU_APP_ID", "")
    api_key = creds.get("baidu_api_key") or os.environ.get("BAIDU_API_KEY", "")
    secret_key = creds.get("baidu_secret_key") or os.environ.get("BAIDU_SECRET_KEY", "")
    return app_id, api_key, secret_key


def _get_access_token(api_key: str, secret_key: str) -> str:
    """获取并缓存百度 access_token（移植自 stt_service.go getAccessToken）。"""
    if _token_cache["token"] and time.time() + 300 < _token_cache["expire_at"]:
        return _token_cache["token"]

    resp = requests.post(
        BAIDU_TOKEN_URL,
        params={"grant_type": "client_credentials", "client_id": api_key, "client_secret": secret_key},
        headers={"Content-Type": "application/json", "Accept": "application/json"},
        timeout=15,
    )
    if resp.status_code != 200:
        raise RuntimeError(f"百度 token 接口返回 {resp.status_code}: {resp.text}")
    data = resp.json()
    if data.get("error"):
        raise RuntimeError(f"百度 token 错误: {data.get('error')} ({data.get('error_description')})")
    token = data.get("access_token", "")
    if not token:
        raise RuntimeError("百度 token 为空")
    expires_in = data.get("expires_in") or 2592000
    _token_cache["token"] = token
    _token_cache["expire_at"] = time.time() + expires_in
    return token


def _audio_format(audio: bytes) -> str:
    """根据文件魔数判断音频格式（移植自 stt_service.go audioFormat）。"""
    if len(audio) < 12:
        return "pcm"
    if audio[4:8] == b"ftyp":
        return "m4a"
    if audio[0:4] == b"RIFF" and audio[8:12] == b"WAVE":
        return "wav"
    if audio[0:5] == b"#!AMR":
        return "amr"
    return "pcm"


def transcribe(audio_b64: str, app_id: str, api_key: str, secret_key: str) -> str:
    """调用百度语音识别，返回文本（移植自 stt_service.go baiduRecognize）。"""
    try:
        audio = base64.b64decode(audio_b64)
    except Exception as e:
        raise HTTPException(status_code=400, detail=f"audio base64 解码失败: {e}")

    if not audio:
        raise HTTPException(status_code=400, detail="音频文件为空")
    if len(audio) > MAX_AUDIO_MB * 1024 * 1024:
        raise HTTPException(status_code=400, detail=f"音频文件超过 {MAX_AUDIO_MB} MB 限制")

    token = _get_access_token(api_key, secret_key)
    fmt = _audio_format(audio)
    rate = 8000 if fmt == "amr" else 16000

    body = {
        "format": fmt,
        "rate": rate,
        "channel": 1,
        "cuid": app_id,
        "token": token,
        "speech": base64.b64encode(audio).decode("ascii"),
        "len": len(audio),
        "dev_pid": 1537,  # 普通话(纯中文识别)
    }
    resp = requests.post(
        f"{BAIDU_ASR_BASE}/server_api",
        json=body,
        headers={"Content-Type": "application/json", "Accept": "application/json"},
        timeout=30,
    )
    if resp.status_code != 200:
        raise RuntimeError(f"百度语音识别接口返回 {resp.status_code}: {resp.text}")
    data = resp.json()
    if data.get("err_no", 0) != 0:
        raise RuntimeError(f"百度语音识别错误: {data.get('err_msg')} (code={data.get('err_no')})")
    result = data.get("result") or []
    if not result or not result[0]:
        raise RuntimeError("未能识别到语音")
    return result[0].strip()


@app.get("/health", response_model=HealthResponse)
def health() -> HealthResponse:
    return HealthResponse(
        module_id=MODULE_ID,
        version=VERSION,
        status="ok",
        capabilities=CAPABILITIES,
    )


@app.post("/execute")
def execute(req: ExecuteRequest) -> dict[str, Any]:
    action = (req.action or "").strip()
    params = req.params or {}
    try:
        if action != "transcribe":
            raise HTTPException(status_code=400, detail=f"不支持的 action: {action}")
        audio_b64 = params.get("audio")
        if not audio_b64:
            raise HTTPException(status_code=400, detail="transcribe 需要参数 audio (base64)")
        app_id, api_key, secret_key = _read_credentials(params)
        if not (app_id and api_key and secret_key):
            raise HTTPException(
                status_code=400,
                detail="百度 ASR 凭证未配置（baidu_app_id/baidu_api_key/baidu_secret_key）",
            )
        text = transcribe(str(audio_b64), app_id, api_key, secret_key)
        return {"text": text, "provider": "baidu"}
    except HTTPException:
        raise
    except Exception as e:
        logger.exception("execute 失败")
        raise HTTPException(status_code=500, detail=str(e))


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("PORT", "8092")))
