#!/usr/bin/env bash
# 开发模式：同时跑 Go 后端（:8080）与 Vite 开发服务器（:5173，/api 与 /rtc 代理到后端）。
# 首次使用需要两处依赖就绪：web/node_modules 与 Go 模块缓存。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [[ -f scripts/dev-env.sh ]]; then
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
fi

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

echo "==> 启动后端 http://127.0.0.1:8080"
go run ./cmd/tinycast &
SERVER_PID=$!

echo "==> 启动前端 http://127.0.0.1:5173"
(cd web && pnpm dev)

wait
