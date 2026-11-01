# 在 Windows 上启动 TinyCast 后端
# 用法： powershell -ExecutionPolicy Bypass -File deploy\windows\run.ps1
# 注意：SRS 需先启动（srs.exe -c conf\srs.conf），并把 srs.conf 的 http_hooks 指向 http://127.0.0.1:8080
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$exe = Join-Path $root 'bin\tinycast.exe'
if (-not (Test-Path $exe)) {
    throw "未找到 $exe，请先运行 deploy\windows\build.ps1"
}

# ---- 按部署环境修改以下变量 ----
$env:HTTP_ADDR = ':8080'
$env:LOG_LEVEL = 'info'
$env:ROOM_GRACE = '5m'
$env:JANITOR_INTERVAL = '15s'
$env:SRS_APP = 'live'
$env:SRS_CALLBACK_ALLOWED_CIDRS = '127.0.0.0/8,::1/128,192.168.0.0/16,10.0.0.0/8,172.16.0.0/12'
# $env:SRS_CALLBACK_SECRET = 'change-me'
$env:SRS_RTMP_PUBLIC_BASE = 'rtmp://127.0.0.1/live'
$env:SRS_WEBRTC_PUBLIC_BASE = 'webrtc://127.0.0.1/live'
$env:SRS_RTC_PROXY_TARGET = 'http://127.0.0.1:1985'
$env:SRS_CLIENTS_API_URL = 'http://127.0.0.1:1985/api/v1/clients/'

Write-Host "==> 启动 TinyCast：$exe"
& $exe @args
