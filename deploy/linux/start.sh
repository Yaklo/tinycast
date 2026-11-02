#!/usr/bin/env bash
# 解压后直接运行：./start.sh
#
# MediaMTX 与 TinyCast 都跑在前台会话里；按 Ctrl+C 会一起退出。
# 后台日志分别写到 logs/mediamtx.log 与 logs/tinycast.log。
set -euo pipefail
cd "$(dirname "$0")"

if [[ ! -x tinycast ]]; then
  echo "缺少 tinycast，请确认压缩包已完整解压。" >&2
  exit 1
fi
if [[ ! -x mediamtx/mediamtx ]]; then
  echo "缺少 mediamtx/mediamtx，请确认压缩包已完整解压。" >&2
  exit 1
fi
if [[ ! -f tinycast.env ]]; then
  echo "缺少 tinycast.env，请确认压缩包已完整解压。" >&2
  exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "缺少 curl，无法探测 MediaMTX 是否就绪；请先安装 curl。" >&2
  exit 1
fi

# MediaMTX 默认监听 1935（特权端口）。普通用户需要 root 授权或放宽
# net.ipv4.ip_unprivileged_port_start，否则 MediaMTX 会因 EACCES 直接退出。
if [[ "$(id -u)" -ne 0 ]]; then
  threshold=$(cat /proc/sys/net/ipv4/ip_unprivileged_port_start 2>/dev/null || echo 1024)
  if [[ "$threshold" =~ ^[0-9]+$ ]] && (( threshold > 1935 )); then
    echo "[WARN] 当前用户无法绑定 1935 端口：请用 sudo 运行，或执行" >&2
    echo "       sudo sysctl -w net.ipv4.ip_unprivileged_port_start=1024（写入 /etc/sysctl.conf 可持久化）。" >&2
    echo "       也可以改用 systemd 单元（已带 CAP_NET_BIND_SERVICE）。" >&2
  fi
fi

mkdir -p logs

# MediaMTX 自己不做日志滚动，超过 16 MB 就在启动时换一个文件，保留三份。
rotate_log() {
  local file=$1 max=$2 keep=$3
  [[ -f "$file" ]] || return 0
  local size
  size=$(stat -c%s "$file" 2>/dev/null || echo 0)
  (( size > max )) || return 0
  local i
  for ((i = keep; i >= 1; i--)); do
    [[ -f "$file.$i" ]] && mv -f "$file.$i" "$file.$((i + 1))"
  done
  mv -f "$file" "$file.1"
}
rotate_log logs/mediamtx.log $((16 * 1024 * 1024)) 3

echo "[MediaMTX] RTMP 1935 / HLS 8888 / WebRTC 8889 / 媒体 UDP 8000"
./mediamtx/mediamtx mediamtx/mediamtx.yml >>logs/mediamtx.log 2>&1 &
mtx=$!
cleanup() { kill "$mtx" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

echo "[MediaMTX] 等待控制接口就绪 ..."
ready=0
for _ in $(seq 1 15); do
  # 开了 api 鉴权时 401/403 也说明服务已经起来了，不能只看 curl 退出码。
  code=$(curl -sS -m 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:9997/v3/config/global/get 2>/dev/null || true)
  if [[ "$code" == "200" || "$code" == "401" || "$code" == "403" ]]; then
    ready=1
    break
  fi
  if ! kill -0 "$mtx" 2>/dev/null; then
    break
  fi
  sleep 1
done
if [[ "$ready" -ne 1 ]]; then
  echo "[ERROR] MediaMTX 未能在 15 秒内就绪，请检查 logs/mediamtx.log（端口 1935/8888/8889/9997 是否被占用）。" >&2
  tail -n 20 logs/mediamtx.log >&2 || true
  exit 1
fi

echo "[TinyCast] 浏览器打开 http://127.0.0.1:18855"
./tinycast -config tinycast.env -open
