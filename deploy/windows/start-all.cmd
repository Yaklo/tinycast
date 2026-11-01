@echo off
rem TinyCast production launcher: SRS 6 + TinyCast (Windows).
rem Close the SRS window to stop media, press Ctrl+C in this window to stop TinyCast.
setlocal
cd /d "%~dp0"

if not exist "srs\objs\srs.exe" (
  echo [ERROR] srs\objs\srs.exe not found.
  echo         Copy the SRS 6 Windows build into the srs folder first.
  pause
  exit /b 1
)
if not exist "srs\conf\srs.conf" (
  echo [ERROR] srs\conf\srs.conf not found.
  pause
  exit /b 1
)
if not exist "srs\logs" mkdir "srs\logs"

echo [SRS] starting (1935/tcp rtmp, 1985/tcp http-api, 8000/udp webrtc) ...
start "TinyCast - SRS 6" /D "%~dp0srs" cmd /c "objs\srs.exe -c conf\srs.conf 1>> logs\srs.log 2>&1"
timeout /t 2 /nobreak >nul

echo [TinyCast] starting on port 8080 ...
call "%~dp0start.cmd" -open
