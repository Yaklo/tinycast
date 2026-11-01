#!/usr/bin/env bash
# 构建 TinyCast：先构建前端（产物进入 internal/webui/dist 并被 go:embed 内嵌），再构建 Go 二进制。
#
# 用法：
#   ./scripts/build.sh                # 构建本机平台到 bin/tinycast
#   GOOS=linux GOARCH=amd64 ./scripts/build.sh   # 交叉编译
#   SKIP_WEB=1 ./scripts/build.sh     # 只构建后端（已有前端产物时）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p bin

if [[ "${SKIP_WEB:-0}" != "1" ]]; then
  echo "==> 构建前端"
  if [[ ! -d web/node_modules ]]; then
    if command -v pnpm >/dev/null 2>&1; then
      (cd web && pnpm install --frozen-lockfile)
    else
      echo "未找到 pnpm，请先安装：corepack enable && corepack prepare pnpm@latest --activate" >&2
      exit 1
    fi
  fi
  (cd web && pnpm build)
  # vite 的 emptyOutDir 会清空目录，这里补回占位文件保证 embed 模式始终可用
  mkdir -p internal/webui/dist
  touch internal/webui/dist/.gitkeep
fi

echo "==> 构建后端 ${GOOS:-本机}/${GOARCH:-本机}"
OUT="bin/tinycast"
if [[ "${GOOS:-}" == "windows" ]]; then
  OUT="bin/tinycast.exe"
fi
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$OUT" ./cmd/tinycast

echo "==> 完成：$OUT"
ls -lh "$OUT"
