# search-web 模块镜像（claw 内置联网搜索范例）
# 作为标准 Eleball Marketplace Module 运行，暴露 /health + /execute。
# claw 启动时扫描 marketplace/search-web/module.json 自动注册（官方预置，免拉镜像）。

FROM python:3.11-slim

WORKDIR /app

# 先装依赖（利用层缓存）
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# 再拷贝源码
COPY main.py .

ENV PORT=8091
EXPOSE 8091

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8091"]
