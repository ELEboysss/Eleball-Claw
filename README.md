# Eleball-claw

> 面向本地环境的 AI 工作站 ｜ 独立开源仓库（eleball 主仓 submodule）
> 完整文档：[`docs/`](docs/) ｜ 快速开始：[docs/getting-started.md](docs/getting-started.md) ｜ 架构：[docs/architecture.md](docs/architecture.md)

Eleball-claw 把 AI agent 能力运行到本地设备（Windows / Linux 优先），agent 编排与工具在本地运行、数据落本地 SQLite 自主可控，同时连接 eleball 云端的账户、LLM 托管与秘技集市。

**定位**：本地工作站，非云端同构子系统。claw 专注本地环境直操性能（编程、多模态），运行时与云端允许异构。由 **web + gateway + admin-web** 三部分组成。

## 快速开始

```bash
# 一键安装（下载二进制 + SHA256 校验）
curl -fsSL https://eleball.cn/install.sh | sh          # Linux / macOS
irm https://eleball.cn/install.ps1 | iex               # Windows (PowerShell)

# 或从源码编译（需 Go 1.25+，便携 Go 在 .tools/go）
make build
./claw-server --port=8090
```

启动后访问 `http://localhost:8090/health`，应返回 `{"code":0,"message":"ok","data":{"node":"eleball-claw"}}`。

## 文档

| 想做什么 | 去哪 |
|---------|------|
| 跑起来 | [docs/getting-started.md](docs/getting-started.md) |
| 懂架构 / 异构边界 | [docs/architecture.md](docs/architecture.md) |
| 工具能力与限制 | [docs/tool-layer.md](docs/tool-layer.md) |
| 与云端对接 | [docs/cloud-integration.md](docs/cloud-integration.md) |
| 安全模型 | [docs/security.md](docs/security.md) |
| 开发 / 测试 | [docs/development.md](docs/development.md) |
| 常见问题 | [docs/troubleshooting.md](docs/troubleshooting.md) |

agent 工作流入口：[`AGENTS.md`](AGENTS.md) ｜ 高频信息：[`CLAUDE.md`](CLAUDE.md)

## 核心特性

- **本地网关**：本地运行对话、视觉、Agent 工作流、模块集市，数据落本地 SQLite。
- **与云端互通**：Ele Agent 模型转发至云端 `api.eleball.cn/v1`；账户 / 秘技 / 充值走云端。
- **本地控制台**：`/v1/claw-console/*` 用户账户登录的本地控制台。
- **模块化秘技**：官方模块随仓库预置（免容器），第三方模块拉镜像 + cosign 签名校验后激活。
- **双轨通道**：LAN mDNS + E2E 加密中继 + auto 选择。

## 与云端 eleball 的关系

| 维度 | claw（本地） | 云端 eleball |
|------|------|------|
| 运行位置 | 用户设备本地 | 云端 |
| 编排与工具 | 本地 runtime + 本地工具层 | 云端 runtime + 安全沙箱 |
| 数据 | 本地 SQLite，不出设备 | 云端 DB |
| 计费 | 本地不计费 | 云端账户计费 |

详见 [docs/architecture.md](docs/architecture.md) 与 [docs/cloud-integration.md](docs/cloud-integration.md)。

## 安全

- 本地数据 SQLite，数据不出设备。
- API Key 加密存储（AES-256-GCM），请求期间仅驻内存。
- 第三方模块镜像需签名校验（cosign / sigstore）。

详见 [docs/security.md](docs/security.md)。
