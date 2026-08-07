# 本地工具层

> claw 的工具层是 agent 操作本地环境的能力核心。本文记录**现状**与**目标演进**。
> 目标详见 eleball 主仓 `.claude/plans/pr-e-claw-local-tool-layer.md`（PR-E 工作流）。

## 1. 内置工具（现状）

claw agent 当前可调用的内置工具：

| 工具 | 能力 |
|------|------|
| `WriteFile` | 写文件 |
| `ReadFile` | 读文件 |
| `StrReplaceFile` | 字符串替换式局部编辑（path / old_string / new_string） |
| `Grep` | 内容搜索（builtin 实现，有限 flag） |
| `Shell` | 执行命令（受限，见下） |
| `OCR` | 图片文字识别 |
| `FetchURL` | 抓取 URL |
| `SearchWeb` | 联网搜索（经 search-web 模块） |

工具注册表在 `gateway/internal/service/agent_tools.go`（`toolNameAliases`）。

## 2. 现状限制（继承自云端安全沙箱）

claw 当前的 Shell 工具是**白名单沙箱**，约束来自云端多租户安全模型经同构继承：

- **白名单**：仅允许 `ls/cat/pwd/echo/head/tail/wc/grep/find/sort/uniq/cut/awk/sed/python/node/pip/npm/npx/...` 等只读或受控命令。
- **禁元字符**：`; | & $ ( ) \` < >` 与换行被拒--**不支持管道、重定向、链式（&&/||/;）、子命令 $()、内联 -c/-e**。
- **无 git / 构建工具链**：白名单不含 `git`/`go`/`make`/`cargo`/`gradle`/`mvn`。
- **grep/find 是 Go 子集重写**（`tool_shell_builtin.go`）：flag 有限（`-r/-i/-c/-l/-v`），无 `--type`/glob/`-A/-B/-C`/multiline。
- **无独立 Glob 工具**：仅 `filepath.Match`（`* ?`）。
- **同步阻塞执行**：`cmd.CombinedOutput()` 一次性返回，无流式、无后台进程、无输出截断。

**后果**：claw 无法胜任编程--不能跑 `grep -rn foo src/ | head`、`git diff`、`go test ./...`、后台 dev server。这套约束对云端多租户正确，对本地单用户可信场景错误。

## 3. 目标演进（PR-E）

构建 claw **本地专有工具层**，脱离云端安全沙箱：

| 工具 | 目标 |
|------|------|
| **Shell** | 真 bash（管道/重定向/链式/`$()`/`-c`）+ 权限确认 + 流式输出 + 后台进程 |
| **Git** | 新增：status/diff/log/blame/show/commit/stash/checkout（写操作走权限确认） |
| **构建工具链** | 放行 `go/make/cargo/gradlew/mvn/docker`（走权限确认）+ 按 cwd 项目类型识别 |
| **Grep** | 优先调系统 `rg`（ripgrep）fallback builtin；支持 `--type`/glob/`-A/-B/-C`/multiline/`output_mode`/`head_limit` |
| **Glob** | 新增独立工具：`**` 递归、按 mtime 排序 |
| **Edit** | 强化：read-before-edit 强制、唯一匹配校验、`replace_all`、文件状态追踪防 stale |
| **BackgroundShell** | 新增：`run_in_background`、流式、轮询、超时与取消分离 |
| **Read** | 增 `offset`/`limit`、图片/PDF/Jupyter 支持 |

## 4. 安全模型切换（D3）

- **现状**：白名单 + 元字符禁令（云端继承）。
- **目标**：本地权限确认模型（类 Claude Code）：
  - 只读操作（ReadFile/Grep/Glob/`git status`/`git diff`）自动放行；
  - 写改/shell 执行需确认（可会话级 allowlist「允许本次/允许此类/拒绝」）；
  - 危险操作（`rm -rf`/`git push --force`/`sudo`/写 cwd 外）二次确认且不可 allowlist。
- **配置**：`claw.yaml` `permission.mode: confirm|auto|strict`（默认 confirm）。

详见 [security.md](security.md)。

## 5. 语言

工具层用 Go 实现（`os/exec` 完全支持管道/流式/后台）。语言切换仅在嵌入式目标或实测性能瓶颈时评估（见 eleball 主仓路线图 D1）。
