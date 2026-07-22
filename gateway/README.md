# Eleball-claw Gateway

> claw 本地网关目录。`cmd/claw-server` 为本地网关入口，编译为单文件二进制运行在用户设备上。

## 目录概览

```
gateway/
├── cmd/
│   ├── claw-server/     # claw 本地网关入口（运行）
│   ├── server/          # 云端入口（claw 不运行）
│   ├── e2e-server/      # E2E 入口（claw 不运行）
│   └── seed/
├── internal/            # handler / service / repository / model / middleware / router
├── pkg/                 # crypto / llm / util
├── configs/
│   └── claw.yaml        # claw 本地配置
├── web/                 # 用户端 H5（P2 本地化）
├── admin-web/           # 管理后台（P3 本地化）
├── marketplace/         # 预置官方模块
├── go.mod / go.sum
└── Makefile
```

## 编译运行

```bash
# 纯 Go 编译（无 CGO，跨架构，推荐）
CGO_ENABLED=0 go build -o build/eleball-claw ./cmd/claw-server

# 运行（默认端口 8090）
go run ./cmd/claw-server serve --port=8090
```

或使用上层目录的根 `Makefile` / `dev-run.sh` / `dev-run.ps1` 一键构建并启动。

## 配置

本地配置文件 `configs/claw.yaml`：本地 SQLite、不启用管理后台 / 支付 / 邮件；`server.eleagent_base_url` 指向云端 `api.eleball.cn/v1`（Ele Agent 模型转发至云端）。详见文件内注释。

可用 `CONFIG_PATH` 环境变量指定自定义配置路径。

## 健康检查

```bash
curl http://localhost:8090/health
# {"code":0,"message":"ok","data":{"node":"eleball-claw"}}
```

## 测试

```bash
go test ./internal/...
```

## 关联

- 本仓库根 README：`../README.md`
- claw 实现规划：主仓库 `docs/marketing/claw-implementation-plan.md`
