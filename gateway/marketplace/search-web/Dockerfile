# search-web 模块镜像（claw 内置联网搜索范例）
# 作为标准 Eleball Marketplace Module 运行，暴露 /health + /execute。
# claw 启动时扫描 marketplace/search-web/module.json 自动注册（官方预置，免拉镜像）。

FROM python:3.11-slim

WORKDIR /app

# 装 Node + mcporter 并注册 Exa（keyless，无需 API Key）
# Exa 搜索/web_read 经 mcporter 调用，与纯 Python 的 baidu/bing/searxng/duckduckgo 互补
ENV NPM_CONFIG_REGISTRY=https://registry.npmmirror.com
RUN apt-get update && apt-get install -y --no-install-recommends nodejs npm \
    && npm install -g mcporter \
    && mcporter config add exa https://mcp.exa.ai/mcp --scope home \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# 再装 Python 依赖（利用层缓存）
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# 再拷贝源码
COPY main.py .

ENV PORT=8091
EXPOSE 8091

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8091"]
