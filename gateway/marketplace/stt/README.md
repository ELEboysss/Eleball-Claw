# stt 模块（claw 内置语音识别 / 百度 ASR）

claw 端 STT 下沉为集市模块（仿 `search-web`）。百度短语音识别 REST API 的
`app_id` / `api_key` / `secret_key` 作为模块**凭证**配置，由用户传入。

> 云端（cmd/server）的 `/stt` 内置服务保持不变（固定服务，不要求用户配 key）；
> claw 端改为本模块。逻辑移植自 `gateway/internal/service/stt_service.go`。

## 凭证配置（二选一，web 字段优先）
1. **web 字段**：在 claw 技能页配置本模块凭证 `baidu_app_id` / `baidu_api_key` /
   `baidu_secret_key`，`moduleDriver` 执行时注入 `/execute` 的 `params.credentials`。
2. **环境变量**：`BAIDU_APP_ID` / `BAIDU_API_KEY` / `BAIDU_SECRET_KEY`（docker-compose 直配）。

## 标准接口
- `GET /health` -> `{ module_id, version, status, capabilities }`
- `POST /execute` -> `action=transcribe`，`params={ audio: <base64>, language? }`
  -> `{ text, provider: "baidu" }`

支持音频格式：m4a / wav / amr / pcm（按文件魔数判定），单声道，<= 60s，<= 10MB。

## 运行
```bash
# 本地
pip install -r requirements.txt
PORT=8092 uvicorn main:app --host 0.0.0.0 --port 8092

# 容器
docker compose up -d
```

claw 启动时扫描 `marketplace/stt/module.json` 自动注册为本地模块（官方预置，免拉镜像）。
