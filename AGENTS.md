# AGENTS.md

> 本文件是 agent 在 eleball-claw 仓内工作的入口。claw 是独立开源仓库（eleball 主仓 submodule），本文自洽，不依赖主仓文档。
> 单一事实源：架构见 `docs/architecture.md`；工具层见 `docs/tool-layer.md`；安全模型见 `docs/security.md`。

## 项目概述

Eleball-claw 是面向**本地环境**的 AI 工作站。在用户设备本地（Windows / Linux 优先）运行 agent 编排与工具，数据落本地 SQLite 自主可控，同时连接 eleball 云端的账户、LLM 托管与秘技集市。

定位：**本地工作站，非云端同构子系统**。运行时与云端允许异构--claw 专注本地直操性能（编程、多模态），不强求与云端代码同构。

## 工程目录

```
eleball-claw/
├── gateway/                      # 本地网关（独立 go module）
│   ├── cmd/claw-server/          # claw 入口二进制
│   ├── internal/                 # handler / service / repository / model / middleware / router / seed / config
│   │   └── router/claw_router.go # claw 路由 + /v1/claw-console/*
│   ├── pkg/                      # crypto / llm / util（与云端共享）
│   ├── configs/                  # claw.yaml / config.yaml / hooks.json
│   ├── web/                      # 用户端 React（与云端同构）
│   ├── admin-web/                # 本地控制台 React（只读为主）
│   └── marketplace/              # 预置官方模块
├── docs/                         # 本文档系统（自洽）
├── install.sh / install.ps1      # 一键安装
├── Makefile / dev-run.sh / dev-run.ps1
└── README.md / AGENTS.md / CLAUDE.md
```

## 构建与测试

```bash
make build                                   # claw-server 单文件二进制（CGO_ENABLED=0，跨架构）
cd gateway && go test ./internal/...         # 后端测试
cd gateway/web && npm run build              # 前端构建验证
cd gateway/admin-web && npm run build
```

便携 Go 在 `.tools/go`（`dev-run.sh`/`dev-run.ps1` 自动使用）。C 盘空间不足时把 `TMP`/`GOMODCACHE`/`GOCACHE` 指向项目盘。

## 开发顺序与同步规则

### 异构边界（关键）

claw 与云端 eleball **共享**（保持同步）：`pkg/`、API 契约（主仓 `specs/api-schema.yml`）、web 工作台、agent loop 骨架。

claw **分叉**（自有，不同步回云端）：`tool_platform.go`、`tool_shell_builtin.go`、`agent_tools.go`、`permission_service.go`（本地工具层与安全模型）。

> 云端 shell 白名单安全模型对多租户正确；claw 本地可信场景改用权限确认模型。两套各自演进。详见 `docs/tool-layer.md` / `docs/security.md`。

### 双服务端

claw 仓内：`cmd/claw-server`（claw 入口）。云端双服务端（`cmd/server` + `cmd/e2e-server`）在 eleball 主仓 `gateway/`。改共享 API 契约时先改主仓 `specs/api-schema.yml`，双端同步。

### commit 规范

- claw 是独立 git 仓库：claw 改动在 claw 仓独立提交。
- eleball 主仓仅当 submodule 指针推进时提交。
- message：`feat(pr-x): ...` / `fix: ...`。

## 契约与文档

- API 契约在 eleball 主仓 `specs/api-schema.yml`（claw 仓无 specs/）。
- claw 自身文档在 `docs/`（自洽，不依赖主仓）。
- 新增/变更 claw 能力时同步更新 `docs/`。

## 当前阶段

- P1-P6 已完成（本地网关 + 双轨通道 + 秘技安装 + 一键安装脚本）。
- 进行中：**平台重构（platform_refactor_20260804）**--claw 本地专有工具层（PR-E）、文档独立化（PR-D）。详见 eleball 主仓 `.claude/plans/platform-refactor-20260804.md`。

## 安全红线

- API Key 用 AES-256-GCM 加密入库，请求期间仅驻内存；DB 无明文，日志禁打印 Key。
- 主密钥经 `ENCRYPTION_MASTER_KEY` 注入；存在加密 Key 但缺主密钥时**拒绝启动**。
- 第三方模块镜像需 cosign 签名校验通过方可激活。
