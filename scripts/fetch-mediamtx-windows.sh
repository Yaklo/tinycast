#!/usr/bin/env bash
# 下载 MediaMTX 的 Windows 版本，放进 Windows 运行包（bin/windows-amd64/mediamtx/）。
#
# 用法： ./scripts/fetch-mediamtx-windows.sh
#        MEDIAMTX_VERSION=1.21.3 ./scripts/fetch-mediamtx-windows.sh
#
# 从 GitHub 下载可能很慢；也可以手动把 zip 放到 .tools/mediamtx/ 下，脚本会直接用缓存。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${MEDIAMTX_VERSION:-1.21.3}"
ASSET="mediamtx_v${VERSION}_windows_amd64.zip"
CACHE_DIR=".tools/mediamtx"
CACHE="${CACHE_DIR}/${ASSET}"
DEST="bin/windows-amd64/mediamtx"

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
import os, sys, zipfile

src, dest = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(src) as z:
    names = [n for n in z.namelist() if n.lower().endswith('.exe')]
    if not names:
        raise SystemExit('压缩包里没有 mediamtx.exe')
    for name in names:
        target = os.path.join(dest, os.path.basename(name))
        with z.open(name) as f, open(target, 'wb') as out:
            out.write(f.read())
        print('    ' + target)
PY

echo "==> 完成：$DEST"
