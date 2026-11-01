#!/usr/bin/env bash
# 构建 TinyCast：先构建前端（产物进入 internal/webui/dist 并被 go:embed 内嵌），再构建 Go 二进制。
#
# 用法：
#   ./scripts/build.sh                       # 构建本机平台到 bin/
#   ./scripts/build.sh --windows             # 交叉编译 Windows 生产运行包到 bin/windows-amd64/
#   GOOS=linux GOARCH=arm64 ./scripts/build.sh  # 其它平台交叉编译
#   SKIP_WEB=1 ./scripts/build.sh            # 只构建后端（前端产物已存在时）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

for arg in "$@"; do
  case "$arg" in
    --windows) export GOOS=windows GOARCH=amd64 ;;
    -h | --help)
      sed -n '2,8p' "${BASH_SOURCE[0]}"
      exit 0
      ;;
    *)
      echo "未知参数：$arg" >&2
      exit 2
      ;;
  esac
done

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

SUFFIX=""
OUTDIR="bin"
if [[ "${GOOS:-}" == "windows" ]]; then
  SUFFIX=".exe"
  OUTDIR="bin/windows-amd64"
  mkdir -p "$OUTDIR"
  # 清掉上一次生成的顶层文件；srs/ 与 logs/ 原样保留，避免重复下载 42MB 的 SRS
  rm -f "$OUTDIR"/*.exe "$OUTDIR"/*.cmd "$OUTDIR"/*.txt "$OUTDIR"/*.env
fi

echo "==> 构建后端 ${GOOS:-本机}/${GOARCH:-本机}"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$OUTDIR/tinycast$SUFFIX" ./cmd/tinycast

if [[ "$OUTDIR" == "bin/windows-amd64" ]]; then
  # Windows 生产运行包：可执行文件 + 配置文件 + 启动脚本 + 说明，SRS 由 fetch-srs-windows.sh 放入 srs/
  cp deploy/windows/tinycast.env deploy/windows/start.cmd deploy/windows/start-all.cmd \
    deploy/windows/部署说明.txt "$OUTDIR/"
  mkdir -p "$OUTDIR/logs" "$OUTDIR/srs/conf"
  if [[ -f deploy/srs/srs.windows.conf ]]; then
    cp deploy/srs/srs.windows.conf "$OUTDIR/srs/conf/srs.conf"
  fi
  if [[ ! -f "$OUTDIR/srs/objs/srs.exe" ]]; then
    echo "提示：尚未放入 SRS，运行 ./scripts/fetch-srs-windows.sh 之后才可做真实推流"
  fi

  # 模拟回调的联调工具不进生产包，单独放在 bin/dev 下备用
  echo "==> 构建联调工具 tinysim"
  mkdir -p bin/dev
  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "bin/dev/tinysim$SUFFIX" ./cmd/tinysim
else
  echo "==> 构建联调工具 tinysim"
  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$OUTDIR/tinysim$SUFFIX" ./cmd/tinysim
fi

echo "==> 完成：$OUTDIR"
ls -lh "$OUTDIR"
