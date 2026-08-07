# Eleball-claw 文档

> claw 是面向**本地环境**的 AI 工作站--在用户设备本地运行 agent 编排与工具，数据自主可控，同时连接 eleball 云端的账户、LLM 托管与秘技集市。
> 本文档系统自洽：GitHub 用户与 agent 通过本目录即可了解 claw 全貌，无需查阅 eleball 主仓库。

## claw 是什么

- **本地工作站**：agent 编排/工具/数据在本地运行（Windows / Linux 优先），不是云端服务的薄客户端。
- **与云端互补而非同构**：claw 专注本地环境直操性能（编程、多模态），云端 eleball 负责账户/LLM 托管/秘技集市/计费。运行时允许异构。
- **web + gateway + admin-web** 三部分：agent 编排在本地 gateway，交互靠 H5/Web 渲染。

## 文档索引

| 文档 | 内容 |
|------|------|
| [getting-started.md](getting-started.md) | 安装、运行、首次验证 |
| [architecture.md](architecture.md) | 架构定位、异构边界、gateway 分层、数据流 |
| [tool-layer.md](tool-layer.md) | 本地工具层：现状与目标（真 shell / git / 构建工具链 / 权限模型） |
| [cloud-integration.md](cloud-integration.md) | 与云端 eleball 的对接：账户 / LLM / 秘技集市 |
| [security.md](security.md) | 本地安全模型：数据 / 凭证 / 模块签名 / shell 权限 |
| [development.md](development.md) | 开发：构建 / 测试 / 工具链 / 模块开发 |
| [troubleshooting.md](troubleshooting.md) | 常见问题：Windows 无 Docker Desktop 的 WSL 桥接等 |

## 快速入口

- 想跑起来：[getting-started.md](getting-started.md)
- 想懂架构：[architecture.md](architecture.md)
- 想做开发：[development.md](development.md)
- 遇到问题：[troubleshooting.md](troubleshooting.md)
- agent 工作流入口：仓库根 `AGENTS.md`
- 高频信息（构建命令 / 踩坑）：仓库根 `CLAUDE.md`
