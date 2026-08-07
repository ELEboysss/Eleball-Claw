# 与云端 eleball 的对接

claw 在本地运行 agent 与数据，同时连接云端 eleball 获取账户、LLM 托管与秘技集市服务。关系是互补：**本地能自主完成的不过云端，云端强托管的由云端负责**。

## 1. 账户与鉴权

- **登录态**：用户用云端 eleball 账户登录 claw。本地 JWT 验签，失败自动 fallback 云端 `/auth/me`（`middleware.JWTAuthCloudFallback`），故本地 JWT 密钥无需与云端一致。
- **配置**：`claw.yaml` `jwt.secret`（安装脚本生成随机值；`change-me-in-claw-production` 仅开发占位）。
- **本地不计费**：claw 不维护计费/充值/支付逻辑（云端职责）。

## 2. LLM

- **Ele Agent 模式**：模型经 `server.eleagent_base_url`（默认 `https://api.eleball.cn/v1`）转发至云端，由云端账户计费。claw 不持有云端 LLM key。
- **BYOK 模式**：用户可配自带 key 直连上游端点（OpenAI/Anthropic/Gemini/Bailian 协议，由 `pkg/llm` 统一）。
- **配置**：`claw.yaml` `llm.timeout` / `llm.max_retries`。

## 3. 秘技集市

- **已购秘技拉取**：技能页登录态调云端 `GET /v1/market/modules/installed` 拉取已购未安装秘技，与本地秘技合并展示。
- **下载/安装**：云端来源秘技走本地 `POST /v1/claw-console/modules/install`；需云端 VIP1+（后端 4002 兜底，前端前置提示）。本地内置秘技（如 SearchWeb）不经过此接口，免门控。
- **安装闭环**：安装成功后本地 upsert 驱动映射 + `AgentItem`（approved）+ 购买记录（`EnsureCloudAgentProvision`），云端购买的秘技可直接在技能页激活。
- **激活**：激活 toggle 后，下次 `/v1/agent/execute` 由 `AgentToolLoader` 自动载入该秘技工具，无需重启。

## 4. web 双通道

claw web 与云端 web 同构（共享设计语言与组件），通过 baseURL 双通道区分：

- 整页（首页、文档、充值等）走云端 `eleball.cn`；
- 本地能力（agent 执行、模块、本地控制台）走本地 `http://localhost:<port>`。

## 5. 本地预置 SKU 与助手

- **预置 SearchWeb SKU**：启动时种子 `search-web-baidu`（百度千帆）/ `search-web-bing`（必应）两个免费官方秘技；本地免费获取走本地 `POST /agents/:id/purchase`。
- **助手（Assistant）**：`/assistants` 管理页 CRUD「已激活秘技的命名组合」；对话页按会话绑定助手；Agent 模式仅加载助手内秘技工具。

## 6. 什么不过云端

- 对话与会话数据（本地 SQLite）
- agent 编排与工具执行
- 本地模块运行（docker）
- 本地凭证（API key 加密存本地）

> 契约（API 形状）在 eleball 主仓 `specs/api-schema.yml`，是 claw 与云端的共同事实源。
