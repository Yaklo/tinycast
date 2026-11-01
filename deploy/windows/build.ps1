# 在 Windows 上构建 TinyCast（需要 Go 1.24+、Node 22+、pnpm）
# 用法： powershell -ExecutionPolicy Bypass -File deploy\windows\build.ps1
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
go build -trimpath -ldflags '-s -w' -o bin\tinycast.exe ./cmd/tinycast

Write-Host "==> 完成：$root\bin\tinycast.exe"
