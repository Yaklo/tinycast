#!/usr/bin/env bash
# 下载 SRS 6 官方 Windows 构建（Cygwin 版），解出 srs.exe 与全部依赖 DLL，
# 放到 bin/windows-amd64/srs/ ，供 Windows 侧与 tinycast 一起做真实 OBS 推流测试。
#
# 注意：官方安装包的 objs\ 里只带了 cygwin1.dll、cyggcc_s-seh-1.dll、cygstdc++-6.dll 三个，
# 而 cygstdc++-6.dll 依赖 cygiconv-2.dll 与 cygintl-8.dll，缺了会在启动时报
# 「由于找不到 cygiconv-2.dll，无法继续执行代码」。本脚本从 Cygwin 官方软件源补齐这两个 DLL。
#
# 用法：./scripts/fetch-srs-windows.sh
# 环境变量：CYGWIN_MIRROR 可换镜像（默认清华源，失败自动回退 cygwin.com）
# 产物（不入库，体积约 45MB）：
#   bin/windows-amd64/srs/objs/srs.exe
#   bin/windows-amd64/srs/objs/cygwin1.dll
#   bin/windows-amd64/srs/objs/cyggcc_s-seh-1.dll
#   bin/windows-amd64/srs/objs/cygstdc++-6.dll
#   bin/windows-amd64/srs/objs/cygiconv-2.dll   （从 Cygwin 官方 libiconv2 包补齐）
#   bin/windows-amd64/srs/objs/cygintl-8.dll    （从 Cygwin 官方 libintl8 包补齐）
#   bin/windows-amd64/srs/conf/srs.conf         （来自 deploy/srs/srs.windows.conf）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="v6.0-b0"                                       # 6.0 的正式发布，内含 SRS 6.0.172
ASSET="SRS-Windows-x86_64-6.0-b0-setup.exe"
SHA256="d0af39202c8ad224f06549cd9ed7d8b6f87c5569ed9bde53216b2276772bcbdc"
URL="https://github.com/ossrs/srs/releases/download/${VERSION}/${ASSET}"
SEVENZIP_URL="https://www.7-zip.org/a/7z2301-linux-x64.tar.xz"

# 官方安装包缺失的两个 Cygwin 运行时依赖，取自 Cygwin x86_64 官方软件源。
MIRRORS="${CYGWIN_MIRROR:-https://mirrors.tuna.tsinghua.edu.cn/cygwin} https://cygwin.com"
CYG_PKGS=(
  "libiconv/libiconv2/libiconv2-1.17-1.tar.xz  695679eac95451bdbffb01309aba58104f10734e5998e8a4619e457505d58c00"
  "gettext/libintl8/libintl8-0.22.5-1.tar.xz   aa981c2583e187c5c07c75132d714fe3b8a4a81f585e9b5de64f0bf8c8dd211f"
)

WORK=".tools/srs-windows"
DEST="bin/windows-amd64/srs"

mkdir -p "$WORK" "$DEST/objs" "$DEST/conf"

echo "==> 准备安装包 $ASSET"
verify() { echo "$1  $2" | sha256sum -c - >/dev/null 2>&1; }

if [[ -f "$WORK/$ASSET" ]] && verify "$SHA256" "$WORK/$ASSET"; then
  echo "    已存在且校验通过，跳过下载"
