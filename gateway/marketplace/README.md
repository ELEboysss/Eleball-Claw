# 集市秘技模块目录

本目录存放独立的「集市秘技」Docker 模块。每个子目录对应一个可插拔的能力模块，满足以下标准接口。

> 关于模块分层运行时、统一模块总线、ToolDriver 抽象的完整设计，请先阅读 [`docs/agent-market-modular-architecture.md`](../../docs/agent-market-modular-architecture.md)。
> 面向普通创作者的上架手册见 [`docs/agent-market-creator-guide.md`](../../docs/agent-market-creator-guide.md)。

## 模块标准接口

每个模块容器必须暴露 HTTP 服务并提供以下端点。多个模块可以复用同一个运行时地址，通过 `X-Module-ID` 请求头或请求体中的 `module_id` 字段路由。

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 返回模块状态与能力清单 |
| POST | `/execute` | 执行工具 action |

### `/health` 响应示例

```json
{
  "module_id": "agent-reach",
  "version": "1.0.0",
  "status": "ok",
  "capabilities": ["web_read", "search", "youtube_subtitles", "..."]
}
```

### `/execute` 请求示例

```http
POST /execute
Content-Type: application/json

{
  "action": "youtube_subtitles",
  "params": {
    "query": "https://www.youtube.com/watch?v=xxx"
  },
  "user_id": "user_xxx"
}
```

- `/execute` 的 `params` 中只应包含**业务参数**（如 `url`、`query`、`limit`）。
- 网关会在调用模块前自动把当前用户为当前 SKU 填写的凭证注入 `params.credentials`，模块从 `params.credentials` 读取。
- 当模块以 L3 独立远程服务运行时，也可以直接 `POST /execute`。

### SKU 参数结构规范

为降低大模型调用出错率，新 SKU 的 `parameters` 应遵循：

1. **单 action SKU（推荐）**：直接声明业务参数，不要嵌套 `params`。
2. **多 action SKU**：保留 `action` 字段，业务参数与 `action` 平级。
3. **禁止**在 `parameters` 中再嵌套一层 `params`。

示例见 [`docs/tool-driver-guide.md`](../../docs/tool-driver-guide.md)。

### 模块元数据

模块对应的 SKU 在 `ToolManifest.driver` 中引用已注册的驱动别名，无需声明 `metadata.module`：

```json
{
  "driver": "agent_reach"
}
```

网关通过 `/health` 检测模块是否在线，并据此决定集市中对应 SKU 是否可上架。

## 当前模块

- `agent-reach/` — 互联网能力（网页阅读、搜索、视频字幕、GitHub、社交平台等）
- `firecrawl/` — 网页抓取与结构化提取（基于 Firecrawl）；API 参数与错误码参考 [`firecrawl/docs/firecrawl-api-guide.md`](firecrawl/docs/firecrawl-api-guide.md)
- `search-web/` - 本地联网搜索（百度千帆 / 必应），claw 内置范例模块

## 一键启停所有模块

本目录提供统一脚本 `start.sh`，可自动发现所有子模块的 `docker-compose.yml`：

```bash
cd gateway/marketplace
./start.sh up     # 构建并启动所有模块（默认）
./start.sh down   # 停止并移除所有模块
./start.sh logs   # 查看所有模块日志
./start.sh ps     # 查看所有模块运行状态
```

- 脚本会自动加载 `gateway/deployments/.env` 中的环境变量，与网关保持配置一致。
- 默认模式 `./deployments/start.sh` 已通过 `include` 自动拉起 `agent-reach` 与 `firecrawl`，无需再单独运行本脚本。
- 在 `./deployments/start.sh caddy|nginx` 模式下，compose 文件未包含集市模块，可单独使用本脚本启动模块。

## 新增模块流程

1. 阅读 [`docs/agent-market-modular-architecture.md`](../../docs/agent-market-modular-architecture.md) 确定模块应归属的运行时层级（L0/L1/L2/L3）。
2. 在本目录下新建 `{module-id}/` 子目录（L3 独立服务）。
3. 实现标准接口的 Dockerfile + 服务代码。
4. 提供 `docker-compose.yml`，网络统一使用 `eleball-net`。
5. 将 SKU 的 `ToolManifest` 文件放在 `{module-id}/skus/{sku-name}.json`，并在 `manifest_json` 中设置：
   - `driver`: 已注册的驱动别名（如 `agent_reach`、`firecrawl`），不要写具体 `module_id`
   - `runtime_type`: 与模块运行时层级一致，L3 独立远程服务填 `remote`
   - 如需用户预先提供 Cookie / API Key / Token，在 `credentials` 中声明字段定义
   - 推荐每个 SKU 只包含一个 `action`；单 action SKU 的 `parameters` 直接声明业务参数，不要嵌套 `params`
   - 多 action SKU 的 `parameters` 保留 `action` 字段，业务参数与 `action` 平级
6. 在创作者后台提交 SKU 审批申请，其中 `driver` 填写申请的驱动别名。
7. 驱动别名与 `auth_token` 由云端发放并绑定（`auth_token` 绑定在驱动别名上）。
8. 部署模块服务后，开发者使用 `auth_token` 调用 `POST /v1/market/modules/register` 自助注册到网关：
   - `module_id` 可留空，由网关自动生成；
   - 网关会自动将该模块绑定到对应驱动别名（回填 `drivers.module_id`）。
9. 管理员审批通过后，SKU 状态变为 `approved`；模块在线时自动上架到集市。
10. 在 `specs/tool-manifest-schema.json` 中补充新的 action/参数定义（如适用）。
11. 更新 `docs/tool-driver-guide.md` 与 `docs/agent-market-creator-guide.md` 中相关说明。
12. 网关启动时会通过 `ModuleRegistry` 自动探测 `/health` 并检测健康状态。

> 对于官方模块（如 `agent-reach`、`firecrawl`），随仓库预置，无需开发者自助注册。
