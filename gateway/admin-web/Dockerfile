# 构建阶段
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/library/node:20-alpine AS builder

WORKDIR /app

# API Base URL，构建时可通过 --build-arg VITE_API_BASE=/xxx 覆盖
ARG VITE_API_BASE=/admin/api
ENV VITE_API_BASE=${VITE_API_BASE}

# 复制依赖文件并安装（利用缓存）
COPY package.json package-lock.json ./
RUN npm ci

# 复制源码并构建
COPY . .
RUN npm run build

# 运行阶段：使用 Nginx 托管静态资源
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/library/nginx:alpine

# 安装 envsubst 工具（部分 nginx:alpine 镜像已内置，显式确保可用）
RUN apk add --no-cache gettext

# 复制构建产物
COPY --from=builder /app/dist /usr/share/nginx/html/admin

# 复制 Nginx 配置到主配置与 conf.d 两处，确保生效
COPY nginx.conf /etc/nginx/nginx.conf
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

# 支持通过环境变量 GATEWAY_HOST 指定后端地址（默认 gateway:8080）
ENV GATEWAY_HOST=gateway:8080
CMD ["/bin/sh", "-c", "envsubst '$GATEWAY_HOST' < /etc/nginx/nginx.conf > /etc/nginx/nginx.conf.tmp && mv /etc/nginx/nginx.conf.tmp /etc/nginx/nginx.conf && envsubst '$GATEWAY_HOST' < /etc/nginx/conf.d/default.conf > /etc/nginx/conf.d/default.conf.tmp && mv /etc/nginx/conf.d/default.conf.tmp /etc/nginx/conf.d/default.conf && echo '=== nginx.conf ===' && cat /etc/nginx/nginx.conf && nginx -g 'daemon off;'"]
