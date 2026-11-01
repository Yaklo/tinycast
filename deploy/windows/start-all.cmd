@echo off
rem TinyCast launcher: MediaMTX (media) + TinyCast (business logic) for Windows.
rem Close the MediaMTX window to stop media; press Ctrl+C here to stop TinyCast.
setlocal
cd /d "%~dp0"

if not exist "mediamtx\mediamtx.exe" (
  echo [ERROR] mediamtx\mediamtx.exe not found.
  echo         Extract the whole package; MediaMTX ships in the mediamtx folder.
  pause
  exit /b 1
)
if not exist "mediamtx\mediamtx.yml" (
  echo [ERROR] mediamtx\mediamtx.yml not found.
  pause
  exit /b 1
)
if not exist "logs" mkdir "logs"

echo [MediaMTX] starting (RTMP 1935, HLS 8888, WebRTC 8889, media UDP 8000) ...
start "TinyCast - MediaMTX" /D "%~dp0mediamtx" cmd /c "mediamtx.exe mediamtx.yml 1>> ..\logs\mediamtx.log 2>&1"
timeout /t 2 /nobreak >nul

echo [TinyCast] starting on port 8080 ...
call "%~dp0start.cmd" -open
