# search-web（claw 内置联网搜索范例）

> 状态：📋 P4 待实现 ｜ 关联：`docs/marketing/claw-implementation-plan.md` §E、`docs/tool-driver-guide.md` §15.4

## 双重身份

1. **claw 本地联网搜索工具**：激活后注入对话页聊天窗的搜索源选择（矩阵明确：claw 对话页「联网」功能）。
2. **开发者范例**：可借鉴本模块源码开发自己的秘技（`transport_type=module`，标准 `/health` + `/execute` 接口）。

## 现状

SearchWeb 当前是 `gateway/internal/service/builtin_tool_driver.go` 的内置工具之一（与 ReadFile/WriteFile/Shell/OCR/FetchURL 并列）。P4 将其抽出为本模块：

```
search-web/
├── module.json          # 模块清单（本目录）
├── main.py              # FastAPI 标准接口实现（/health + /execute）
├── Dockerfile           # 模块镜像（第三方拉镜像安装用）
├── docker-compose.yml   # 本地编排
└── README.md            # 本文件（开发者范例文档）
```

## 实现要点（P4）

- 复用上游 SearchWeb 的搜索逻辑（调搜索引擎 API）。
- 作为独立 module 进程暴露 `/health` + `/execute`，遵循 `docs/tool-driver-guide.md` §9 标准接口。
- `module.json` 的 `url` 指向本地 module runtime 端口（默认 8091）。
- 提供清晰源码与注释，作为开发者自定义秘技的参考实现。

## 接入 claw

claw 启动扫描 `marketplace/` 时发现本 `module.json`，自动在本地 `modules` / `drivers` 表确保记录存在并激活（无需拉镜像，官方预置）。激活后即可在对话页搜索源中选择。
