#!/usr/bin/env bash
# 产出一个可以直接解压运行的 Linux 包：tinycast + MediaMTX + 配置样例 + systemd 单元。
#
# 用法： ./scripts/package-linux.sh              # 当前架构（默认 amd64）
#        GOARCH=arm64 ./scripts/package-linux.sh # 交叉编译 arm64 包
#        SKIP_WEB=1 ./scripts/package-linux.sh   # 前端产物已存在时跳过前端构建
# 产物：dist/tinycast-<版本>-linux-<arch>.tar.gz
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ -x ".tools/go/bin/go" ]]; then
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
fi

# shellcheck disable=SC1091
source scripts/release-env.sh

ARCH="${GOARCH:-amd64}"
OUTDIR="bin/linux-$ARCH"
DIST="${DIST:-dist}"
NAME="tinycast-${VERSION}-linux-${ARCH}"
STAGE=".tools/linuxpkg-stage/$NAME"

if [[ ! -x "$OUTDIR/mediamtx/mediamtx" ]]; then
  echo "==> 首次构建，先获取 MediaMTX 的 Linux 版本"
  ARCH="$ARCH" DEST="$OUTDIR/mediamtx" ./scripts/fetch-mediamtx-linux.sh
fi

OUTDIR="$OUTDIR" ./scripts/build.sh

echo "==> 组装 $NAME"
rm -rf "$STAGE"
mkdir -p "$STAGE/mediamtx" "$STAGE/systemd"
cp "$OUTDIR/tinycast" "$STAGE/tinycast"
cp deploy/linux/tinycast.env "$STAGE/tinycast.env"
cp deploy/linux/start.sh "$STAGE/start.sh"
cp deploy/mediamtx/mediamtx.yml "$STAGE/mediamtx/mediamtx.yml"
cp "$OUTDIR/mediamtx/mediamtx" "$STAGE/mediamtx/mediamtx"
cp deploy/systemd/tinycast.service deploy/systemd/mediamtx.service "$STAGE/systemd/"
chmod +x "$STAGE/tinycast" "$STAGE/start.sh" "$STAGE/mediamtx/mediamtx"

cat > "$STAGE/version.txt" <<EOF
version:   $VERSION
commit:    $COMMIT
buildTime: $BUILD_TIME
go:        $(go version | awk '{print $3}')
EOF

mkdir -p "$DIST"
EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct 2>/dev/null || date +%s)}"
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$EPOCH" \
  --mode='u=rwX,go=rX' \
  -C .tools/linuxpkg-stage -cf - "$NAME" | gzip -9n > "$DIST/$NAME.tar.gz"

echo "==> 完成：$DIST/$NAME.tar.gz"
ls -lh "$DIST/$NAME.tar.gz"
tar -tzf "$DIST/$NAME.tar.gz"
