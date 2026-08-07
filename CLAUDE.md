# CLAUDE.md

> 高频信息（每次会话都需要）。架构读 `docs/architecture.md`，工具层读 `docs/tool-layer.md`，完整工作流规范读 `AGENTS.md`。

## claw 是什么

面向本地环境的 AI 工作站。agent 编排/工具/数据在本地运行（Windows / Linux 优先），连接 eleball 云端的账户 / LLM 托管 / 秘技集市。**本地工作站，非云端同构子系统**。

## 构建命令

```bash
make build                                   # claw-server 单文件二进制（CGO_ENABLED=0，跨架构）
cd gateway && go run ./cmd/claw-server --port=8090
cd gateway && go test ./internal/...         # 后端测试
cd gateway/web && npm run dev                # 用户端 H5
cd gateway/admin-web && npm run dev          # 本地控制台
npm run build                                # 构建验证
```

便携 Go 在 `.tools/go`（`dev-run.sh` / `dev-run.ps1` 自动使用）。C 盘空间不足时把 `TMP` / `GOMODCACHE` / `GOCACHE` 指向项目盘。

## 异构边界（最易踩坑）

- **共享**（与云端同步）：`pkg/`、API 契约（主仓 `specs/api-schema.yml`）、web、agent loop 骨架。
- **分叉**（claw 自有，不同步）：`tool_platform.go` / `tool_shell_builtin.go` / `agent_tools.go` / `permission_service.go`。

改 claw 工具层时**不要**同步回云端；改 `pkg/` / web / 契约时按 AR-refactor 规则同步。

## 常见坑

| 现象 | 解法 |
|------|------|
| `java` 找不到 | claw 纯 Go，不依赖 JDK；若误装 android 相关才需，见主仓 `debugs/depends` |
| Go 编译慢 / C 盘满 | 用 `.tools/go`；`TMP`/`GOMODCACHE`/`GOCACHE` 指向项目盘 |
| 模块不在线 | 确认 docker 可用；拉取失败自动回退本地构建；见 `docs/troubleshooting.md` |
| Windows 无 Docker Desktop | 用 WSL 桥接 shim，见 `docs/troubleshooting.md` |
| LLM 请求失败 | 确认 `claw.yaml` `server.eleagent_base_url` 与登录态；BYOK 检查 key |
| 改了 claw tool 层却去同步云端 | 异构边界--tool 层不同步；仅 `pkg/`/web/契约同步 |

## 安全红线

- API Key 用 AES-256-GCM 加密入库，请求期间仅驻内存；DB 无明文，日志禁打印 Key。
- 主密钥经 `ENCRYPTION_MASTER_KEY` 注入；存在加密 Key 但缺主密钥时**拒绝启动**。
- 第三方模块镜像需 cosign 签名校验通过方可激活。

## 当前阶段

P1-P6 已完成。进行中：平台重构（platform_refactor_20260804）--PR-D 文档独立化、PR-E 本地专有工具层。详见 eleball 主仓 `.claude/plans/`。
