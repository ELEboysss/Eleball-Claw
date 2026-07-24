# Eleball-claw

> 设备端本地化网关组件 ｜ 状态：✅ P1-P6 完成（本地网关 + 双轨通道 + 秘技安装 + 一键安装脚本，详见主仓库 claw-implementation-plan.md / claw-e2e-checklist.md）
> 规划：主仓库 `docs/marketing/claw-implementation-plan.md` ｜ 产品定位：主仓库 `docs/marketing/product-matrix.md` §5
> 本仓库是主项目的 submodule。

Eleball-claw 把 AI agent 能力运行到本地设备（Windows / Linux 优先），数据与编排自主可控，同时与云端 eleball 账户 / 秘技 / 文档 / 充值互通。由 **web + gateway + admin-web** 三部分组成，agent 编排与运行在本地，交互靠 H5/Web 渲染。

## 功能定位

- **本地网关**：在设备本地运行对话、视觉、Agent 工作流、本地模块集市等能力，数据落本地 SQLite。
- **与云端互通**：Ele Agent 模型转发至云端 `api.eleball.cn/v1`；首页、文档、充值等整页走云端 `eleball.cn`。
- **本地控制台**：用户账户登录的本地控制台（`/v1/claw-console/*`），区别于云端管理后台。
- **模块化秘技**：官方模块随仓库预置（免容器），第三方模块拉镜像 + 签名校验后激活。

## 当前实现状态

### ✅ P1 脚手架（本仓库初始版本）
- **本地网关入口**：`gateway/cmd/claw-server`，编译为单文件二进制（无 CGO，跨架构）。
- **本地控制台**：`/v1/claw-console/*` 提供本地控制台接口。
- **本地配置**：`gateway/configs/claw.yaml`（本地 SQLite，不启用管理后台 / 支付 / 邮件）。
- **分发脚手架**：根 `Makefile` / `install.sh` / `install.ps1`。
- **预置模块范例**：`gateway/marketplace/search-web/`（P4 接入扫描）。
- 编译验证：`go build ./cmd/claw-server` 通过；冒烟测试 `/health` 返回 claw 节点、无 panic 启动。

### ✅ 已落地（P1-P6，见主仓库 claw-implementation-plan.md §I / claw-e2e-checklist.md）
| 阶段 | 状态 |
|------|------|
| P1 | ✅ 脚手架：claw-server 编译启动，`/v1/admin/*` 与支付路由 404（裁剪生效） |
| P2 | ✅ web 本地化：baseURL 双通道、技能页登录态拉云端、claw-guide、官网内嵌 + 登录态双向同步 |
| P3 | ✅ admin-web 本地化：本地控制台、Modules 安装/提交审核、模型配置只读 |
| P4 | ✅ SearchWeb 本地秘技 + 第三方拉镜像安装（cosign 签名校验）+ 提交审核转发云端 |
| P5 | ✅ 双轨通道：LAN mDNS + E2E 加密中继 + auto 选择（P2P 打洞评估后暂不做） |
| P6 | ✅ install 脚本（对接云端网关 release 端点）+ 文档 + E2E 验证 |

## 目录结构

```
eleball-claw/
├── gateway/                      # 本地网关（独立 go module: github.com/eleball/gateway）
│   ├── cmd/
│   │   ├── claw-server/          # claw 本地网关入口
│   │   ├── server/               # 云端入口（claw 不运行）
│   │   ├── e2e-server/           # E2E 入口（claw 不运行）
│   │   └── seed/
│   ├── internal/                 # 分层（handler/service/repository/model/middleware/router）
│   │   └── router/claw_router.go # claw 路由 + /v1/claw-console
│   ├── pkg/                      # crypto / llm / util
│   ├── configs/claw.yaml         # claw 本地配置
│   ├── web/                      # 用户端 React（P2 本地化）
│   ├── admin-web/                # 管理后台 React（P3 本地化）
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

### 作为主项目 submodule 开发

主项目已通过 `.gitmodules` 引入本仓库。在主仓库根：

```bash
git submodule update --init eleball-claw   # 首次拉取
cd eleball-claw/gateway && ../../.tools/go/bin/go build ./cmd/claw-server
```

### 一键安装

安装脚本从云端网关 release 端点按架构下载二进制（含 SHA256 校验），生成配置并提示启动命令。下载页与说明见 <https://eleball.cn/claw>。

```bash
# Linux / macOS
curl -fsSL https://eleball.cn/install.sh | sh

# Windows（PowerShell）
irm https://eleball.cn/install.ps1 | iex
```

### 模块开发与启动（marketplace）

安装版 claw 的模块目录（marketplace home）默认在 `~/.eleball-claw/marketplace`
（Windows 为 `%USERPROFILE%\.eleball-claw\marketplace`，`CLAW_MARKETPLACE_DIR` 环境变量可覆盖）。
首次启动或执行 `module` 命令时，官方模块（stt / search-web / firecrawl / agent-reach）会自动播种到该目录——
**这里就是开发和管理模块的地方**：自定义模块放入一个含 `module.json` 的子目录即可被网关扫描登记。

模块以 docker 容器运行，通过二进制内置命令一键管理（无需 clone 仓库、无需 bash，Windows 可用）：

```bash
eleball-claw module ls              # 列出模块与状态
eleball-claw module up              # 构建并启动全部模块（首次构建需几分钟）
eleball-claw module up stt          # 只启动指定模块
eleball-claw module ps / down       # 查看状态 / 停止
eleball-claw module logs stt -f     # 跟踪日志
```

模块端口固定发布到宿主机（stt 8092 / search-web 8091 / firecrawl 8093 / agent-reach 8094），
claw（宿主机进程）经 `http://localhost:<端口>` 调用；启动后约 1 分钟内控制台「本地模块」自动转在线。

## 与云端 eleball 的关系

claw 定位为设备端本地化组件，与云端 `eleball.cn` 互补：

| 维度 | claw（本地） | 云端 eleball |
|------|------|------|
| 运行位置 | 用户设备本地 | 云端 |
| 计费 | 本地不计费 | 由云端账户计费 |
| 控制台 | `/v1/claw-console/*`，用户账户登录 | 云端管理后台 |
| 支付 / 充值 | 走云端 eleball.cn | 云端 |
| 数据 | 本地 SQLite，数据不出设备 | 云端 |

## 安全

- 本地数据 SQLite，数据不出设备。
- 第三方模块镜像需签名校验（cosign / sigstore）通过方可激活。
- API Key 加密存储，请求期间仅驻内存。

## 关联文档（主仓库）

- 实现规划：`docs/marketing/claw-implementation-plan.md`
- 产品矩阵：`docs/marketing/product-matrix.md` §5
- 模块化架构（含 claw §8）：`docs/agent-market-modular-architecture.md`
- 工具驱动（含 claw §15）：`docs/tool-driver-guide.md`
- API 契约：`specs/api-schema.yml`（`ModuleInstallMeta` / `/market/modules/installed`）
