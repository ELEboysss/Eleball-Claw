#!/usr/bin/env sh
# Eleball-claw 一键安装（Linux / macOS）
#
# 用法：
#   curl -fsSL https://eleball.cn/install.sh | sh
#   curl -fsSL https://eleball.cn/install.sh | sh -s -- --port=8090
#
# 安装到 /usr/local/bin/eleball-claw（需 sudo），或用户目录 ~/.eleball-claw/（无 sudo 时）。
# 下载对应架构二进制 -> 初始化配置 -> 提示启动命令。
set -eu

VERSION="${CLAW_VERSION:-latest}"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="${HOME}/.eleball-claw"
PORT="${PORT:-8090}"

# 解析 --port 参数
while [ $# -gt 0 ]; do
  case "$1" in
    --port=*) PORT="${1#--port=}" ;;
    --version=*) VERSION="${1#--version=}" ;;
  esac
  shift
done

# 探测架构
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "不支持的架构: $ARCH"; exit 1 ;;
esac

# 下载地址：默认走云端网关 release 端点，按架构下载（CLAW_DOWNLOAD_URL 可覆盖为镜像/内网）
ARCH_TAG="${OS}-${ARCH}"
BASE_URL="${CLAW_DOWNLOAD_URL:-https://api.eleball.cn/v1/releases/claw/download}"
URL="${BASE_URL}?arch=${ARCH_TAG}"
# 指定非 latest 版本时附加 version 参数（走网关版本路由）
if [ -n "$VERSION" ] && [ "$VERSION" != "latest" ]; then
  URL="${URL}&version=${VERSION}"
fi

echo "==> 下载 claw ($OS/$ARCH) from $URL"
TMP_BIN="$(mktemp)"
HEADER_FILE="$(mktemp)"
if command -v curl >/dev/null 2>&1; then
  curl -fsSL -D "$HEADER_FILE" "$URL" -o "$TMP_BIN"
else
  wget -qO "$TMP_BIN" "$URL"
fi
chmod +x "$TMP_BIN"

# 可选完整性校验：从响应头取 X-Content-SHA256，与本地 sha256 比对（无则跳过）
EXPECTED_SHA="$(grep -i 'X-Content-SHA256:' "$HEADER_FILE" | tr -d '\r' | sed 's/.*: *//')"
if [ -n "$EXPECTED_SHA" ] && command -v sha256sum >/dev/null 2>&1; then
  ACTUAL_SHA="$(sha256sum "$TMP_BIN" | awk '{print $1}')"
  if [ "$EXPECTED_SHA" != "$ACTUAL_SHA" ]; then
    echo "✗ 校验失败：SHA256 不匹配（期望 $EXPECTED_SHA，实际 $ACTUAL_SHA）"
    rm -f "$TMP_BIN" "$HEADER_FILE"
    exit 1
  fi
  echo "==> SHA256 校验通过"
fi
rm -f "$HEADER_FILE"

# 安装二进制
if [ -w "$INSTALL_DIR" ] || sudo -n true 2>/dev/null; then
  mv "$TMP_BIN" "$INSTALL_DIR/eleball-claw"
  BINARY="$INSTALL_DIR/eleball-claw"
else
  mkdir -p "$CONFIG_DIR/bin"
  mv "$TMP_BIN" "$CONFIG_DIR/bin/eleball-claw"
  BINARY="$CONFIG_DIR/bin/eleball-claw"
  echo "==> 无 sudo 权限，已安装到 $BINARY（建议加入 PATH）"
fi
chmod +x "$BINARY"

# 生成 JWT secret（heredoc 外预计算，避免命令替换嵌套）
CLAW_JWT_SECRET="$(openssl rand -hex 32 2>/dev/null || echo change-me-claw-secret)"

# 初始化配置
mkdir -p "$CONFIG_DIR/data"
if [ ! -f "$CONFIG_DIR/claw.yaml" ]; then
  cat > "$CONFIG_DIR/claw.yaml" <<YAML
server:
  port: ${PORT}
  mode: release
  eleagent_base_url: "https://api.eleball.cn/v1"
database:
  driver: sqlite
  dsn: "${CONFIG_DIR}/data/claw.db"
jwt:
  secret: "${CLAW_JWT_SECRET}"
  access_expire_hours: 2
  refresh_expire_hours: 720
agent:
  enabled: true
  base_path: "${CONFIG_DIR}/data/sessions"
  knowledge_base: "${CONFIG_DIR}/data/knowledge_base"
  model: "gpt-4o-mini"
  max_steps: 500
admin:
  enabled: false
admin_gate:
  enabled: false
payment:
  order_expire_minutes: 30
  alipay:
    enabled: false
mail:
  enabled: false
  port: 465
YAML
  echo "==> 已生成配置 ${CONFIG_DIR}/claw.yaml"
fi

echo ""
echo "✅ Eleball-claw 安装完成"
echo "   启动: CONFIG_PATH=${CONFIG_DIR}/claw.yaml eleball-claw serve --port=${PORT}"
echo "   首页: http://localhost:${PORT}"
echo "   配置: ${CONFIG_DIR}/claw.yaml"
echo "   模块: eleball-claw module ls   # 模块目录 ${CONFIG_DIR}/marketplace（首次使用自动播种官方模块）"
echo "         eleball-claw module up   # 经 docker 一键启动全部模块（需已安装 Docker）"
