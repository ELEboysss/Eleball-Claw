# 开发

## 1. 工具链

- **Go 1.25+**：便携 Go 在仓库 `.tools/go`（开发脚本 `dev-run.sh` / `dev-run.ps1` 自动使用）。系统无 Go 也可编译。
- **Node**：web / admin-web 开发需 Node（`npm run dev` / `npm run build`）。
- **Docker**（可选）：模块以 docker 容器运行；无 docker 网关照常运行，仅模块不可用。

> C 盘空间不足时把 `TMP` / `GOMODCACHE` / `GOCACHE` 指向项目盘。

## 2. 构建

```bash
# claw-server 单文件二进制（CGO_ENABLED=0，跨架构）
make build

# 交叉编译（默认当前架构；arm64 显式指定）
make build GOOS=linux GOARCH=arm64
```

构建参数：`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../claw-server ./cmd/claw-server`。

## 3. 运行与调试

```bash
./dev-run.sh            # Linux/macOS，用 .tools/go
./dev-run.ps1           # Windows
# 或
cd gateway && go run ./cmd/claw-server --port=8090
```

健康检查：`http://localhost:8090/health` 应返回 `{"node":"eleball-claw"}`。

## 4. 测试

```bash
cd gateway
go test ./internal/...           # 全部
go test ./internal/service/ -run TestShell -v   # 单个包/用例
```

单测就近放置（先例：`permission_service_test.go`、`agent_tool_loop_test.go`）。

## 5. web / admin-web

```bash
cd gateway/web && npm run dev     # 用户端 H5（0.0.0.0:5173）
cd gateway/admin-web && npm run dev
npm run build                     # 构建验证
```

web 与云端 eleball web 同构：共享设计语言与组件，按 AR-refactor web sync 规则同步（字节一致文件 + 真分叉文件分别处理）。

## 6. 模块开发

模块是可热插拔的服务单元（MCP / docker）。开发一个新模块：

1. 在 `gateway/marketplace/<name>/` 下放 `module.json`（声明 transport / 工具 schema / SKU）；
2. 提供 `main.py`（或对应实现）+ `Dockerfile` + `docker-compose.yml`；
3. 启动 claw 后自动扫描登记，或 `eleball-claw module up <name>` 构建。

模块经 `http://localhost:<端口>` 被 claw 调用。详见 `gateway/marketplace/README.md` 与范例 `marketplace/search-web/`。

## 7. 配置

- `gateway/configs/claw.yaml`：claw 本地配置（端口、SQLite、LLM 超时、JWT、ASR、模块自动上下线等）。
- `gateway/configs/hooks.json`：hook 配置（PreToolUse/PostToolUse/Stop/PreCompact，stdin-JSON/exit-code 契约）。
- 安装版配置生成在用户目录，开发时直接编辑 `configs/claw.yaml`。

## 8. 与云端 eleball 的开发关系

- **共享**（同步）：`pkg/`、API 契约、web、agent loop 骨架。
- **分叉**（不同步）：`tool_platform.go` / `tool_shell_builtin.go` / `agent_tools.go` / `permission_service.go`（claw 本地工具层与安全模型）。
- claw 是独立 git 仓库（eleball 主仓 submodule）；claw 改动在 claw 仓独立提交，主仓推进 submodule 指针。
