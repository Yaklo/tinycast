#!/usr/bin/env bash
# 开发辅助：把 Go 工具链与缓存指向仓库内的 .tools 目录（适用于 $HOME 不可写的环境）。
# 用法： source scripts/dev-env.sh
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="$ROOT/.tools/go/bin:$PATH"
export GOCACHE="$ROOT/.tools/gocache"
export GOMODCACHE="$ROOT/.tools/gopath/pkg/mod"
export GOPATH="$ROOT/.tools/gopath"
export PNPM_HOME="$ROOT/.tools/pnpm"
export PATH="$PNPM_HOME:$PATH"
echo "go:   $(command -v go || echo 未安装) $(go version 2>/dev/null || true)"
echo "node: $(command -v node || echo 未安装) $(node --version 2>/dev/null || true)"
