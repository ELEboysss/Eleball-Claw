# Firecrawl 集市模块

Eleball 弹丸集市中的网页抓取秘技模块，将 [Firecrawl](https://www.firecrawl.dev/) 包装为标准 Eleball Marketplace Module，供 Agent 工作流动态调用。

> 完整 Firecrawl API 参数、响应格式与错误码说明见 [`docs/firecrawl-api-guide.md`](docs/firecrawl-api-guide.md)。

## 能力

- `scrape`：将单个网页转换为干净 Markdown，返回标题、URL、描述等元数据。
- `crawl`：对指定网站启动批量爬取任务，返回任务 ID。
- `extract`：按 JSON Schema 从网页中提取结构化数据。

## 标准接口

- `GET  /health` — 返回模块状态与能力清单
- `POST /execute` — 执行 `scrape` / `crawl` / `extract`

请求体示例：

```json
{
  "action": "scrape",
  "params": {"url": "https://example.com"},
  "user_id": "user_xxx"
}
```

## 部署方式

### 1. 使用 Firecrawl Cloud API（默认，最轻量）

生产环境默认接入 Firecrawl Cloud API，无需自托管 Firecrawl 后端。

```bash
docker run -d \
  --name eleball-firecrawl \
  --network eleball-net \
  -e PORT=8080 \
  -e FIRECRAWL_BASE_URL=https://api.firecrawl.dev \
  -e FIRECRAWL_API_KEY=your_api_key \
  eleball/firecrawl-module
```

> API Key 可从 https://firecrawl.dev 获取。正式使用时，推荐在 SKU 的「配置凭证」中由用户自行填写 `firecrawl_api_key`，网关会按请求透传给模块；模块级 `FIRECRAWL_API_KEY` 仅作为未配置 SKU 凭证时的兜底。

### 2. 本地源码启动（开发测试）

```bash
cd gateway/marketplace/firecrawl
python -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
FIRECRAWL_BASE_URL=http://localhost:3002 python main.py
```

### 3. 自托管 Firecrawl（可选，非默认）

Firecrawl 自托管较重（Redis/Playwright/Postgres/RabbitMQ），需要额外启动 `providers/firecrawl/docker-compose.yaml`。
配置本模块时，将 `FIRECRAWL_BASE_URL` 指向自托管 API 地址（如 `http://firecrawl-api:3002`），并将 `FIRECRAWL_API_KEY` 留空（自托管通常无需 Key）。

## SKU 拆分

Firecrawl 的三个 action 已拆分为独立 SKU，每个 SKU 只包含一个 action，便于单独定价与上下架：

| SKU ID | action | manifest |
|--------|--------|----------|
| `firecrawl-scrape` | `scrape` | `skus/scrape.json` |
| `firecrawl-crawl` | `crawl` | `skus/crawl.json` |
| `firecrawl-extract` | `extract` | `skus/extract.json` |

网关启动时会通过 `seedFirecrawlSKUs` 自动预置这三个 SKU。

## 与网关集成

1. 确保模块容器服务名为 `firecrawl`，并加入 `eleball-net` 网络。
2. 网关启动时会通过 `ModuleRegistry` 探测 `http://firecrawl:8080/health`。
3. 通过 `POST /v1/admin/modules` 或 `POST /v1/market/modules/register` 注册模块（`firecrawl` 已作为内置模块预置）。
4. 在 SKU 的 `manifest_json` 中设置：
   - `driver`: `firecrawl`（已注册的驱动别名，不要写具体 module_id）
5. 模块离线时，网关不会在 Agent 工作流中加载该工具。
