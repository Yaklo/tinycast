#!/usr/bin/env bash
# 下载 MediaMTX 的 Linux 版本，放进 Linux 运行包（bin/linux-<arch>/mediamtx/）。
#
# 用法： ./scripts/fetch-mediamtx-linux.sh
#        GOARCH=arm64 ./scripts/fetch-mediamtx-linux.sh
#        MEDIAMTX_VERSION=1.21.3 ./scripts/fetch-mediamtx-linux.sh
#
# 从 GitHub 下载可能很慢；也可以手动把 tar.gz 放到 .tools/mediamtx/ 下，脚本会直接用缓存。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${MEDIAMTX_VERSION:-1.21.3}"
ARCH="${ARCH:-${GOARCH:-amd64}}"
ASSET="mediamtx_v${VERSION}_linux_${ARCH}.tar.gz"
CACHE_DIR=".tools/mediamtx"
CACHE="$CACHE_DIR/$ASSET"
DEST="${DEST:-bin/linux-$ARCH/mediamtx}"

mkdir -p "$CACHE_DIR" "$DEST"

if [[ ! -f "$CACHE" ]]; then
  URL="https://github.com/bluenviron/mediamtx/releases/download/v${VERSION}/${ASSET}"
  echo "==> 下载 $URL"
  ok=0
  for attempt in $(seq 1 12); do
    if curl -fsSL -m 300 -C - -o "${CACHE}.part" "$URL"; then
      mv "${CACHE}.part" "$CACHE"
      ok=1
      break
    fi
    echo "    第 $attempt 次失败，重试…"
    sleep 3
  done
  if [[ "$ok" != "1" ]]; then
    echo "下载失败：请手动下载 ${ASSET} 放到 ${CACHE} 后重跑本脚本。" >&2
    exit 1
  fi
fi

echo "==> 解压 $CACHE"
python3 - "$CACHE" "$DEST" <<'PY'
import os, sys, tarfile

src, dest = sys.argv[1], sys.argv[2]
with tarfile.open(src, 'r:gz') as tf:
    members = tf.getmembers()
    member = next((m for m in members if os.path.basename(m.name) == 'mediamtx' and m.isfile()), None)
    if member is None:
        raise SystemExit('压缩包里没有 mediamtx 可执行文件')
    target = os.path.join(dest, 'mediamtx')
    with tf.extractfile(member) as f, open(target, 'wb') as out:
        out.write(f.read())
    os.chmod(target, 0o755)
    print('    ' + target)
    # MIT 要求分发的每一份副本都带版权与许可声明
    lic = next((m for m in members if os.path.basename(m.name).lower() == 'license' and m.isfile()), None)
    if lic is None:
        raise SystemExit('压缩包里没有 LICENSE')
    lic_target = os.path.join(dest, 'LICENSE')
    with tf.extractfile(lic) as f, open(lic_target, 'wb') as out:
        out.write(f.read())
    print('    ' + lic_target)
PY

echo "==> 完成：$DEST"
