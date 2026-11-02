# 在 Windows 上启动 TinyCast 后端（读取同目录的 tinycast.env）
# 用法： powershell -ExecutionPolicy Bypass -File deploy\windows\run.ps1
# 注意：MediaMTX 需先启动（mediamtx\mediamtx.exe mediamtx.yml），
#       它的 authHTTPAddress 默认指向 http://127.0.0.1:18856/internal/mtx/auth（与 INTERNAL_ADDR 一致）
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$exe = Join-Path $root 'bin\tinycast.exe'
$envFile = Join-Path $PSScriptRoot 'tinycast.env'
if (-not (Test-Path $exe)) {
    throw "未找到 $exe，请先运行 deploy\windows\build.ps1 或 scripts/build.sh --windows"
}
if (-not (Test-Path $envFile)) {
    throw "未找到配置文件 $envFile"
}

Write-Host "==> 启动 TinyCast：$exe"
& $exe -config $envFile @args
