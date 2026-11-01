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

rem Rotate mediamtx.log at 16 MB, keep three older files.
if exist "logs\mediamtx.log" (
  for %%F in ("logs\mediamtx.log") do if %%~zF GTR 16777216 (
    if exist "logs\mediamtx.log.3" del /q "logs\mediamtx.log.3"
    if exist "logs\mediamtx.log.2" ren "logs\mediamtx.log.2" "mediamtx.log.3"
    if exist "logs\mediamtx.log.1" ren "logs\mediamtx.log.1" "mediamtx.log.2"
    ren "logs\mediamtx.log" "mediamtx.log.1"
  )
)

echo [MediaMTX] starting (RTMP 1935, HLS 8888, WebRTC 8889, media UDP 8000) ...
start "TinyCast - MediaMTX" /D "%~dp0mediamtx" cmd /c "mediamtx.exe mediamtx.yml 1>> ..\logs\mediamtx.log 2>&1"

echo [MediaMTX] waiting for control API ...
set /a WAIT=0
:wait_mtx
curl.exe -s -o NUL -m 2 http://127.0.0.1:9997/v3/config/global/get
if not errorlevel 1 goto mtx_ready
set /a WAIT+=1
if %WAIT% GEQ 15 goto mtx_failed
timeout /t 1 /nobreak >nul
goto wait_mtx

:mtx_ready
echo [MediaMTX] ready.
goto start_tinycast

:mtx_failed
echo [ERROR] MediaMTX did not become ready in 15 seconds.
echo         Check logs\mediamtx.log; ports 1935/8888/8889/9997 may be occupied.
if exist "logs\mediamtx.log" powershell -NoProfile -Command "Get-Content -Path logs\mediamtx.log -Tail 20"
pause
exit /b 1

:start_tinycast
echo [TinyCast] starting on port 18855 ...
call "%~dp0start.cmd" -open
