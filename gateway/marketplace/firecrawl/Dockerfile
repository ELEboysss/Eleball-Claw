# Eleball Marketplace Skill: Firecrawl
# 默认使用国内镜像拉取 Python 基础镜像，避免 Docker Hub 超时。
# 可通过 build arg 覆盖：docker build --build-arg PYTHON_BASE_IMAGE=python:3.12-slim ...
ARG PYTHON_BASE_IMAGE=swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/library/python:3.12-slim
FROM ${PYTHON_BASE_IMAGE}

WORKDIR /app

# 使用国内 PyPI 镜像并延长超时，避免官方源下载超时导致构建失败
ENV PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple \
    PIP_TRUSTED_HOST=pypi.tuna.tsinghua.edu.cn \
    PIP_TIMEOUT=300 \
    PIP_RETRIES=5

# build context 为项目根目录，COPY 路径需相对项目根目录
COPY gateway/marketplace/firecrawl/requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY gateway/marketplace/firecrawl/main.py ./

ENV PORT=8080
EXPOSE 8080

CMD ["python", "main.py"]