else
  # 先找本地缓存（例如曾经手工下载过），避免重复走 GitHub。
  if [[ -f ".tools/srsdl/$ASSET" ]] && verify "$SHA256" ".tools/srsdl/$ASSET"; then
    cp ".tools/srsdl/$ASSET" "$WORK/$ASSET"
    echo "    复用本地缓存 .tools/srsdl/$ASSET"
  else
    curl -L --retry 4 --retry-all-errors --retry-delay 3 -C - \
      --connect-timeout 30 -m 900 -o "$WORK/$ASSET" "$URL"
    verify "$SHA256" "$WORK/$ASSET" || {
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
[[ -f "$SRC/srs.exe" ]] || { echo "安装包里缺少 srs.exe，SRS 版本可能已变更" >&2; exit 1; }

# srs.exe 与它旁边的所有 DLL 一起拷走（Cygwin 运行时全在里面）
cp "$SRC/srs.exe" "$DEST/objs/"
shopt -s nullglob
for dll in "$SRC"/*.dll; do cp "$dll" "$DEST/objs/"; done
shopt -u nullglob

echo "==> 补齐安装包缺失的 Cygwin 运行时（cygiconv-2.dll、cygintl-8.dll）"
for entry in "${CYG_PKGS[@]}"; do
  read -r rel want <<<"$entry"
  pkg="$(basename "$rel")"
  cache="$WORK/cygwin/$pkg"
  mkdir -p "$WORK/cygwin"
  if [[ -f "$cache" ]] && verify "$want" "$cache"; then
    echo "    已缓存 $pkg"
  else
    ok=0
    for m in $MIRRORS; do
      src="${m%/}/x86_64/release/${rel}"
      echo "    下载 $pkg"
      if curl -sSL --retry 3 --retry-all-errors --connect-timeout 20 -m 300 -o "$cache" "$src" \
        && verify "$want" "$cache"; then ok=1; break; fi
      echo "    ${src} 不可用，换下一个镜像" >&2
      rm -f "$cache"
    done
    [[ "$ok" == "1" ]] || { echo "无法下载 $pkg（可用 CYGWIN_MIRROR 指定镜像）" >&2; exit 1; }
  fi
  # 包里只取 usr/bin 下的 DLL（用 python 解包，避免 tar 通配符在 BSD/macOS 上不可用）
  python3 - "$cache" "$WORK/cygwin" <<'PY'
import io, lzma, os, sys, tarfile
pkg, out = sys.argv[1], sys.argv[2]
with tarfile.open(fileobj=io.BytesIO(lzma.open(pkg, "rb").read())) as tar:
    for member in tar.getmembers():
        if member.isfile() and member.name.startswith("usr/bin/") and member.name.endswith(".dll"):
            member.name = os.path.basename(member.name)
            tar.extract(member, out, filter="data")
PY
  for dll in "$WORK/cygwin"/*.dll; do cp "$dll" "$DEST/objs/"; done
  rm -f "$WORK/cygwin"/*.dll
done

# 依赖完整性自检：srs.exe 直接/间接依赖的每个非系统 DLL 都必须在 objs\ 里
echo "==> 校验 DLL 依赖完整性"
python3 - "$DEST/objs" <<'PY'
import os, struct, sys

SYSTEM = {
    "kernel32.dll", "ntdll.dll", "bcrypt.dll", "msvcrt.dll", "advapi32.dll",
    "user32.dll", "ws2_32.dll", "shell32.dll", "ole32.dll", "crypt32.dll",
    "iphlpapi.dll", "psapi.dll", "secur32.dll", "shlwapi.dll", "userenv.dll",
    "winmm.dll", "gdi32.dll", "dbghelp.dll", "version.dll", "netapi32.dll",
}

class PE:
    def __init__(self, path):
        self.d = open(path, "rb").read()
        d = self.d
        e = struct.unpack_from("<I", d, 0x3C)[0]
        coff = e + 4
        nsec = struct.unpack_from("<H", d, coff + 2)[0]
        optsz = struct.unpack_from("<H", d, coff + 16)[0]
        opt = coff + 20
        magic = struct.unpack_from("<H", d, opt)[0]
        self.dd = opt + (112 if magic == 0x20B else 96)
        sec = opt + optsz
        self.secs = []
        for i in range(nsec):
            o = sec + i * 40
            vsz, va, rsz, ptr = struct.unpack_from("<IIII", d, o + 8)
            self.secs.append((va, vsz, ptr, rsz))

    def off(self, rva):
        for va, vsz, ptr, rsz in self.secs:
            if va <= rva < va + max(vsz, rsz):
                return ptr + (rva - va)
        return None

    def imports(self):
        """返回 {dll 名: [函数名...]}，只解析导入表，不解析延迟导入（这几个 DLL 都没有）。"""
        rva, _ = struct.unpack_from("<II", self.d, self.dd + 8)
        if not rva:
            return {}
        o = self.off(rva)
        table = {}
        while True:
            ent = self.d[o:o + 20]
            if len(ent) < 20 or ent == b"\0" * 20:
                return table
            ilt, _, _, nrva, iat = struct.unpack_from("<IIIII", ent)
            p = self.off(nrva)
            dll = self.d[p:self.d.index(b"\0", p)].decode("ascii", "replace")
            funcs, t = [], self.off(ilt or iat)
            while True:
                v = struct.unpack_from("<Q", self.d, t)[0]
                if v == 0:
                    break
                if v >> 63:
                    funcs.append("#%d" % (v & 0xFFFF))
                else:
                    q = self.off((v & 0x7FFFFFFF) + 2)
                    funcs.append(self.d[q:self.d.index(b"\0", q)].decode("ascii", "replace"))
                t += 8
            table[dll] = funcs
            o += 20

    def exports(self):
        rva, _ = struct.unpack_from("<II", self.d, self.dd)
        if not rva:
            return set()
        o = self.off(rva)
        nname = struct.unpack_from("<I", self.d, o + 24)[0]
        base = self.off(struct.unpack_from("<I", self.d, o + 32)[0])
        names = set()
        for i in range(nname):
            r = struct.unpack_from("<I", self.d, base + 4 * i)[0]
            if r:
                p = self.off(r)
                names.add(self.d[p:self.d.index(b"\0", p)].decode("ascii", "replace"))
        return names

objs = sys.argv[1]
seen, queue, missing, tables = set(), ["srs.exe"], [], {}
while queue:
    name = queue.pop()
    tables[name] = PE(os.path.join(objs, name)).imports()
    for dll in tables[name]:
        key = dll.lower()
        if key in seen or key in SYSTEM:
            continue
        seen.add(key)
        if os.path.exists(os.path.join(objs, dll)):
            queue.append(dll)
        else:
            missing.append((name, dll))
if missing:
    for src, dll in missing:
        print(f"    缺少依赖：{src} -> {dll}", file=sys.stderr)
    sys.exit(1)

# 符号级校验：本地 DLL 从包内 cygwin1.dll 导入的每个函数都必须被该 DLL 导出，
# 否则运行时会报「无法定位程序输入点」或「程序无法正常启动」。
cyg_exports = PE(os.path.join(objs, "cygwin1.dll")).exports()
bad = []
for name, table in tables.items():
    for dll, funcs in table.items():
        if dll.lower() == "cygwin1.dll":
            bad += [(name, f) for f in funcs if f not in cyg_exports]
if bad:
    for name, f in bad:
        print(f"    符号缺失：{name} 需要 cygwin1.dll!{f}", file=sys.stderr)
    sys.exit(1)
print(f"    通过：DLL 依赖闭环（{len(seen)} 个本地依赖），Cygwin 符号全部匹配")
PY

cp deploy/srs/srs.windows.conf "$DEST/conf/srs.conf"

echo "==> 完成：$DEST"
ls -lh "$DEST/objs" "$DEST/conf"
echo
echo "提示：Windows 侧双击 bin\\windows-amd64\\start-all.cmd 即可同时启动 SRS 与 tinycast。"
