# Eleball-claw

> 设备端本地化的 Eleball 组件 ｜ 状态：🚧 P1 脚手架已落地（gateway fork + claw-server 可编译可启动），P2-P6 推进中
> 规划：主仓库 `docs/marketing/claw-implementation-plan.md` ｜ 产品定位：主仓库 `docs/marketing/product-matrix.md` §5
> 本仓库是主仓库 [Eleball](https://gitcode.com/Eleboy/Eleball) 的 submodule，gateway/ 为上游 gateway 的裁剪 fork。

Eleball-claw 把云端 Eleball 的 agent 能力搬到本地设备（Windows / Linux 优先），数据与编排自主可控，同时与云端 eleball 账户 / 秘技 / 文档 / 充值互通。由 **web + gateway + admin-web** 三部分组成，agent 编排与运行在本地，交互靠 H5/Web 渲染。

## 当前实现状态

### ✅ P1 脚手架（本仓库初始版本）
- **gateway fork**：`gateway/` 为上游 Eleball gateway 的完整 fork（internal/pkg/cmd/configs/web/admin-web/marketplace），保持与上游文件结构一致以便 cherry-pick 同步。
- **claw-server**：`gateway/cmd/claw-server/main.go`，复用 fork 的 `internal/` 分层，注入 nil billing（**本地不计费**，Ele Agent 模型经 BaseURL 转发云端 `api.eleball.cn/v1` 计费），链接期裁剪未引用包（payment/cdk/admin_gate 等自动剔除）。
- **claw_router**：`gateway/internal/router/claw_router.go`，裁剪路由--删除 `/v1/admin/*`（改 `/v1/claw-console/*` 本地控制台）、支付/CDK/VIP 套餐/提现/admin_gate；保留对话/视觉/Agent 工作流/集市模块/对话历史/同步/模型列表/STT/凭证。
- **claw 配置**：`gateway/configs/claw.yaml`（admin/admin_gate/payment/mail 全 disabled，本地 SQLite）。
- **分发脚手架**：根 `Makefile` / `install.sh` / `install.ps1`。
- **预置模块范例**：`gateway/marketplace/search-web/`（P4 接入扫描）。
- 编译验证：`go build ./cmd/claw-server` 通过；冒烟测试 `/health` 返回 claw 节点、`/v1/admin/*` 与支付路由 404（裁剪生效）、无 panic 启动。

### ⏳ 待实现（P2-P6，见规划 §I）
| 阶段 | 目标 |
|------|------|
| P2 | web 本地化：baseURL 双通道、技能页登录态拉云端、claw-guide 页 |
| P3 | admin-web 本地化：本地控制台、Modules 管理、模型配置改造 |
| P4 | SearchWeb 抽出为本地秘技 + 第三方拉镜像安装 + 提交审核流程 |
| P5 | APP 端双轨通道（云端 / 本地 claw） |
| P6 | install 脚本发布 + 文档 + 端到端验证 |

## 架构决策（已批准）

1. **gateway 沿用 Go 架构裁剪**（非重写）：复用上游 gateway 的 agent 编排 / SSE / 工具调用 / 模块驱动，裁剪云端专属能力。本仓库保留 fork 全部 internal（不物理删除 billing/vip/cdk 等文件），靠 main/router 层裁剪装配 + 链接器裁剪未引用包，便于与上游 cherry-pick 同步。
2. **秘技安装：官方预置 + 第三方拉镜像**：官方模块随仓库预置免容器，第三方模块拉容器镜像 + 签名校验。
3. **submodule 组织**：本仓库为上游 gateway 的完整 fork，独立可编译（`cd gateway && go build ./cmd/claw-server`），主仓库以 submodule 引入。与上游同步策略见规划附录 1（短期 fork cherry-pick -> 中期抽共享 core）。

## 目录结构

```
eleball-claw/
├── gateway/                      # 上游 gateway 的裁剪 fork（独立 go module: github.com/eleball/gateway）
│   ├── cmd/
│   │   ├── claw-server/          # claw 本地网关入口（claw 专属）
│   │   ├── server/               # 云端入口（fork 保留，claw 不运行）
│   │   ├── e2e-server/           # E2E 入口（fork 保留）
│   │   └── seed/
│   ├── internal/                 # 分层（handler/service/repository/model/middleware/router）
│   │   └── router/claw_router.go # claw 裁剪路由 + /v1/claw-console（claw 专属）
│   ├── pkg/                      # crypto / llm / util
│   ├── configs/claw.yaml         # claw 本地配置（claw 专属）
│   ├── web/                      # 用户端 React（P2 本地化）
│   ├── admin-web/                # 管理后台 React（P3 本地化裁剪）
│   ├── marketplace/              # 预置官方模块（含 search-web 范例）
│   ├── go.mod / go.sum
│   └── Makefile / README.md
├── Makefile                      # 根分发 Makefile（build/package/run）
├── install.sh                    # Linux 一键安装
├── install.ps1                   # Windows 一键安装
└── README.md                     # 本文件
```

## 快速开始

### 编译运行

```bash
# 编译 claw-server 单文件二进制（无 CGO，跨架构）
make build

# 或直接 go run（需本机 Go 1.25+）
cd gateway && go run ./cmd/claw-server --port=8090
```

启动后访问 `http://localhost:8090/health`，应返回 `{"code":0,"message":"ok","data":{"node":"eleball-claw"}}`。

### 作为主仓库 submodule 开发

主仓库已通过 `.gitmodules` 引入本仓库。在主仓库根：

```bash
git submodule update --init eleball-claw   # 首次拉取
cd eleball-claw/gateway && ../../.tools/go/bin/go build ./cmd/claw-server
```

### 一键安装（用户侧，待 P6 发布二进制后可用）

```bash
# Linux / macOS
curl -fsSL https://eleball.cn/install.sh | sh

# Windows
irm https://eleball.cn/install.ps1 | iex
```

## 与云端 eleball 的差异

| 维度 | 云端 eleball | claw |
|------|--------------|------|
| 计费 | 弹丸 / 优雅弹丸扣费 | 本地不计费（Ele Agent 转发云端计费） |
| 管理面 | `/v1/admin/*` + admin gate | `/v1/claw-console/*`，用户账户登录 |
| 支付/CDK/VIP/订单/提现 | 有 | 无（走云端 eleball.cn） |
| 对话/视觉/模型/技能 | 云端 gateway | 本地 claw gateway |
| 首页/文档/充值 | 云端 | 走云端 eleball.cn（web 整页跳转） |
| 秘技来源 | DB 注册 | 本地扫描 + 云端拉取 + 本地开发 |

## 安全

- 本地数据 SQLite，数据不出设备。
- 第三方模块镜像需签名校验（cosign / sigstore）通过方可激活。
- API Key 用 AES-256-GCM 加密入库，请求期间仅驻内存（复用上游 `KeyManagerService`）。

## 与上游 gateway 同步

本仓库 gateway/ 是上游 fork。上游 gateway 演进时：
1. 短期：手动 cherry-pick 上游 commit 到本仓库 gateway/。
2. 中期：抽取 agent 编排 / SSE / 工具调用 / module driver 为共享 Go module，上游与本仓库共用，减少分叉漂移（规划附录 1）。

## 关联文档（主仓库）

- 实现规划：`docs/marketing/claw-implementation-plan.md`
- 产品矩阵：`docs/marketing/product-matrix.md` §5
- 模块化架构（含 claw §8）：`docs/agent-market-modular-architecture.md`
- 工具驱动（含 claw §15）：`docs/tool-driver-guide.md`
- API 契约：`specs/api-schema.yml`（`ModuleInstallMeta` / `/market/modules/installed`）
