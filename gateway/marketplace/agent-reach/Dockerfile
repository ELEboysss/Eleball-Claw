# Eleball Marketplace Skill: Agent-Reach
# 作为独立容器运行，暴露 HTTP 接口供网关调用，实现集市秘技的模块化部署
#
# 构建说明：
# 本 Dockerfile 假设 build context 为项目根目录（即 Eleball 仓库根目录），
# 这样可以直接使用本地子模块 providers/agent-reach，避免构建时访问 GitHub。
# 社交 CLI（twitter-cli / rdt-cli / xiaohongshu-cli）从清华 PyPI 镜像安装，无需访问 GitHub。
# 推荐在项目根目录执行：
#   docker build -f gateway/marketplace/agent-reach/Dockerfile -t eleball-agent-reach .
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

# 1. 从本地子模块安装 Agent-Reach 本体
# 使用本地代码避免从 GitHub clone，显著提升构建速度并保证版本一致。
# 只安装核心依赖，避免 [all] 里的 playwright / mcp 以及 [cookies] 里可能引发冲突的 browser-cookie3。
COPY providers/agent-reach /opt/agent-reach
RUN if [ ! -f /opt/agent-reach/pyproject.toml ]; then \
      echo "本地 providers/agent-reach 为空，回退到 atomgit clone..." && \
      rm -rf /opt/agent-reach && \
      git clone --depth 1 https://atomgit.com/Panniantong/Agent-Reach.git /opt/agent-reach; \
    fi && \
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
COPY gateway/marketplace/agent-reach/requirements.txt ./requirements.txt
RUN pip3 install --break-system-packages --no-cache-dir -r requirements.txt

# 5. 模块服务代码
COPY gateway/marketplace/agent-reach/main.py ./main.py

# Cookie/数据持久化目录（通过 docker-compose volume 挂载到宿主机）
RUN mkdir -p /data/cookies && chmod 700 /data/cookies

EXPOSE 8080

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080"]
