#!/usr/bin/env bash
# 下载 SRS 6 官方 Windows 构建（Cygwin 版），解出 srs.exe 与依赖 DLL，
# 放到 bin/windows-amd64/srs/ ，供 Windows 侧与 tinycast 一起做真实 OBS 推流测试。
#
# 用法：./scripts/fetch-srs-windows.sh
# 产物（不入库，体积约 42MB）：
#   bin/windows-amd64/srs/objs/srs.exe
#   bin/windows-amd64/srs/objs/cygwin1.dll
#   bin/windows-amd64/srs/objs/cyggcc_s-seh-1.dll
#   bin/windows-amd64/srs/objs/cygstdc++-6.dll
#   bin/windows-amd64/srs/conf/srs.conf      （来自 deploy/srs/srs.windows.conf）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="v6.0-b0"                                       # 6.0 的正式发布，内含 SRS 6.0.172
ASSET="SRS-Windows-x86_64-6.0-b0-setup.exe"
SHA256="d0af39202c8ad224f06549cd9ed7d8b6f87c5569ed9bde53216b2276772bcbdc"
URL="https://github.com/ossrs/srs/releases/download/${VERSION}/${ASSET}"
SEVENZIP_URL="https://www.7-zip.org/a/7z2301-linux-x64.tar.xz"

WORK=".tools/srs-windows"
DEST="bin/windows-amd64/srs"

mkdir -p "$WORK" "$DEST/objs" "$DEST/conf"

echo "==> 准备安装包 $ASSET"
verify() { echo "${SHA256}  $1" | sha256sum -c - >/dev/null 2>&1; }

if [[ -f "$WORK/$ASSET" ]] && verify "$WORK/$ASSET"; then
  echo "    已存在且校验通过，跳过下载"
else
  # 先找本地缓存（例如曾经手工下载过），避免重复走 GitHub。
  if [[ -f ".tools/srsdl/$ASSET" ]] && verify ".tools/srsdl/$ASSET"; then
    cp ".tools/srsdl/$ASSET" "$WORK/$ASSET"
    echo "    复用本地缓存 .tools/srsdl/$ASSET"
  else
    curl -L --retry 4 --retry-all-errors --retry-delay 3 -C - \
      --connect-timeout 30 -m 900 -o "$WORK/$ASSET" "$URL"
    verify "$WORK/$ASSET" || {
      echo "校验失败：$WORK/$ASSET" >&2
      exit 1
    }
  fi
fi

# SRS 的 Windows 构建是以 NSIS 安装包形式发布的，用 7-Zip 直接解开即可，无需运行安装程序。
if [[ -x "$WORK/7zz" ]]; then
  SEVENZIP="$WORK/7zz"
else
  echo "==> 准备 7-Zip（用于解 NSIS 安装包）"
  curl -sSL --retry 3 -o "$WORK/7z.tar.xz" "$SEVENZIP_URL"
  python3 - "$WORK" <<'PY'
import io, lzma, sys, tarfile
work = sys.argv[1]
raw = lzma.open(f"{work}/7z.tar.xz", "rb").read()
with tarfile.open(fileobj=io.BytesIO(raw)) as tar:
    member = tar.getmember("7zz")
    member.name = "7zz"
    tar.extract(member, work, filter="data")
PY
  chmod +x "$WORK/7zz"
  SEVENZIP="$WORK/7zz"
fi

echo "==> 解开安装包"
rm -rf "$WORK/extract"
"$SEVENZIP" x -y -o"$WORK/extract" "$WORK/$ASSET" >/dev/null

SRC="$WORK/extract/objs"
for f in srs.exe cygwin1.dll cyggcc_s-seh-1.dll cygstdc++-6.dll; do
  if [[ ! -f "$SRC/$f" ]]; then
    echo "安装包里缺少 ${f}，SRS 版本可能已变更" >&2
    exit 1
  fi
  cp "$SRC/$f" "$DEST/objs/"
done

cp deploy/srs/srs.windows.conf "$DEST/conf/srs.conf"

echo "==> 完成：$DEST"
ls -lh "$DEST/objs" "$DEST/conf"
echo
echo "提示：Windows 侧双击 bin\\windows-amd64\\start-all.cmd 即可同时启动 SRS 与 tinycast。"
