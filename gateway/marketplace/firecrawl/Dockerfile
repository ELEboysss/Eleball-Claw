# Eleball Marketplace Skill: Firecrawl（claw 宿主机模式，build context 为本目录）
# 轻量 Docker MCP HTTP 模块（仅 Python stdlib，无 pip 依赖），cloud + claw 双端同构。
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/library/python:3.12-slim

WORKDIR /app

COPY main.py ./main.py

EXPOSE 8080

# stdlib http.server，无框架依赖；容器内监听 0.0.0.0:8080
CMD ["python", "main.py"]
