# 架构

## 1. 定位：本地工作站，非云端同构子系统

claw 是面向本地环境的 AI 工作站。与云端 eleball 的关系是**互补**而非同构：

| 维度 | claw（本地） | 云端 eleball |
|------|------|------|
| 运行位置 | 用户设备本地 | 云端 |
| 编排与工具 | 本地 agent runtime + 本地工具层 | 云端 agent runtime + 云端安全沙箱 |
| 数据 | 本地 SQLite，不出设备 | 云端 DB |
| 计费 | 本地不计费 | 云端账户计费 |
| LLM | Ele Agent 模型转发至云端；BYOK 可直连上游 | 托管 LLM 池 |
| 安全模型 | 本地可信场景：权限确认（目标） | 多租户不可信：白名单沙箱 |

> **架构方向**：claw 从「云端同构子系统」转为「独立本地工作站」--**UI 同构、运行时异构**。claw 专注本地直操性能（编程/多模态），不再强求与云端代码同构同步。

## 2. 三部分组成

```
eleball-claw/
├── gateway/          # 本地网关（Go）：agent 编排 + 工具层 + 本地 API
│   ├── cmd/claw-server/   # claw 入口二进制
│   ├── internal/          # handler / service / repository / model / middleware / router / seed / config
│   ├── pkg/               # crypto / llm / util（与云端共享）
│   ├── configs/claw.yaml  # 本地配置
│   ├── web/               # 用户端 React（与云端同构）
│   ├── admin-web/         # 本地控制台 React（秘技包管理 + 只读展示）
│   └── marketplace/       # 预置官方模块（module.json + main.py + skus/）
├── install.sh / install.ps1   # 一键安装
└── Makefile                   # 分发构建
```

- **gateway**：Go 编写的本地网关。agent loop、工具调度、本地控制台 API（`/v1/claw-console/*`）、模块管理在此。
- **web**：用户端 React H5。与云端 web 同构（共享设计语言与组件），仅 baseURL/特性开关不同--云端走远程、claw 走 localhost。
- **admin-web**：本地控制台。区别于云端管理后台，以只读展示为主（模型列表、模块状态）。

## 3. 异构边界（关键）

claw 与云端**共享**以下部分（保持同步）：

- `pkg/`：crypto / llm / util 基础库
- API 契约：`specs/api-schema.yml`（在 eleball 主仓）
- web 工作台：UI 同构
- agent loop 骨架

claw **分叉**（自有，不再同步回云端）：

- `tool_platform.go`：本地 shell 安全模型（去白名单，改权限确认）
- `tool_shell_builtin.go`：本地 shell/命令实现
- `agent_tools.go`：本地工具集（含 git / 构建工具链 / Glob 等）
- `permission_service.go`：本地权限确认模型

> 详见 [tool-layer.md](tool-layer.md) 与 [security.md](security.md)。

分叉的理由：云端 shell 的白名单 + 元字符禁令是为**多租户不可信**场景防注入；对**本地单用户可信**场景是过度约束--它阻止 claw 跑管道/重定向/git/构建工具，使 claw 无法胜任编程。两套安全模型各自演进。

## 4. gateway 分层

```
cmd/claw-server/        # 入口，装配各 service
internal/
  config/               # 配置加载（claw.yaml）
  router/claw_router.go # claw 路由 + /v1/claw-console/*
  handler/              # HTTP handler
  service/              # 业务核心：agent_tool_loop / tool_platform / permission / context_* / module / ...
  repository/           # 数据访问（GORM）
  model/                # 数据模型
  middleware/           # JWT auth（本地验签 + 云端 fallback）
  seed/                 # 本地预置 SKU / 助手
pkg/
  crypto/               # AES-256-GCM 凭证加密
  llm/                  # LLM 客户端（OpenAI/Anthropic/Gemini/Bailian 协议）
  util/
```

## 5. 数据流

- **本地数据**：对话、会话、模块安装记录、助手配置落本地 SQLite（`data/claw.db`）。
- **LLM 调用**：Ele Agent 模型经 `server.eleagent_base_url`（默认 `https://api.eleball.cn/v1`）转发至云端，由云端账户计费；BYOK 模式可直连用户自带的上游端点。
- **账户鉴权**：本地 JWT 验签，失败自动 fallback 云端 `/auth/me`（`middleware.JWTAuthCloudFallback`）。
- **秘技集市**：技能页登录态拉云端已购秘技，下载/安装走本地 `/v1/claw-console/modules/install`，激活后由 `AgentToolLoader` 载入。

## 5.1 秘技包模型（卡片 = 秘技包，强关联模块生命周期）

集市卡片与插件系统模块**强关联**，一个秘技包 = 一个卡片，包内多项能力（SKU）可被作为工具调用：

- **包身份物化**：`SkillRuntime` 增 `Category/PackageName/PackageTitle/PackageDescription`。module.json 扫描（`ensureMarketplaceModules`）、控制台注册/更新（`moduleRecordToRuntime`）、MCP 安装（`InstallMCPRuntime`）都会写入包身份；`PackageName` 即包 slug（多运行时包共享）。
- **SKU 打标**：`DeriveSKUs` 派生 SKU 时写 `metadata.package_module/package_title/package_description/package_version`（`buildDerivedManifest`）；派生签名并入包身份字段，模块更新（名称/描述/版本/分类）即触发重派生，**SKU 卡片同步刷新**。
- **级联下架**：`UnregisterModule` 注销模块时 `delistPackageSKUs` 按 `auto_sku_module/module/package_module` 精确归属下架本包全部 SKU（delisted 保留购买记录，集市列表即消失）。
- **真·卸载**：`DELETE /v1/claw-console/modules/:id/uninstall`（`UninstallModule`）：官方守卫 → 停进程/容器 → 下架本包 SKU → 删 marketplace/<slug>/ 目录（user/mcp 起源）→ 注销本包全部运行时。
- **前端聚合**：技能页 `AgentMarket` 按 `package_module` 聚合包卡 + 包详情逐 SKU 勾选 + 包级一键激活；助手组装（`AssistantManager`）按包分组；控制台 `admin-web`「本地秘技包」页按包归组展示运行时子表 + SKU 清单 + 显著「卸载」按钮。

**存量覆盖**：手写 SKU manifest（`marketplace/<mod>/skus/*.json`）与 module.json 均携带 `package_module/category` 等包级字段；`RescanMarketplace` 额外执行 `backfillPackageIdentity`（幂等）——老库运行时无包身份、SKU manifest 缺 package_module 的存量行在启动扫描/「重新扫描」时自动补齐，确保既有模块（agent-reach/firecrawl/search-web/mcp-hello/mcp-stdio-echo/stt + prompt-only skill）全部进入秘技包体系。

**孤儿 SKU 对账**：旧版本注销模块只删运行时、不级联下架 SKU，历史遗留的 approved SKU 卡片仍展示。`RescanMarketplace` 每次执行 `reconcileOrphanedSKUs`（幂等）：引用已不存在运行时（按 auto_sku_module/module/package_module 归属，driver≠none 的可执行 SKU）一律下架（delisted 保留购买记录）；prompt-only 秘技（driver=none）与无归属元数据的 SKU 保守保留。

## 6. 目标演进

当前 claw 的工具层仍带云端安全沙箱的约束（白名单 shell）。目标是构建本地专有工具层（见 [tool-layer.md](tool-layer.md)）：真 bash、git、构建工具链、流式/后台进程、权限确认模型--让 claw 真正胜任编程与重本地操作。
