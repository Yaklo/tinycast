#!/usr/bin/env bash
# 解压后直接运行：./start.sh
#
# MediaMTX 与 TinyCast 都跑在前台会话里；按 Ctrl+C 会一起退出。
# 后台日志分别写到 logs/mediamtx.log 与 logs/tinycast.log。
set -euo pipefail
cd "$(dirname "$0")"

if [[ ! -x mediamtx/mediamtx ]]; then
  echo "缺少 mediamtx/mediamtx，请确认压缩包已完整解压。" >&2
  exit 1
fi
if [[ ! -f tinycast.env ]]; then
  echo "缺少 tinycast.env，请确认压缩包已完整解压。" >&2
  exit 1
fi

mkdir -p logs

echo "[MediaMTX] RTMP 1935 / HLS 8888 / WebRTC 8889 / 媒体 UDP 8000"
./mediamtx/mediamtx mediamtx/mediamtx.yml >>logs/mediamtx.log 2>&1 &
mtx=$!
cleanup() { kill "$mtx" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

sleep 1
echo "[TinyCast] 浏览器打开 http://127.0.0.1:18855"
./tinycast -config tinycast.env -open
