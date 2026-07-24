#!/usr/bin/env bash
# claw 本地开发一键启动（从源码编译运行，无需 CDN 下载）
#
# 用法：
#   ./dev-run.sh                       # 默认端口 8090，构建前端+启动（访问 :8090 见页面）
#   ./dev-run.sh --no-build-web        # 跳过前端构建（仅 API，dev 跑 vite :5173）
#   ./dev-run.sh --port=18090
#   ./dev-run.sh 18090                  # 端口简写
#
# 启动后自动验证 /health，Ctrl-C 停止 claw。
# 可选环境变量（启动前 export）：JWT_SECRET / RELAY_URL / CLAW_RELAY_TOKEN / CLAW_DEVICE_ID
set -eu

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"          # 主仓库根（.tools/go 所在）
GATEWAY="$SCRIPT_DIR/gateway"
GO="$REPO_ROOT/.tools/go/bin/go"

# 参数解析：默认构建前端，--no-build-web 跳过
PORT=8090
BUILD_WEB=1
while [[ $# -gt 0 ]]; do
  case "$1" in
    --port=*) PORT="${1#--port=}";;
    --build-web) BUILD_WEB=1;;
    --no-build-web) BUILD_WEB=0;;
    [0-9]*) PORT="$1";;
  esac
  shift
done

# Go 环境（缓存指向项目盘，避免 C 盘空间不足）
export TMP="$REPO_ROOT/.tools/tmp" TEMP="$REPO_ROOT/.tools/tmp"
export GOMODCACHE="$REPO_ROOT/.tools/gomodcache" GOCACHE="$REPO_ROOT/.tools/gocache"
export GOPROXY=https://goproxy.cn,direct

if [[ ! -x "$GO" ]]; then
  echo "✗ 未找到 Go: $GO（请确认主仓库 .tools/go 已就位）" >&2
  exit 1
fi

# build_dist：构建单个前端 dist（web 或 admin-web）
# $1=前端目录 $2=展示名 $3=主仓库对应 node_modules（junction 源）
build_dist() {
  local dir="$1" name="$2" main_node="$3"
  echo "▶ 构建 $name dist..."
  if [[ ! -d "$dir/node_modules" ]]; then
    if [[ -d "$main_node" ]]; then
      # Git Bash 路径 /j/... 需转 Windows 路径 J:/... 给 PowerShell junction
      local dir_win main_win
      dir_win=$(cygpath -m "$dir" 2>/dev/null || echo "$dir")
      main_win=$(cygpath -m "$main_node" 2>/dev/null || echo "$main_node")
      echo "  链接 node_modules <- $main_node"
      powershell -NoProfile -Command "New-Item -ItemType Junction -Path '${dir_win}/node_modules' -Target '${main_win}' -ErrorAction SilentlyContinue | Out-Null" 2>/dev/null || true
      # 非 Windows（无 cygpath）用符号链接
      if [[ ! -d "$dir/node_modules" ]] && command -v ln >/dev/null 2>&1; then
        ln -s "$main_node" "$dir/node_modules" 2>/dev/null || true
      fi
    else
      echo "  ✗ $dir/node_modules 不存在且主仓库无（$main_node），请 cd $dir && npm install" >&2
      return 1
    fi
  fi
  ( cd "$dir" && MSYS_NO_PATHCONV=1 VITE_API_BASE=/v1 VITE_CLOUD_BASE=https://www.eleball.cn VITE_CLOUD_API=https://api.eleball.cn/v1 \
      node ./node_modules/vite/bin/vite.js build ) || { echo "✗ $name build 失败" >&2; return 1; }
}

if [[ "$BUILD_WEB" == 1 ]]; then
  build_dist "$GATEWAY/web" "web" "$REPO_ROOT/gateway/web/node_modules" || exit 1
  build_dist "$GATEWAY/admin-web" "admin-web" "$REPO_ROOT/gateway/admin-web/node_modules" || exit 1
  echo "✓ 前端 dist 构建完成，claw-server 将 serve 页面"
else
  echo "ℹ 跳过前端构建（--no-build-web）；访问 :5173 跑 vite dev 或无 dist 时 / 返回构建提示"
fi

mkdir -p "$GATEWAY/data"
cd "$GATEWAY"

LOG="/tmp/claw-dev.log"
echo "▶ 编译并启动 claw-server (端口 $PORT)..."
echo "  /health: http://localhost:$PORT/health"
[[ "$BUILD_WEB" == 1 ]] && echo "  页面: http://localhost:$PORT （web）/ http://localhost:$PORT/admin （控制台）"
echo "  日志: tail -f $LOG"

"$GO" run ./cmd/claw-server serve --port="$PORT" > "$LOG" 2>&1 &
PID=$!
cleanup() { kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap cleanup INT TERM EXIT

# 等待启动 + 验证 /health（最多 20s）
echo "⏳ 等待启动..."
for i in $(seq 1 20); do
  sleep 1
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "✗ claw 进程已退出，日志：" >&2
    tail -20 "$LOG" >&2
    exit 1
  fi
  if curl -sf "http://localhost:$PORT/health" >/dev/null 2>&1; then
    echo "✅ claw 已启动: http://localhost:$PORT/health"
    curl -s "http://localhost:$PORT/health"
    echo ""
    echo "💡 Ctrl-C 停止。可选 env: JWT_SECRET / RELAY_URL / CLAW_RELAY_TOKEN / CLAW_DEVICE_ID"
    wait "$PID"
    exit 0
  fi
done
echo "✗ 启动超时（20s），日志：" >&2
tail -20 "$LOG" >&2
exit 1
