# 常见问题

## Windows 无 Docker Desktop：WSL 桥接 shim

没有 Docker Desktop 时可复用 WSL2 发行版内的 docker（claw 的 docker 探测会回退查找 `%USERPROFILE%\bin\docker.bat` 等 shim，无需加入 PATH）。

### 架设步骤

1. WSL 发行版（如 Ubuntu-24.04）内安装 docker：`apt install docker.io docker-compose-v2`。

2. 新建 `/usr/local/bin/docker-shim`（把 Windows 路径参数经 `wslpath` 转成 `/mnt/<盘>/...`，其余透传）：

   ```bash
   #!/bin/bash
   args=()
   for a in "$@"; do
     if [[ "$a" =~ ^[A-Za-z]:[\/] ]]; then args+=("$(wslpath -u "$a")"); else args+=("$a"); fi
   done
   exec docker "${args[@]}"
   ```

3. 新建 `/usr/local/bin/start-dockerd.sh`：`exec dockerd >>/var/log/dockerd.log 2>&1`（均 `chmod +x`）。

4. **dockerd 必须由计划任务常驻**：inbox 版 WSL 会在启动会话退出后回收其后台进程（nohup/setsid/隐藏窗口 holder 均无效），因此用计划任务托管（完全脱离控制台会话，且登录自启）：

   ```powershell
   schtasks /create /tn "claw-dockerd" /sc onlogon /ru "$env:USERNAME" `
     /tr "wsl -d Ubuntu-24.04 -u root /usr/local/bin/start-dockerd.sh" /f
   ```

5. 新建 `%USERPROFILE%\bin\docker.bat`：daemon 未运行时 `schtasks /run /tn "claw-dockerd"` 拉起并等待就绪（轮询 `docker info` 最长约 40s），然后把参数中的 `\` 替换为 `/` 后调用 `wsl -d Ubuntu-24.04 -- bash /usr/local/bin/docker-shim %ARGS%`。

之后 claw（exe 或 dev-run）即可正常自动上线模块；compose 文件路径自动经 `/mnt/<盘>` 映射进 WSL。

> 注意：WSL 内 docker 的镜像/容器与 Docker Desktop 互不共享。

## 模块镜像仓库配置

模块「拉镜像优先」策略从配置的 registry 拉取（默认指向 CI 推送的镜像仓库）。开源部署时请替换为自有仓库：

```yaml
modules:
  auto_start: true
  auto_stop: true
  registry: <your-registry>/eleball      # MODULES_REGISTRY，替换为你的镜像仓库
  image_tag: develop                      # MODULES_IMAGE_TAG
  pull_policy: pull_first                 # pull_first / build_only / pull_only
```

> compose 文件中的 `image:` 字段需与 `modules.registry`/`modules.image_tag` 对齐，否则 compose 会按文件内镜像名另拉/另建。
> 本地控制台可通过 `GET /v1/claw-console/system/status` 查询 docker/compose 可用性。

## 模块未自动上线

- 确认 docker 可用（`eleball-claw module ps` 或 `GET /v1/claw-console/system/status`）。
- 拉取失败会自动回退本地构建（`docker compose up -d --build`），不阻塞网关启动。
- 查日志：`eleball-claw module logs <name> -f`。

## LLM 请求失败

- Ele Agent 模式：确认 `claw.yaml` `server.eleagent_base_url` 指向可达的云端（默认 `https://api.eleball.cn/v1`），且账户登录态有效。
- BYOK 模式：确认自带 key 配置正确、上游端点可达。
- 超时：调大 `claw.yaml` `llm.timeout`（默认 120s）。
