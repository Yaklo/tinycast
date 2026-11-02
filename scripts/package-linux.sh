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
STAGE_ROOT=".tools/linuxpkg-stage"
STAGE="$STAGE_ROOT/$NAME"

if [[ ! -x "$OUTDIR/mediamtx/mediamtx" || ! -f "$OUTDIR/mediamtx/LICENSE" ]]; then
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
cp LICENSE README.md THIRD-PARTY-NOTICES.md "$STAGE/"
cp deploy/mediamtx/mediamtx.yml "$STAGE/mediamtx/mediamtx.yml"
cp "$OUTDIR/mediamtx/mediamtx" "$STAGE/mediamtx/mediamtx"
cp "$OUTDIR/mediamtx/LICENSE" "$STAGE/mediamtx/LICENSE"
cp deploy/systemd/tinycast.service deploy/systemd/mediamtx.service "$STAGE/systemd/"
cp deploy/systemd/tinycast.env.example "$STAGE/systemd/tinycast.env.example"

cat > "$STAGE/version.txt" <<EOF
version:   $VERSION
commit:    $COMMIT
buildTime: $BUILD_TIME
go:        $(go version | awk '{print $3}')
EOF

# 仓库常放在 Windows 挂载点（DrvFs）上，那里 chmod 不生效、源文件一律 0777，
# 所以不能靠 chmod 收紧权限：改由下面打包步骤给每个成员写入固定 mode。
#   目录                       0755
#   tinycast / start.sh / mediamtx/mediamtx  0755
#   其余常规文件（LICENSE、README、env、systemd 单元 …）  0644
# 包内路径一律带顶层目录 $NAME/，这样解压后可以直接 cd tinycast-*-linux-amd64。
mkdir -p "$DIST"
EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct 2>/dev/null || date +%s)}"
TARBALL="$DIST/$NAME.tar.gz"

python3 - "$STAGE_ROOT" "$NAME" "$TARBALL" "$EPOCH" <<'PY_TAR'
import gzip
import os
import sys
import tarfile

stage_root, top, out, epoch = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
base = os.path.join(stage_root, top)

# 允许可执行的文件（相对暂存目录的路径）
EXEC_FILES = frozenset({"tinycast", "start.sh", "mediamtx/mediamtx"})

if not os.path.isdir(base):
    sys.exit(f"打包失败：找不到暂存目录 {base}")

missing = sorted(p for p in EXEC_FILES if not os.path.isfile(os.path.join(base, p)))
if missing:
    sys.exit("打包失败：暂存目录缺少可执行文件：" + "、".join(missing))


def entries(rel=""):
    """按名字排序递归产出 (相对暂存目录的路径, 是否目录)，保证归档顺序可复现。"""
    full = os.path.join(base, rel) if rel else base
    for entry in sorted(os.listdir(full)):
        child = f"{rel}/{entry}" if rel else entry
        path = os.path.join(full, entry)
        if os.path.isdir(path) and not os.path.islink(path):
            yield child, True
            yield from entries(child)
        else:
            yield child, False


def mode_for(rel, is_dir):
    if is_dir:
        return 0o755
    return 0o755 if rel in EXEC_FILES else 0o644


def fixed(info, mode):
    info.mode = mode
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    info.mtime = epoch
    return info


def add_dir(tar, arcname):
    info = fixed(tarfile.TarInfo(arcname), 0o755)
    info.type = tarfile.DIRTYPE
    info.size = 0
    tar.addfile(info)


def add_file(tar, rel):
    info = fixed(tarfile.TarInfo(f"{top}/{rel}"), mode_for(rel, False))
    info.type = tarfile.REGTYPE
    info.size = os.path.getsize(os.path.join(base, rel))
    with open(os.path.join(base, rel), "rb") as fh:
        tar.addfile(info, fh)


os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
tmp = out + ".tmp"
try:
    with open(tmp, "wb") as raw:
        # gzip 头 mtime=0 + tar 成员固定 mtime/属主 => 同样的输入产出同样的压缩包
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, compresslevel=9, mtime=0) as gz:
            with tarfile.open(fileobj=gz, mode="w", format=tarfile.GNU_FORMAT) as tar:
                # 顶层目录成员必须先写：缺了它，解压后 cd tinycast-* 会失败
                add_dir(tar, top)
                for rel, is_dir in entries():
                    if is_dir:
                        add_dir(tar, f"{top}/{rel}")
                    else:
                        add_file(tar, rel)
    os.replace(tmp, out)  # 中途失败不留半个 tarball
except BaseException:
    if os.path.exists(tmp):
        os.unlink(tmp)
    raise

# 回读校验：顶层目录、mode、属主写错就立刻失败，别等发布出去才发现
problems = []
with tarfile.open(out, "r:gz") as tar:
    members = tar.getmembers()
    if not members:
        problems.append("归档为空")
    elif members[0].name != top:
        problems.append(f"归档第一个成员是 {members[0].name!r}，应为顶层目录 {top!r}")
    for m in members:
        if m.name == top:
            want = 0o755
        elif m.name.startswith(top + "/"):
            want = mode_for(m.name[len(top) + 1:], m.isdir())
        else:
            problems.append(f"{m.name}: 不在顶层目录 {top}/ 之内")
            continue
        if m.mode != want:
            problems.append(f"{m.name}: mode={m.mode:04o}，应为 {want:04o}")
        if m.uid != 0 or m.gid != 0:
            problems.append(f"{m.name}: uid/gid={m.uid}/{m.gid}，应为 0/0")
if problems:
    sys.exit("打包校验失败：\n  " + "\n  ".join(problems))
print(f"==> 打包校验通过：{len(members)} 个成员（含顶层目录 {top}/），权限 0755/0644、属主 0/0")
PY_TAR

echo "==> 完成：$TARBALL"
ls -lh "$TARBALL"
tar -tvzf "$TARBALL"
