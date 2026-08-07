# 快速开始

## 一键安装

安装脚本从云端 release 端点按架构下载二进制（含 SHA256 校验），生成配置并提示启动命令。

```bash
# Linux / macOS
curl -fsSL https://eleball.cn/install.sh | sh

# Windows（PowerShell）
irm https://eleball.cn/install.ps1 | iex
```

## 编译运行

```bash
# 编译单文件二进制（CGO_ENABLED=0，无 CGO，跨架构）
make build

# 或直接 go run（需本机 Go 1.25+）
cd gateway && go run ./cmd/claw-server --port=8090
```

启动后访问 `http://localhost:8090/health`，应返回：

```json
{"code":0,"message":"ok","data":{"node":"eleball-claw"}}
```

`serve` 启动成功后会自动用系统默认浏览器打开 `http://localhost:<port>`；远程/服务化部署可用 `CLAW_NO_BROWSER=1` 关闭（Linux 无桌面环境自动跳过）。

## 开发模式

仓库根提供开发脚本，自动用本地 `.tools/go`（便携 Go 工具链）：

```bash
./dev-run.sh        # Linux/macOS
./dev-run.ps1       # Windows
```

开发细节见 [development.md](development.md)。

## 模块（秘技）管理

安装版 claw 的模块目录默认在 `~/.eleball-claw/marketplace`（Windows 为 `%USERPROFILE%\.eleball-claw\marketplace`，`CLAW_MARKETPLACE_DIR` 可覆盖）。自定义模块放入一个含 `module.json` 的子目录即可被网关扫描登记。

模块以 docker 容器运行，通过二进制内置命令管理（无需 clone 仓库、无需 bash，Windows 可用）：

```bash
eleball-claw module ls              # 列出模块与状态
eleball-claw module up              # 构建并启动全部模块
eleball-claw module up stt          # 只启动指定模块
eleball-claw module ps / down       # 查看状态 / 停止
eleball-claw module logs stt -f     # 跟踪日志
```

`serve` 启动后后台自动上线含 docker-compose.yml 的预置模块（拉镜像优先、本地构建兜底）；收到退出信号时自动 `docker compose down`。无 docker 仅告警跳过，网关照常运行。

模块端口固定发布到宿主机（stt 8092 / search-web 8091 / firecrawl 8093 / agent-reach 8094），claw 经 `http://localhost:<端口>` 调用。

## 下一步

- 想懂整体架构：[architecture.md](architecture.md)
- 想做开发/测试：[development.md](development.md)
- 想了解工具能力与限制：[tool-layer.md](tool-layer.md)
