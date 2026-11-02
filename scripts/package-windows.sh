#!/usr/bin/env bash
# 一键产出 Windows 运行包：构建 tinycast.exe、准备 MediaMTX（Windows 版）、打成 zip。
#
# 用法：./scripts/package-windows.sh            # 首次会联网下载 MediaMTX（约 30MB）
#       SKIP_WEB=1 ./scripts/package-windows.sh # 前端产物已存在时跳过前端构建
# 产物：dist/tinycast-<版本>-windows-amd64.zip（解压即用，目录结构见 deploy/windows/部署说明.txt）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# 仓库内自带工具链时优先使用（$HOME 不可写的环境）
if [[ -x ".tools/go/bin/go" ]]; then
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
fi

# shellcheck disable=SC1091
source scripts/release-env.sh

OUTDIR="bin/windows-amd64"
STAGE=".tools/winpkg-stage"
DIST="${DIST:-dist}"
ZIP="$DIST/tinycast-${VERSION}-windows-amd64.zip"
mkdir -p "$DIST"

if [[ ! -f "$OUTDIR/mediamtx/mediamtx.exe" || ! -f "$OUTDIR/mediamtx/LICENSE" ]]; then
  echo "==> 首次构建，先获取 MediaMTX 的 Windows 版本"
  ./scripts/fetch-mediamtx-windows.sh
fi

# 目标目录里的 tinycast.exe 可能正在 Windows 上运行，此时删除会返回 Input/output error。
# 这种情况改为在临时目录里构建，只更新 zip，不去动用户正在用的目录。
# 代价是 $OUTDIR 里的旧文件（尤其是 tinycast.exe）不会自动更新，所以打完包会尽力
# 回写一次，并明确列出哪些文件仍是旧的 —— 不静默分叉。
WIN_OUTDIR="$OUTDIR"
STAGED=0
if [[ -e "$OUTDIR/tinycast.exe" ]] && ! rm -f "$OUTDIR/tinycast.exe" 2>/dev/null; then
  STAGED=1
  WIN_OUTDIR="$STAGE"
  echo "==> [WARN] $OUTDIR/tinycast.exe 正在被占用（可能还在运行）" >&2
  echo "         改为在 $STAGE 构建，本次 zip 的内容来自临时目录。" >&2
  echo "         $OUTDIR/tinycast.exe 不会更新（bin 未更新），打完包会尝试回写其余文件。" >&2
  rm -rf "$STAGE"
  mkdir -p "$STAGE/mediamtx"
  cp "$OUTDIR/mediamtx/mediamtx.exe" "$STAGE/mediamtx/mediamtx.exe"
  cp "$OUTDIR/mediamtx/LICENSE" "$STAGE/mediamtx/LICENSE"
fi

WIN_OUTDIR="$WIN_OUTDIR" ./scripts/build.sh --windows

# 运行时日志、pid/id 不进压缩包：下面的 zip 步骤会跳过 logs 目录与 .log/.pid/.id 文件。
# 这里刻意不动 $WIN_OUTDIR：目标目录往往就是用户正在运行的 bin/windows-amd64，
# 打包时删日志既没必要，也可能删掉正在排查的现场。

# 包里放一份版本信息，用户报障时能直接对上是哪一版
printf 'version:   %s\r\ncommit:    %s\r\nbuildTime: %s\r\n' \
  "$VERSION" "$COMMIT" "$BUILD_TIME" > "$WIN_OUTDIR/version.txt"

echo "==> 打包 $ZIP"
python3 - "$WIN_OUTDIR" "$ZIP" <<'PY'
import os, shutil, sys, time, zipfile

src, out = sys.argv[1], sys.argv[2]

# 包内权限：常规文件 0644，可执行文件 0755。
# 仓库常在 Windows 挂载点（DrvFs）上，源文件权限恒为 0777，不能沿用 st_mode
# （这正是之前 zip 成员 external_attr 全是 0777 的原因）。
EXEC_SUFFIXES = (".exe", ".cmd", ".bat", ".ps1", ".sh")


def mode_for(rel, is_dir):
    if is_dir:
        return 0o755
    return 0o755 if rel.lower().endswith(EXEC_SUFFIXES) else 0o644


def dos_time(path):
    t = time.gmtime(os.path.getmtime(path))
    if t.tm_year < 1980:  # ZIP 时间戳最早只支持 1980 年
        t = time.gmtime(315532800)
    return t[:6]


