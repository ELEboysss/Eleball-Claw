# Firecrawl API 接入完整指南

> 本文档基于 Firecrawl 官方文档整理，API 版本以 `v2` 为主。Base URL：`https://api.firecrawl.dev`

---

## 目录

1. [快速开始](#1-快速开始)
2. [鉴权方式](#2-鉴权方式)
3. [核心 API 概览](#3-核心-api-概览)
4. [单页抓取：Scrape](#4-单页抓取-scrape)
5. [整站爬取：Crawl](#5-整站爬取-crawl)
6. [URL 发现：Map](#6-url-发现-map)
7. [搜索抓取：Search](#7-搜索抓取-search)
8. [结构化提取：Extract](#8-结构化提取-extract)
9. [批量抓取：Batch Scrape](#9-批量抓取-batch-scrape)
10. [页面交互：Interact](#10-页面交互-interact)
11. [Webhook](#11-webhook)
12. [SDK 接入](#12-sdk-接入)
13. [状态码与错误码](#13-状态码与错误码)
14. [最佳实践与 FAQ](#14-最佳实践与-faq)

---

## 1. 快速开始

Firecrawl 是一个面向 AI 应用的数据抓取平台，能够将任意网页转换为 LLM 友好的 Markdown、HTML、结构化数据，并支持整站爬取、搜索、截图、代理、浏览器自动化等能力。

### 1.1 获取 API Key

1. 访问 [https://www.firecrawl.dev/](https://www.firecrawl.dev/) 注册账号。
2. 进入控制台，获取 API Key（格式通常为 `fc-xxxxxxxxxxxx`）。
3. 在请求头中通过 `Authorization: Bearer <API_KEY>` 传递。

### 1.2 第一个请求

```bash
curl --request POST \
  --url https://api.firecrawl.dev/v2/scrape \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx' \
  --header 'Content-Type: application/json' \
  --data '{
    "url": "https://example.com",
    "formats": ["markdown"]
  }'
```

---

## 2. 鉴权方式

所有 API 请求都需要在 HTTP Header 中携带 API Key：

```http
Authorization: Bearer <YOUR_API_KEY>
Content-Type: application/json
```

> **注意**：不要把 API Key 暴露在前端代码或公开仓库中。建议通过后端服务或环境变量调用。

---

## 3. 核心 API 概览

| 功能 | 端点 | 说明 |
|------|------|------|
| **Scrape** | `POST /v2/scrape` | 抓取单个页面 |
| **Crawl** | `POST /v2/crawl` | 爬取整个网站 |
| **Crawl Status** | `GET /v2/crawl/{id}` | 查询爬取任务状态和结果 |
| **Map** | `POST /v2/map` | 快速发现网站 URL 列表 |
| **Search** | `POST /v2/search` | 搜索网页并返回内容 |
| **Extract** | `POST /v2/extract` | 用自然语言提取结构化数据 |
| **Batch Scrape** | `POST /v2/batch/scrape` | 批量抓取多个 URL |
| **Batch Status** | `GET /v2/batch/scrape/{id}` | 查询批量任务状态 |
| **Interact** | `POST /v2/scrape/{id}/interact` | 在页面中执行交互操作 |

---

## 4. 单页抓取：Scrape

`POST /v2/scrape`

将单个网页转换为 Markdown、HTML、结构化数据或截图。

### 4.1 请求示例

```bash
curl --request POST \
  --url https://api.firecrawl.dev/v2/scrape \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx' \
  --header 'Content-Type: application/json' \
  --data '{
    "url": "https://example.com",
    "formats": ["markdown", "html"],
    "onlyMainContent": true,
    "timeout": 60000
  }'
```

### 4.2 请求参数

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `url` | string | 是 | - | 要抓取的 URL |
| `formats` | array | 否 | `["markdown"]` | 输出格式：`markdown`、`html`、`rawHtml`、`links`、`images`、`screenshot`、`json`、`product`、`menu` |
| `onlyMainContent` | boolean | 否 | `true` | 只保留正文，过滤导航、页脚、广告等 |
| `onlyCleanContent` | boolean | 否 | `false` | 使用 LLM 进一步清理内容 |
| `includeTags` | array | 否 | - | 只保留指定 HTML 标签 |
| `excludeTags` | array | 否 | - | 排除指定 HTML 标签 |
| `headers` | object | 否 | - | 自定义请求头（如 Cookie、User-Agent） |
| `waitFor` | integer | 否 | `0` | 页面加载后等待毫秒数 |
| `mobile` | boolean | 否 | `false` | 模拟移动设备 |
| `skipTlsVerification` | boolean | 否 | `true` | 跳过 TLS 证书校验 |
| `timeout` | integer | 否 | `60000` | 超时时间，范围 `1000`–`300000` ms |
| `actions` | array | 否 | - | 抓取前执行交互操作（点击、输入、滚动、截图等） |
| `location` | object | 否 | `{country: "US"}` | 设置地理位置、语言、时区 |
| `proxy` | enum | 否 | `auto` | 代理类型：`basic`、`enhanced`、`auto` |
| `maxAge` | integer | 否 | `172800000` | 缓存最大有效期（毫秒），默认 2 天 |
| `minAge` | integer | 否 | - | 最小缓存年龄，命中缓存则直接返回 |
| `storeInCache` | boolean | 否 | `true` | 是否缓存结果 |
| `lockdown` | boolean | 否 | `false` | 仅读取缓存，不发起外部请求 |
| `redactPII` | boolean | 否 | `false` | 是否脱敏个人身份信息 |
| `zeroDataRetention` | boolean | 否 | `false` | 是否开启零数据保留（需联系官方开通） |
| `extract` | object | 否 | - | 结构化提取配置（替代 Extract 端点） |

### 4.3 输出格式说明

- `markdown`：页面正文 Markdown（最常用）。
- `html`：清洗后的 HTML。
- `rawHtml`：原始 HTML，未清洗。
- `links`：页面内所有链接。
- `images`：页面内所有图片 URL。
- `screenshot`：页面截图（Base64 或 URL）。
- `json`：基于 JSON Schema 提取的结构化数据。
- `product` / `menu`：针对电商/餐饮页面的预置结构化提取。

### 4.4 成功响应示例

```json
{
  "success": true,
  "data": {
    "markdown": "# Example Domain\n\nThis domain is for use in illustrative examples...",
    "html": "<html>...</html>",
    "metadata": {
      "title": "Example Domain",
      "description": "A domain for examples",
      "sourceURL": "https://example.com",
      "statusCode": 200
    }
  }
}
```

### 4.5 失败响应示例

```json
{
  "success": false,
  "error": "URL is required and must be a valid URL"
}
```

---

## 5. 整站爬取：Crawl

`POST /v2/crawl`

从指定入口 URL 开始，自动发现并爬取整个网站。

### 5.1 请求示例

```bash
curl --request POST \
  --url https://api.firecrawl.dev/v2/crawl \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx' \
  --header 'Content-Type: application/json' \
  --data '{
    "url": "https://example.com",
    "limit": 1000,
    "excludePaths": ["/blog/*"],
    "scrapeOptions": {
      "formats": ["markdown"],
      "onlyMainContent": true
    }
  }'
```

### 5.2 请求参数

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `url` | string | 是 | - | 爬取起始 URL |
| `prompt` | string | 否 | - | 自然语言描述爬取需求，自动生成参数 |
| `excludePaths` | array | 否 | - | 排除路径（支持通配符/正则） |
| `includePaths` | array | 否 | - | 只包含路径（支持通配符/正则） |
| `maxDiscoveryDepth` | integer | 否 | - | 最大发现深度 |
| `sitemap` | enum | 否 | `include` | sitemap 处理：`skip` / `include` / `only` |
| `ignoreQueryParameters` | boolean | 否 | `false` | 忽略不同 query 参数导致的重复 URL |
| `regexOnFullURL` | boolean | 否 | `false` | 正则匹配完整 URL（含 query 参数） |
| `limit` | integer | 否 | `10000` | 最大抓取页数 |
| `crawlEntireDomain` | boolean | 否 | `false` | 是否跨兄弟/父级路径爬取 |
| `allowExternalLinks` | boolean | 否 | `false` | 是否跟踪外部链接 |
| `allowSubdomains` | boolean | 否 | `false` | 是否跟踪子域名链接 |
| `ignoreRobotsTxt` | boolean | 否 | `false` | 是否忽略 robots.txt（企业版） |
| `delay` | number | 否 | - | 每次抓取间隔秒数 |
| `maxConcurrency` | integer | 否 | - | 最大并发数 |
| `webhook` | object | 否 | - | 接收进度/结果的 Webhook 配置 |
| `scrapeOptions` | object | 否 | - | 每个页面的抓取选项（同 `/v2/scrape`） |
| `zeroDataRetention` | boolean | 否 | `false` | 零数据保留 |

### 5.3 成功响应示例

```json
{
  "success": true,
  "id": "crawl_abc123",
  "url": "https://api.firecrawl.dev/v2/crawl/crawl_abc123"
}
```

### 5.4 查询爬取状态

```bash
curl --request GET \
  --url https://api.firecrawl.dev/v2/crawl/crawl_abc123 \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx'
```

响应示例：

```json
{
  "success": true,
  "status": "completed",
  "total": 100,
  "completed": 100,
  "creditsUsed": 100,
  "expiresAt": "2024-12-31T23:59:59Z",
  "data": [
    {
      "markdown": "...",
      "metadata": {
        "sourceURL": "https://example.com/page1",
        "statusCode": 200
      }
    }
  ]
}
```

状态字段可能值：`scraping`（进行中）、`completed`（已完成）、`failed`（失败）、`cancelled`（已取消）。

### 5.5 取消爬取任务

```bash
curl --request DELETE \
  --url https://api.firecrawl.dev/v2/crawl/crawl_abc123 \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx'
```

---

## 6. URL 发现：Map

`POST /v2/map`

快速获取一个网站的所有 URL 列表，不抓取内容，适合用于网站索引和入口分析。

### 6.1 请求示例

```bash
curl --request POST \
  --url https://api.firecrawl.dev/v2/map \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx' \
  --header 'Content-Type: application/json' \
  --data '{
    "url": "https://example.com",
    "sitemap": true,
    "limit": 5000
  }'
```

### 6.2 请求参数

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `url` | string | 是 | - | 目标网站 URL |
| `sitemap` | boolean | 否 | `true` | 是否解析 sitemap.xml |
| `limit` | integer | 否 | `5000` | 返回 URL 最大数量 |
| `ignoreSitemap` | boolean | 否 | `false` | 是否忽略 sitemap |
| `includeSubdomains` | boolean | 否 | `false` | 是否包含子域名 URL |
| `search` | string | 否 | - | 只返回匹配关键词的 URL |

### 6.3 成功响应示例

```json
{
  "success": true,
  "links": [
    "https://example.com/",
    "https://example.com/about",
    "https://example.com/blog"
  ]
}
```

---

## 7. 搜索抓取：Search

`POST /v2/search`

基于关键词搜索网页，并返回搜索结果页面的完整内容（Markdown/HTML）。

### 7.1 请求示例

```bash
curl --request POST \
  --url https://api.firecrawl.dev/v2/search \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx' \
  --header 'Content-Type: application/json' \
  --data '{
    "query": "firecrawl api documentation",
    "limit": 5,
    "scrapeOptions": {
      "formats": ["markdown"]
    }
  }'
```

### 7.2 请求参数

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `query` | string | 是 | - | 搜索关键词 |
| `limit` | integer | 否 | `5` | 返回结果数量 |
| `scrapeOptions` | object | 否 | - | 每个结果的抓取选项 |
| `lang` | string | 否 | `en` | 搜索语言 |
| `country` | string | 否 | `us` | 搜索国家/地区 |

### 7.3 成功响应示例

```json
{
  "success": true,
  "data": [
    {
      "title": "Firecrawl Documentation",
      "url": "https://docs.firecrawl.dev/",
      "markdown": "...",
      "metadata": {
        "sourceURL": "https://docs.firecrawl.dev/",
        "statusCode": 200
      }
    }
  ]
}
```

---

## 8. 结构化提取：Extract

`POST /v2/extract`

使用自然语言或 JSON Schema，从网页中提取结构化数据。适合抽取价格、产品信息、联系地址、文章元数据等。

### 8.1 请求示例

```bash
curl --request POST \
  --url https://api.firecrawl.dev/v2/extract \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx' \
  --header 'Content-Type: application/json' \
  --data '{
    "url": "https://example.com/products",
    "prompt": "Extract all product names and prices as a list."
  }'
```

### 8.2 请求参数

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `url` | string | 是 | - | 要提取的 URL |
| `prompt` | string | 否 | - | 自然语言提取提示 |
| `schema` | object | 否 | - | JSON Schema 定义输出结构 |
| `systemPrompt` | string | 否 | - | 自定义系统提示 |
| `temperature` | number | 否 | `0` | LLM 温度参数 |
| `enableWebSearch` | boolean | 否 | `false` | 是否启用联网搜索辅助提取 |
| `includeSubdomains` | boolean | 否 | `false` | 是否包含子域名页面 |

### 8.3 使用 JSON Schema 示例

```json
{
  "url": "https://example.com/products",
  "schema": {
    "type": "object",
    "properties": {
      "products": {
        "type": "array",
        "items": {
          "type": "object",
          "properties": {
            "name": {"type": "string"},
            "price": {"type": "string"}
          },
          "required": ["name", "price"]
        }
      }
    },
    "required": ["products"]
  }
}
```

### 8.4 成功响应示例

```json
{
  "success": true,
  "data": {
    "products": [
      {"name": "Product A", "price": "$19.99"},
      {"name": "Product B", "price": "$29.99"}
    ]
  }
}
```

---

## 9. 批量抓取：Batch Scrape

`POST /v2/batch/scrape`

同时抓取多个 URL，异步返回结果。

### 9.1 请求示例

```bash
curl --request POST \
  --url https://api.firecrawl.dev/v2/batch/scrape \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx' \
  --header 'Content-Type: application/json' \
  --data '{
    "urls": [
      "https://example.com/page1",
      "https://example.com/page2"
    ],
    "formats": ["markdown"]
  }'
```

### 9.2 请求参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `urls` | array | 是 | 要抓取的 URL 列表 |
| `formats` | array | 否 | 输出格式 |
| `onlyMainContent` | boolean | 否 | 同 Scrape |
| `scrapeOptions` | object | 否 | 每个 URL 的抓取选项 |
| `webhook` | object | 否 | 任务完成后的 Webhook |

### 9.3 查询批量任务状态

```bash
curl --request GET \
  --url https://api.firecrawl.dev/v2/batch/scrape/batch_abc123 \
  --header 'Authorization: Bearer fc_xxxxxxxxxxxx'
```

---

## 10. 页面交互：Interact

`POST /v2/scrape/{id}/interact`

在已创建的 Scrape 任务会话中执行浏览器自动化操作（点击、输入、滚动、截图等）。

### 10.1 常用 action 类型

| action | 说明 | 示例参数 |
|--------|------|----------|
| `click` | 点击元素 | `{"type": "click", "selector": "#submit"}` |
| `input` | 输入文本 | `{"type": "input", "selector": "#search", "value": "hello"}` |
| `scroll` | 滚动页面 | `{"type": "scroll", "direction": "down", "amount": 800}` |
| `screenshot` | 截图 | `{"type": "screenshot"}` |
| `wait` | 等待毫秒 | `{"type": "wait", "milliseconds": 2000}` |

### 10.2 在 Scrape 中直接使用 actions

```json
{
  "url": "https://example.com",
  "formats": ["markdown"],
  "actions": [
    {"type": "click", "selector": "#load-more"},
    {"type": "wait", "milliseconds": 2000}
  ]
}
```

---

## 11. Webhook

Firecrawl 支持在 Crawl / Batch Scrape 任务完成或进度更新时，向你的服务器发送 Webhook。

### 11.1 配置示例

```json
{
  "url": "https://example.com",
  "limit": 100,
  "webhook": {
    "url": "https://your-server.com/webhook",
    "headers": {
      "X-Secret": "your-secret"
    },
    "events": ["completed", "page", "failed"]
  }
}
```

### 11.2 Webhook 事件类型

- `completed`：任务完成时触发。
- `page`：每完成一个页面时触发。
- `failed`：任务失败时触发。

### 11.3 Webhook 负载示例

```json
{
  "type": "page",
  "id": "crawl_abc123",
  "data": {
    "markdown": "...",
    "metadata": {
      "sourceURL": "https://example.com/page1"
    }
  }
}
```

---

## 12. SDK 接入

Firecrawl 提供官方 SDK，支持 Python 和 Node.js。

### 12.1 Python SDK

#### 安装

```bash
pip install firecrawl-py
```

#### 单页抓取

```python
from firecrawl import FirecrawlApp

app = FirecrawlApp(api_key="fc_xxxxxxxxxxxx")

result = app.scrape_url("https://example.com", {
    "formats": ["markdown"],
    "onlyMainContent": True
})

print(result["data"]["markdown"])
```

#### 整站爬取

```python
from firecrawl import FirecrawlApp

app = FirecrawlApp(api_key="fc_xxxxxxxxxxxx")

crawl = app.crawl_url("https://example.com", {
    "limit": 100,
    "scrapeOptions": {
        "formats": ["markdown"]
    }
})

print(crawl)
```

### 12.2 Node.js SDK

#### 安装

```bash
npm install @mendable/firecrawl-js
```

#### 单页抓取

```javascript
import { FirecrawlApp } from '@mendable/firecrawl-js';

const app = new FirecrawlApp({ apiKey: 'fc_xxxxxxxxxxxx' });

const result = await app.scrapeUrl('https://example.com', {
  formats: ['markdown'],
  onlyMainContent: true
});

console.log(result.data.markdown);
```

#### 整站爬取

```javascript
const crawl = await app.crawlUrl('https://example.com', {
  limit: 100,
  scrapeOptions: {
    formats: ['markdown']
  }
});

console.log(crawl);
```

---

## 13. 状态码与错误码

### 13.1 HTTP 状态码

| 状态码 | 含义 |
|--------|------|
| 200 | 请求成功 |
| 400 | 请求参数错误 |
| 401 | 未提供 API Key 或 Key 无效 |
| 402 | 需要付费或额度不足 |
| 404 | 资源不存在 |
| 408 | 请求超时 |
| 429 | 超出速率限制 |
| 500 | Firecrawl 服务端错误 |

### 13.2 常见 Firecrawl 错误码

| 错误码 | 说明 | 处理建议 |
|--------|------|----------|
| `SCRAPE_TIMEOUT` | 页面加载超时 | 增大 `timeout` 参数，或检查目标站可用性 |
| `SCRAPE_ALL_ENGINES_FAILED` | 所有抓取引擎失败 | 尝试更换代理或 `skipTlsVerification` |
| `SCRAPE_SSL_ERROR` | SSL 证书异常 | 设置 `skipTlsVerification: true` |
| `SCRAPE_DNS_RESOLUTION_ERROR` | DNS 解析失败 | 检查 URL 是否正确 |
| `SCRAPE_ACTION_ERROR` | 页面操作执行失败 | 检查 selector 是否正确 |
| `SCRAPE_UNSUPPORTED_FILE_ERROR` | 不支持的文件类型或超过 10MB | 更换 URL 或抓取 HTML 页面 |
| `SCRAPE_ZDR_VIOLATION_ERROR` | 零数据保留与缓存类选项冲突 | 关闭 `storeInCache` 或 `minAge` 等 |
| `SCRAPE_LOCKDOWN_CACHE_MISS` | lockdown 模式缓存未命中 | 关闭 `lockdown` 或等待缓存生效 |
| `RATE_LIMIT_EXCEEDED` | 超出速率限制 | 降低请求频率或升级套餐 |

---

## 14. 最佳实践与 FAQ

### 14.1 最佳实践

1. **优先使用缓存**：合理设置 `maxAge`，命中缓存可大幅提速并降低成本。
2. **只取所需内容**：使用 `onlyMainContent: true` 和 `excludeTags` 减少 Token 消耗。
3. **合理设置并发**：爬取大量页面时，控制 `maxConcurrency` 和 `delay`，避免被封禁。
4. **善用 Webhook**：大任务使用 Webhook 接收进度，避免频繁轮询。
5. **错误重试**：对 `SCRAPE_TIMEOUT` 和 `RATE_LIMIT_EXCEEDED` 实现指数退避重试。
6. **保护 API Key**：不要在前端暴露 Key，建议通过后端代理调用。
7. **遵守 robots.txt**：默认会遵守，企业版可忽略，但请合法合规使用。

### 14.2 常见问题

**Q：免费额度多少？**
A：Firecrawl 通常提供每月一定数量的免费积分，具体以官网控制台为准。

**Q：如何提升抓取速度？**
A：开启缓存、使用 `enhanced` 代理、设置合理的 `maxConcurrency`。

**Q：支持抓取 SPA（单页应用）吗？**
A：支持，可通过 `waitFor` 或 `actions` 等待页面渲染完成。

**Q：可以抓取需要登录的页面吗？**
A：可以，通过 `headers` 传递 Cookie，或先调用 Interact 端点登录。

**Q：抓取结果中的图片是 Base64 吗？**
A：视 `formats` 配置，screenshot 可返回 Base64 或 URL，图片列表通常返回 URL。

---

## 附录：完整请求头模板

```http
POST /v2/scrape HTTP/1.1
Host: api.firecrawl.dev
Authorization: Bearer fc_xxxxxxxxxxxx
Content-Type: application/json

{
  "url": "https://example.com",
  "formats": ["markdown"]
}
```

---

> 文档版本：2025-01（基于 Firecrawl v2 API）
> 官方文档地址：https://docs.firecrawl.dev/
