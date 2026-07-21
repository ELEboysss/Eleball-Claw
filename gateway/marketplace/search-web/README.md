# search-web（claw 内置联网搜索范例）

> 状态：✅ 模块实现已落地（main.py + Dockerfile + docker-compose）｜ 关联：`docs/marketing/claw-implementation-plan.md` §E、`docs/tool-driver-guide.md` §15.4

## 双重身份

1. **claw 本地联网搜索工具**：激活后注入对话页聊天窗的搜索源选择（矩阵明确：claw 对话页「联网」功能）。
2. **开发者范例**：可借鉴本模块源码开发自己的秘技（`transport_type=module`，标准 `/health` + `/execute` 接口）。

## 实现

复用上游 builtin SearchWeb 的搜索逻辑（`gateway/internal/service/search_provider.go`），支持四个搜索源，按环境变量选择可用源：

| 搜索源 | 环境变量 | 说明 |
|--------|---------|------|
| baidu | `BAIDU_API_KEY` | 百度千帆 AI 搜索，每日 100 次免费 |
| bing | `BING_SEARCH_API_KEY` | Bing Web Search |
| searxng | `SEARXNG_URL` | 自建 SearXNG 实例 |
| duckduckgo | （无需 key） | DuckDuckGo Lite，国际网络可用 |

未配置任何 key 时回退 DuckDuckGo。

## 标准接口

- `GET /health` -> `{module_id, version, status, capabilities}`
- `POST /execute` -> 按 `action` 执行：
  - `search`：参数 `{query, provider?}`，返回 `{results: [{title, url, snippet}]}`
  - `fetch`：参数 `{url}`，返回 `{title, url, content}`（网页正文）

请求体：
```json
{ "action": "search", "params": { "query": "Eleball" }, "user_id": "user_xxx" }
```

## 运行

```bash
# 本地直接运行
pip install -r requirements.txt
python main.py   # 监听 :8091

# Docker
docker compose up -d
```

## 接入 claw

claw 启动扫描 `marketplace/` 时发现 `module.json`，自动在本地 `modules` / `drivers` 表确保记录存在并激活（**官方预置，免拉镜像**）。激活后即可在对话页搜索源中选择。

第三方模块安装则按 `docker-compose.yml` 构建镜像、由 claw 技能页「安装到本地」拉取并校验签名后激活。

## 作为开发者范例

本模块展示了标准集市模块的最小实现：一个 FastAPI 服务，`/health` 上报能力、`/execute` 执行业务逻辑。开发者可参考 `main.py` 编写自己的秘技模块，通过 `POST /v1/market/modules/register`（需 auth_token）提交云端审核。
