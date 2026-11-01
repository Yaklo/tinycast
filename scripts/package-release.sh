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
rm -f "$DIST"/SHA256SUMS

echo "==> 发布版本 $VERSION（commit $COMMIT，构建于 $BUILD_TIME）"
./scripts/package-linux.sh
./scripts/package-windows.sh

echo "==> 生成校验和"
( cd "$DIST" && sha256sum ./* > SHA256SUMS )
cat "$DIST/SHA256SUMS"
