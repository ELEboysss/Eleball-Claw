#!/usr/bin/env bash
# Eleball 集市秘技模块一键启动脚本
# 自动发现 marketplace/ 下所有子目录中的 docker-compose.yml 并统一管理。
#
# 用法：
#   cd gateway/marketplace
#   ./start.sh up     # 构建并启动所有模块（默认）
#   ./start.sh down   # 停止并移除所有模块
#   ./start.sh logs   # 查看所有模块日志
#   ./start.sh ps     # 查看所有模块运行状态

set -e

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
cd "$SCRIPT_DIR"

# 加载 gateway/deployments/.env（如果存在），与网关使用同一套环境变量
DEPLOY_ENV="$SCRIPT_DIR/../deployments/.env"
if [ -f "$DEPLOY_ENV" ]; then
  echo "[INFO] 加载环境变量: $DEPLOY_ENV"
  export $(grep -v '^#' "$DEPLOY_ENV" | grep -v '^$' | xargs)
else
  echo "[INFO] 未找到 $DEPLOY_ENV，使用模块默认值"
fi

# 确保与 gateway 共用的外部网络存在
# 模块可独立于 gateway 启动，但必须加入同一个 eleball-net 才能被网关解析
docker network inspect eleball-net >/dev/null 2>&1 || docker network create eleball-net

# 自动发现所有子目录下的 docker-compose.yml
COMPOSE_FILES=()
while IFS= read -r file; do
  COMPOSE_FILES+=("$file")
done < <(find . -maxdepth 2 -type f -name 'docker-compose.yml' | sort)

if [ ${#COMPOSE_FILES[@]} -eq 0 ]; then
  echo "[WARN] 未在 marketplace/ 下发现任何 docker-compose.yml"
  exit 0
fi

echo "[INFO] 发现 ${#COMPOSE_FILES[@]} 个模块："
for f in "${COMPOSE_FILES[@]}"; do
  echo "  - $f"
done

# 构造 docker compose 文件参数
COMPOSE_ARGS=(-p eleball-marketplace)
for f in "${COMPOSE_FILES[@]}"; do
  COMPOSE_ARGS+=(-f "$f")
done

CMD="${1:-up}"

case "$CMD" in
  up)
    echo "[INFO] 构建并启动所有模块..."
    docker compose "${COMPOSE_ARGS[@]}" up -d --build
    echo "[INFO] 模块启动完成"
    docker compose "${COMPOSE_ARGS[@]}" ps
    ;;
  down)
    echo "[INFO] 停止并移除所有模块..."
    docker compose "${COMPOSE_ARGS[@]}" down
    ;;
  logs)
    echo "[INFO] 查看所有模块日志..."
    docker compose "${COMPOSE_ARGS[@]}" logs -f
    ;;
  ps)
    docker compose "${COMPOSE_ARGS[@]}" ps
    ;;
  *)
    echo "未知命令: $CMD"
    echo "用法: $0 [up|down|logs|ps]"
    exit 1
    ;;
esac
