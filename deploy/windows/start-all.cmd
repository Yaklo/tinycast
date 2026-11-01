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

rem SRS is a Cygwin build: srs.exe needs these DLLs next to it, or Windows pops up
rem "cygiconv-2.dll / cygintl-8.dll was not found" and never starts. Check them first.
set "MISSING=0"
for %%D in (cygwin1.dll cyggcc_s-seh-1.dll cygstdc++-6.dll cygiconv-2.dll cygintl-8.dll) do if not exist "srs\objs\%%D" (
  echo [ERROR] srs\objs\%%D is missing.
  set /a MISSING+=1
)
if not "%MISSING%"=="0" (
  echo         Extract the whole package again; do not copy srs.exe alone.
  pause
  exit /b 1
)

if not exist "srs\logs" mkdir "srs\logs"

echo [SRS] starting (1935/tcp rtmp, 1985/tcp http-api, 8000/udp webrtc) ...
start "TinyCast - SRS 6" /D "%~dp0srs" cmd /c "objs\srs.exe -c conf\srs.conf 1>> logs\srs.log 2>&1"
timeout /t 2 /nobreak >nul

echo [TinyCast] starting on port 8080 ...
call "%~dp0start.cmd" -open
