@echo off
rem Simulate the SRS callbacks (publish / play / unpublish) against a local TinyCast.
setlocal
cd /d "%~dp0"

if "%1"=="" (
  tinysim.exe demo
) else (
  tinysim.exe %*
)

echo.
echo Press any key to close this window.
pause >nul
