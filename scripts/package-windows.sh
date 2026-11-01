#!/usr/bin/env bash
# 一键产出 Windows 生产运行包：构建 tinycast.exe、准备 SRS 6（含 Cygwin 运行库）、打成 zip。
#
# 用法：./scripts/package-windows.sh            # 需要联网下载 SRS（首次约 16MB）
#       SKIP_WEB=1 ./scripts/package-windows.sh # 前端产物已存在时跳过前端构建
# 产物：bin/tinycast-windows-amd64.zip（解压即用，目录结构见 deploy/windows/部署说明.txt）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# 仓库内自带工具链时优先使用（$HOME 不可写的环境）
if [[ -x ".tools/go/bin/go" ]]; then
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
fi

OUTDIR="bin/windows-amd64"
ZIP="bin/tinycast-windows-amd64.zip"

if [[ ! -f "$OUTDIR/srs/objs/srs.exe" ]]; then
  echo "==> 首次构建，先获取 SRS 6 与 Cygwin 运行库"
  ./scripts/fetch-srs-windows.sh
fi

./scripts/build.sh --windows

# 运行时日志不进压缩包（启动脚本会自己创建 logs 目录）
rm -rf "$OUTDIR/logs" "$OUTDIR/srs/logs"

echo "==> 打包 $ZIP"
python3 - "$OUTDIR" "$ZIP" <<'PY'
import os, sys, zipfile

src, out = sys.argv[1], sys.argv[2]
os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as z:
    for base, dirs, files in os.walk(src):
        dirs.sort()
        for name in sorted(files):
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
