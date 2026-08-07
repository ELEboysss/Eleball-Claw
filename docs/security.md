# 安全

## 1. 本地数据自主

- 对话、会话、模块安装记录、助手配置落本地 SQLite（`data/claw.db`），数据不出设备。
- claw 不上传用户数据到云端；仅 LLM 推理请求经云端转发（Ele Agent 模式）或直连上游（BYOK 模式）。

## 2. 凭证加密

- API Key 用 **AES-256-GCM** 加密入库，请求期间仅驻内存；DB 无明文，日志禁打印 Key。
- 主密钥经 `ENCRYPTION_MASTER_KEY` 注入；存在加密 Key 但缺主密钥时**拒绝启动**。
- 实现在 `pkg/crypto/`。

## 3. 模块签名校验

- 第三方模块镜像需 **cosign / sigstore** 签名校验通过方可激活，防供应链篡改。
- 官方预置模块随仓库分发（免容器或受信任镜像）。

## 4. shell 安全模型（现状与目标）

### 现状（云端继承的白名单沙箱）

当前 Shell 工具约束来自云端多租户安全模型：

- 命令白名单（仅受控只读/开发命令）；
- 元字符禁令（`; | & $ () \` <>` 与换行）--禁管道/重定向/链式/子命令；
- 无 git / 构建工具链。

**这套模型对云端多租户不可信场景正确，对本地单用户可信场景过度约束**--它阻止 claw 胜任编程。

### 目标（本地权限确认模型）

切换为类 Claude Code 的**权限确认**模型：

| 操作类 | 默认行为 | 例 |
|--------|---------|-----|
| 只读 | 自动放行 | ReadFile/Grep/Glob/`ls`/`cat`/`git status`/`git diff`/`git log` |
| 写/改 | 需确认（可会话级 allowlist） | WriteFile/StrReplaceFile/`git commit`/`npm install` |
| shell 执行 | 需确认（按命令指纹 allowlist） | 任意 bash 命令 |
| 危险 | 二次确认 + 不可 allowlist | `rm -rf /`/`> /dev/sda`/`sudo`/写 cwd 外 |

- 不再用元字符禁令--本地允许管道/重定向/链式/`$()`/`-c`。
- 危险操作正则黑名单仍硬拒，不可 allowlist。
- 配置：`claw.yaml` `permission.mode: confirm|auto|strict`（默认 confirm；auto 全自动用于受信任脚本；strict 全确认）。

> 切换在 PR-E 工作流落地（见 eleball 主仓 `pr-e-claw-local-tool-layer.md`）。云端安全模型**保留不动**，两套模型各自演进。

## 5. 鉴权

- 本地 JWT 验签，失败 fallback 云端 `/auth/me`。
- 本地控制台 `/v1/claw-console/*` 需用户账户登录态。
