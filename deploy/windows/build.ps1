# 在 Windows 上就地构建 TinyCast（需要 Go 1.24+、Node 22+、pnpm）
# 用法： powershell -ExecutionPolicy Bypass -File deploy\windows\build.ps1
# 产物： bin\tinycast.exe（前端已内嵌）。
# 只想要开箱即用的运行包时，在 Linux/WSL 侧执行 ./scripts/build.sh --windows 更省事。
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Set-Location $root

if (-not (Test-Path 'web\node_modules')) {
    Push-Location web
    pnpm install --frozen-lockfile
    Pop-Location
}

Write-Host '==> 构建前端'
Push-Location web
pnpm build
Pop-Location

New-Item -ItemType Directory -Force -Path 'internal\webui\dist' | Out-Null
if (-not (Test-Path 'internal\webui\dist\.gitkeep')) {
    New-Item -ItemType File -Path 'internal\webui\dist\.gitkeep' | Out-Null
}

Write-Host '==> 构建后端'
New-Item -ItemType Directory -Force -Path 'bin' | Out-Null
$env:CGO_ENABLED = '0'
$version = if ($env:TINYCAST_VERSION) { $env:TINYCAST_VERSION } else { 'devel' }
$commit = if ($env:TINYCAST_COMMIT) { $env:TINYCAST_COMMIT } else {
    try { (git rev-parse --short HEAD).Trim() } catch { '' }
}
$buildTime = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$ldflags = "-s -w -X tinycast/internal/version.Version=$version -X tinycast/internal/version.Commit=$commit -X tinycast/internal/version.BuildTime=$buildTime"
go build -trimpath -ldflags $ldflags -o bin\tinycast.exe ./cmd/tinycast

Write-Host "==> 完成：$root\bin\tinycast.exe"
