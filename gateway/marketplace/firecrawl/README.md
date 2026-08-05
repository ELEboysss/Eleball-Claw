# Firecrawl 集市模块

Eleball 弹丸集市中的网页抓取秘技模块，将 [Firecrawl](https://www.firecrawl.dev/) 包装为本地 stdio MCP 模块，供 Agent 工作流动态调用。

> 完整 Firecrawl API 参数、响应格式与错误码说明见 [`docs/firecrawl-api-guide.md`](docs/firecrawl-api-guide.md)。

## 能力

- `scrape`：将单个网页转换为干净 Markdown，返回标题、URL、描述等元数据。
- `crawl`：对指定网站启动批量爬取任务，返回任务 ID。
- `extract`：按 JSON Schema 从网页中提取结构化数据。

## 运行模型（stdio + process + auto_sku）

本模块为 **stdio MCP** 模块（`transport=mcp_stdio`、`deployment=process`），由网关（claw）autostart 拉起 `python main.py` 子进程，经 stdin/stdout JSON-RPC 通信：

- 零依赖：`main.py` 仅用 Python 标准库（`urllib`），无需 `pip install`，降低用户安装成本。
- `auto_sku=true`：网关探活拿到 `tools/list` 后自动派生 `scrape`/`crawl`/`extract` 三份可购买 SKU，**无需手写 `skus/*.json`**（见 `SkillRuntimeSKUService.DeriveSKUs`）。
- SKU ID 约定 `firecrawl-<tool>`，driver=`firecrawl`。

## 凭证配置

Firecrawl Cloud API 需要 API Key，在 `module.json` 顶层声明：

```json
"credentials": {
  "firecrawl_api_key": {"type": "api_key", "required": true, "scope": "module"}
}
```

- API Key 由网关 per-call 注入：每次 `tools/call` 经 `_meta.credentials.firecrawl_api_key` 透传给子进程，子进程用作 `x-api-key` 头调 Firecrawl API（不再 spawn 时烤进 env）。
- 凭证须 `scope=module`（同模块三 SKU 共用一份），网关按调用用户从 `module:firecrawl` 桶加载。
- 用户在 web「配置凭证」填写后，下次调用即取新值生效（per-call 注入，无需重 spawn 子进程）。
- `FIRECRAWL_BASE_URL`（非密）仍经 env 传入，默认 `https://api.firecrawl.dev`；自托管 Firecrawl 时改为自托管 API 地址（自托管通常无需 Key）。

## 与网关集成

1. claw 启动时 `ensureMarketplaceModules` 注册 `firecrawl` SkillRuntime（`mcp_stdio`+`process`）。
2. supervisor autostart -> `locateCommand("python")` 预解析（缺失给可读报错，阶段 D3）-> spawn -> 探活 -> `DeriveSKUs` 出三 SKU。
3. SKU manifest 的 `driver=firecrawl` 命中 SkillRuntimeDriver 别名，`metadata.module=firecrawl` 做在线门控。
4. 模块离线时，网关不会在 Agent 工作流中加载该工具。

> 云端（cloud）不做 process autostart，故 firecrawl 仅在 claw 本地派生 SKU（`sku_scope=claw`）。
