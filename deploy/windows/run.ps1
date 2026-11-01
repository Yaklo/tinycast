# 在 Windows 上启动 TinyCast 后端（读取同目录的 tinycast.env）
# 用法： powershell -ExecutionPolicy Bypass -File deploy\windows\run.ps1
# 注意：SRS 需先启动（srs\objs\srs.exe -c conf\srs.conf），回调地址指向 http://127.0.0.1:8080
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
