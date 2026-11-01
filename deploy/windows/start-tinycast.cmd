@echo off
rem TinyCast launcher for Windows. Starts the embedded web UI on http://127.0.0.1:8080
setlocal
cd /d "%~dp0"

if "%HTTP_ADDR%"=="" set HTTP_ADDR=:8080
if "%ROOM_GRACE%"=="" set ROOM_GRACE=5m
if "%JANITOR_INTERVAL%"=="" set JANITOR_INTERVAL=15s

tinycast.exe -open

echo.
echo TinyCast exited. Press any key to close this window.
pause >nul
