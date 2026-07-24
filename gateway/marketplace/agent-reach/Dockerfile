# Eleball Marketplace Skill: Agent-Reach（claw 宿主机模式，build context 为本目录）
# 作为独立容器运行，暴露 HTTP 接口供网关调用，实现集市秘技的模块化部署。
#
# Agent-Reach 本体从 atomgit 克隆（安装版 claw 的 marketplace home 不含 providers/ 子模块），
# 社交 CLI（twitter-cli / rdt-cli / xiaohongshu-cli）从清华 PyPI 镜像安装，无需访问 GitHub。
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/library/debian:bookworm-slim

# 使用国内镜像加速 apt/pip/npm
RUN sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl python3 python3-pip python3-venv nodejs npm git gh && \
    rm -rf /var/lib/apt/lists/*

# 默认使用清华 PyPI 与 npmmirror 加速，避免官方源连接超时
ENV PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple \
    PIP_TRUSTED_HOST=pypi.tuna.tsinghua.edu.cn \
    PIP_TIMEOUT=300 \
    NPM_CONFIG_REGISTRY=https://registry.npmmirror.com

# 全局配置 pip 使用清华镜像并延长超时，避免单个 wheel 下载失败导致整个构建中断
RUN pip3 config set global.index-url https://pypi.tuna.tsinghua.edu.cn/simple && \
    pip3 config set global.trusted-host pypi.tuna.tsinghua.edu.cn && \
    pip3 config set global.timeout 300 && \
    pip3 config set global.retries 5

WORKDIR /app

# 1. 安装 Agent-Reach 本体（从 atomgit 浅克隆；只装核心依赖，
# 避免 [all] 里的 playwright / mcp 以及 [cookies] 里可能引发冲突的 browser-cookie3）
RUN git clone --depth 1 https://atomgit.com/Panniantong/Agent-Reach.git /opt/agent-reach && \
    pip3 install --break-system-packages --no-cache-dir \
      "agent-reach @ file:///opt/agent-reach" && \
    rm -rf /opt/agent-reach

# 2. 安装上游 CLI 工具
# bilibili-cli 来自 PyPI；npm 包走国内镜像。
RUN pip3 install --break-system-packages --no-cache-dir "bilibili-cli" "yt-dlp" && \
    npm install -g --no-cache mcporter @jackwener/opencli

# 3. 安装社交 CLI
# 这些工具已从 GitHub-only 的子模块改为从清华 PyPI 镜像直接安装，
# 避免国内网络无法访问 GitHub 导致构建失败。
RUN pip3 install --break-system-packages --no-cache-dir \
      "twitter-cli" "rdt-cli" "xiaohongshu-cli"

# 4. 安装模块 HTTP 服务依赖
COPY requirements.txt ./requirements.txt
RUN pip3 install --break-system-packages --no-cache-dir -r requirements.txt

# 5. 模块服务代码
COPY main.py ./main.py

# Cookie/数据持久化目录（通过 docker-compose volume 挂载到宿主机）
RUN mkdir -p /data/cookies && chmod 700 /data/cookies

EXPOSE 8080

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080"]
