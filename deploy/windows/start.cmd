@echo off
rem TinyCast production launcher (Windows).
rem Runs the business backend in the current console window and reads tinycast.env.
rem Extra arguments are passed to tinycast.exe, e.g.  start.cmd -open
setlocal
cd /d "%~dp0"

if not exist "tinycast.exe" (
  echo [ERROR] tinycast.exe not found in %~dp0
  pause
  exit /b 1
)
if not exist "tinycast.env" (
  echo [ERROR] tinycast.env not found in %~dp0
  pause
  exit /b 1
)
if not exist "logs" mkdir "logs"

echo [TinyCast] build:
tinycast.exe -version
echo [TinyCast] config = %~dp0tinycast.env
tinycast.exe -config "%~dp0tinycast.env" %*

echo.
echo [TinyCast] stopped, exit code = %ERRORLEVEL%
pause
