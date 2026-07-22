#!/usr/bin/env bash
# claw 本地开发一键启动（从源码编译运行，无需 CDN 下载）
#
# 用法：
#   ./dev-run.sh                 # 默认端口 8090
#   ./dev-run.sh --port=18090    # 指定端口
#   ./dev-run.sh 18090           # 端口简写
#
# 启动后自动验证 /health，Ctrl-C 停止 claw。
# 可选环境变量（启动前 export）：JWT_SECRET / RELAY_URL / CLAW_RELAY_TOKEN / CLAW_DEVICE_ID
set -eu

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"          # 主仓库根（.tools/go 所在）
GATEWAY="$SCRIPT_DIR/gateway"
GO="$REPO_ROOT/.tools/go/bin/go"

# 端口解析
PORT=8090
arg="${1:-}"
if [[ "$arg" == --port=* ]]; then PORT="${arg#--port=}"; elif [[ "$arg" =~ ^[0-9]+$ ]]; then PORT="$arg"; fi

# Go 环境（缓存指向项目盘，避免 C 盘空间不足）
export TMP="$REPO_ROOT/.tools/tmp" TEMP="$REPO_ROOT/.tools/tmp"
export GOMODCACHE="$REPO_ROOT/.tools/gomodcache" GOCACHE="$REPO_ROOT/.tools/gocache"
export GOPROXY=https://goproxy.cn,direct

if [[ ! -x "$GO" ]]; then
  echo "✗ 未找到 Go: $GO（请确认主仓库 .tools/go 已就位）" >&2
  exit 1
fi

mkdir -p "$GATEWAY/data"
cd "$GATEWAY"

LOG="/tmp/claw-dev.log"
echo "▶ 编译并启动 claw-server (端口 $PORT)..."
echo "  /health: http://localhost:$PORT/health"
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
