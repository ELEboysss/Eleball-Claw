#!/usr/bin/env bash
set -e

cd "$(dirname "$0")"

export PORT="${PORT:-8080}"

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║        Eleball E2E 本地测试服务器启动脚本                ║"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  编译中...                                               ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

if ! go build -o e2e-server .; then
    echo "[错误] 编译失败，请检查 Go 环境"
    exit 1
fi

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║        Eleball E2E 本地测试服务器已启动                  ║"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  API 地址:    http://localhost:${PORT}                   ║"
echo "║  Health:      http://localhost:${PORT}/health            ║"
echo "║  Admin Web:   http://localhost:${PORT}/admin/            ║"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  Android 调试指南:                                       ║"
echo "║    模拟器:    http://10.0.2.2:${PORT}                    ║"
echo "║    真机:      http://$(hostname -I | awk '{print $1}'):${PORT}  ║"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  环境变量:                                               ║"
echo "║    OPENAI_API_KEY     - OpenAI 代理 Key                 ║"
echo "║    DEEPSEEK_API_KEY   - DeepSeek 代理 Key               ║"
echo "║    MOONSHOT_API_KEY   - Moonshot 代理 Key               ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
echo "按 Ctrl+C 停止服务"
echo ""

./e2e-server
