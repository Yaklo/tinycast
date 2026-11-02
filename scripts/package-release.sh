#!/usr/bin/env bash
# 一键产出一次 release 的全部可下载文件（Linux + Windows），并生成校验和。
#
# 用法： ./scripts/package-release.sh
#        SKIP_WEB=1 ./scripts/package-release.sh   # 前端产物已存在时跳过前端构建
# 产物： dist/tinycast-<版本>-linux-amd64.tar.gz
#        dist/tinycast-<版本>-windows-amd64.zip
#        dist/SHA256SUMS
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ -x ".tools/go/bin/go" ]]; then
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
fi

# shellcheck disable=SC1091
source scripts/release-env.sh

export DIST="${DIST:-dist}"
mkdir -p "$DIST"
# 只清理上一轮的发布包，避免历史产物混进本次 SHA256SUMS。
# 只删 $DIST 下这两类产物（以及上次的校验和），不碰其它文件。
rm -f "$DIST"/SHA256SUMS "$DIST"/SHA256SUMS.tmp \
  "$DIST"/tinycast-*-linux-*.tar.gz "$DIST"/tinycast-*-windows-*.zip

echo "==> 发布版本 $VERSION（commit $COMMIT，构建于 $BUILD_TIME）"
./scripts/package-linux.sh
./scripts/package-windows.sh

echo "==> 生成校验和"
LINUX_PKG="tinycast-${VERSION}-linux-${GOARCH:-amd64}.tar.gz"
WINDOWS_PKG="tinycast-${VERSION}-windows-amd64.zip"
for pkg in "$LINUX_PKG" "$WINDOWS_PKG"; do
  if [[ ! -s "$DIST/$pkg" ]]; then
    echo "缺少产物：$DIST/$pkg（打包步骤没有生成？）" >&2
    exit 1
  fi
done
# SHA256SUMS 只包含本次的两个产物；先写临时文件再改名，避免中途失败留下半份校验和。
( cd "$DIST" && sha256sum "$LINUX_PKG" "$WINDOWS_PKG" > SHA256SUMS.tmp && mv -f SHA256SUMS.tmp SHA256SUMS )
cat "$DIST/SHA256SUMS"
