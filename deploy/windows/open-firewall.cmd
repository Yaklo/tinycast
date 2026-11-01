@echo off
REM ==========================================================================
REM  TinyCast firewall rules
REM
REM  Run this file as Administrator (right-click -> "Run as administrator").
REM  It opens the ports SRS needs on this machine:
REM    TCP 1935  OBS publishes RTMP here
REM    TCP 8080  TinyCast web UI, API and the /rtc/ signalling proxy
REM    TCP 1985  SRS HTTP API (internal diagnostics only)
REM    UDP 8000  WebRTC media - REQUIRED, without it the player connects
REM              but never receives a single video frame (black screen)
REM
REM  The rules are named "TinyCast ..." so you can delete them later from
REM  wf.msc (Windows Defender Firewall with Advanced Security).
REM ==========================================================================
setlocal

net session >nul 2>&1
if errorlevel 1 (
  echo [ERROR] Administrator privileges are required.
  echo Right-click this file and choose "Run as administrator".
  echo.
  pause
  exit /b 1
)

call :allow "TinyCast TCP 1935" TCP 1935
call :allow "TinyCast TCP 8080" TCP 8080
call :allow "TinyCast TCP 1985" TCP 1985
call :allow "TinyCast UDP 8000" UDP 8000

echo.
echo Done. Current rules:
netsh advfirewall firewall show rule name="TinyCast" dir=in | findstr /i "Rule Name LocalPort Protocol"
echo.
pause
exit /b 0

:allow
netsh advfirewall firewall delete rule name="%~1" dir=in >nul 2>&1
netsh advfirewall firewall add rule name="%~1" dir=in action=allow protocol=%2 localport=%3 >nul
if errorlevel 1 (
  echo [WARN] failed to add rule: %~1
) else (
  echo [ OK ] %~1
)
exit /b 0
