#!/usr/bin/env bash
# 一键产出 Windows 运行包：构建 tinycast.exe、准备 MediaMTX（Windows 版）、打成 zip。
#
# 用法：./scripts/package-windows.sh            # 首次会联网下载 MediaMTX（约 30MB）
#       SKIP_WEB=1 ./scripts/package-windows.sh # 前端产物已存在时跳过前端构建
# 产物：bin/tinycast-windows-amd64.zip（解压即用，目录结构见 deploy/windows/部署说明.txt）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# 仓库内自带工具链时优先使用（$HOME 不可写的环境）
if [[ -x ".tools/go/bin/go" ]]; then
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
fi

OUTDIR="bin/windows-amd64"
STAGE=".tools/winpkg-stage"
ZIP="bin/tinycast-windows-amd64.zip"

if [[ ! -f "$OUTDIR/mediamtx/mediamtx.exe" ]]; then
  echo "==> 首次构建，先获取 MediaMTX 的 Windows 版本"
  ./scripts/fetch-mediamtx-windows.sh
fi

# 目标目录里的 tinycast.exe 可能正在 Windows 上运行，此时删除会返回 Input/output error。
# 这种情况改为在临时目录里构建，只更新 zip，不去动用户正在用的目录。
WIN_OUTDIR="$OUTDIR"
if [[ -e "$OUTDIR/tinycast.exe" ]] && ! rm -f "$OUTDIR/tinycast.exe" 2>/dev/null; then
  echo "==> $OUTDIR/tinycast.exe 正在被占用（可能还在运行），改为在 $STAGE 构建"
  WIN_OUTDIR="$STAGE"
  rm -rf "$STAGE"
  mkdir -p "$STAGE/mediamtx"
  cp "$OUTDIR/mediamtx/mediamtx.exe" "$STAGE/mediamtx/mediamtx.exe"
fi

WIN_OUTDIR="$WIN_OUTDIR" ./scripts/build.sh --windows

# 运行时日志不进压缩包（启动脚本会自己创建 logs 目录）
rm -rf "$WIN_OUTDIR/logs"

echo "==> 打包 $ZIP"
python3 - "$WIN_OUTDIR" "$ZIP" <<'PY'
import os, sys, zipfile

src, out = sys.argv[1], sys.argv[2]
os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as z:
    for base, dirs, files in os.walk(src):
        dirs.sort()
        for name in sorted(files):
            # 运行期残留的 pid 文件与日志一样不进包
            if name.endswith((".pid", ".id", ".log")):
                continue
            full = os.path.join(base, name)
            z.write(full, os.path.relpath(full, src))
PY

echo "==> 完成：$ZIP"
ls -lh "$ZIP"
python3 - "$ZIP" <<'PY'
import sys, zipfile
with zipfile.ZipFile(sys.argv[1]) as z:
    for info in z.infolist():
        if not info.is_dir():
            print(f"    {info.file_size:>9}  {info.filename}")
PY
