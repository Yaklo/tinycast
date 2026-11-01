#!/usr/bin/env bash
# 计算发布用的版本信息，被 build.sh 与各打包脚本 source，保证一次发布里所有产物版本一致。
#
# 优先级：环境变量 VERSION / COMMIT / BUILD_TIME > git describe > 默认值。
# 打 tag 之后执行 git describe 会得到 v0.1.0-0-g<sha>，这里会把它收成干净的 v0.1.0。

if [[ -z "${VERSION:-}" ]]; then
  ver="$(git describe --tags --long --dirty --match "v*" 2>/dev/null || true)"
  if [[ "$ver" =~ ^(v[0-9]+\.[0-9]+\.[0-9]+)-0-g[0-9a-f]+(-dirty)?$ ]]; then
    ver="${BASH_REMATCH[1]}${BASH_REMATCH[2]}"
  fi
  VERSION="${ver:-devel}"
fi

COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

export VERSION COMMIT BUILD_TIME