os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as z:
    for base, dirs, files in os.walk(src):
        dirs.sort()
        relbase = os.path.relpath(base, src)
        if relbase == ".":
            relbase = ""
        for name in list(dirs):
            if name == "logs":
                dirs.remove(name)  # 运行期日志目录整个不进包
                continue
            rel = f"{relbase}/{name}" if relbase else name
            info = zipfile.ZipInfo(rel + "/", date_time=dos_time(os.path.join(base, name)))
            info.create_system = 3  # Unix：解压端才会按 external_attr 恢复权限位
            info.external_attr = ((0o040000 | mode_for(rel, True)) << 16) | 0x10
            info.compress_type = zipfile.ZIP_STORED
            z.writestr(info, b"")
        for name in sorted(files):
            # 运行期残留的 pid/id 文件与日志一样不进包
            if name.endswith((".pid", ".id", ".log")):
                continue
            full = os.path.join(base, name)
            rel = f"{relbase}/{name}" if relbase else name
            info = zipfile.ZipInfo(rel, date_time=dos_time(full))
            info.create_system = 3
            info.external_attr = (0o100000 | mode_for(rel, False)) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            with open(full, "rb") as fh, z.open(info, "w") as dst:
                shutil.copyfileobj(fh, dst, 1024 * 1024)
PY

echo "==> 完成：$ZIP"
ls -lh "$ZIP"
python3 - "$ZIP" <<'PY'
import sys, zipfile

REQUIRED = ("tinycast.exe", "start-all.cmd", "mediamtx/mediamtx.exe")

with zipfile.ZipFile(sys.argv[1]) as z:
    problems = []
    names = set()
    for info in z.infolist():
        names.add(info.filename.rstrip("/"))
        mode = info.external_attr >> 16
        print(f"    {mode & 0o7777:04o}  {info.file_size:>9}  {info.filename}")
        if mode & 0o022:
            problems.append(f"{info.filename}: 对组/其他人可写")
        if (mode & 0o7777) == 0:
            problems.append(f"{info.filename}: 权限位是 0（external_attr 没写上）")
        if info.create_system != 3:
            problems.append(f"{info.filename}: create_system={info.create_system}，解压端不会恢复 Unix 权限")
    for need in REQUIRED:
        if need not in names:
            problems.append(f"缺少必需成员 {need}")
    if problems:
        sys.exit("zip 校验失败：\n  " + "\n  ".join(problems))
PY

# exe 被占用时 zip 来自临时目录，这里尽力把新内容回写到 $OUTDIR，
# 让 bin 与发布包尽量一致；写不进去的（正在运行的 exe）明确列出来。
if [[ "$STAGED" -eq 1 ]]; then
  # 暂存目录万一被删/被占用，这里必须报错而不是打印"已回写"
  if [[ ! -d "$STAGE" ]]; then
    echo "==> [ERROR] 暂存目录 $STAGE 不存在，无法回写；$OUTDIR 仍是旧内容（bin 未更新）" >&2
    exit 1
  fi
  echo "==> 尝试把 $STAGE 的内容回写到 $OUTDIR（被占用的文件会跳过）"
  missed=()
  copied=0
  while IFS= read -r -d '' rel; do
    rel="${rel#./}"
    dst="$OUTDIR/$rel"
    mkdir -p "$(dirname "$dst")"
    if [[ -e "$dst" ]] && ! rm -f "$dst" 2>/dev/null; then
      missed+=("$rel")
    elif ! cp -f "$STAGE/$rel" "$dst" 2>/dev/null; then
      missed+=("$rel")
    else
      copied=$((copied + 1))
    fi
  done < <(cd "$STAGE" && find . -type f -print0 | sort -z)

  if [[ "$copied" -eq 0 && ${#missed[@]} -eq 0 ]]; then
    echo "==> [ERROR] $STAGE 里没有可回写的文件，$OUTDIR 没有更新（bin 未更新）" >&2
    exit 1
  elif [[ ${#missed[@]} -gt 0 ]]; then
    echo "==> [WARN] 已回写 $copied 个文件；以下文件没能写回 $OUTDIR（文件仍被占用，bin 未完全更新）：" >&2
    printf '         %s\n' "${missed[@]}" >&2
    echo "         要更新它们请先退出正在运行的 TinyCast，再重新执行本脚本。" >&2
  else
    echo "==> 已回写 $copied 个文件：$OUTDIR 与 $STAGE 内容一致"
  fi
fi
