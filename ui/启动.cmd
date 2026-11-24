@echo off
cd /d "%~dp0"
if exist "openlist-sync-ui.exe" (
  start "" "%~dp0openlist-sync-ui.exe"
) else (
  echo missing openlist-sync-ui.exe
  pause
)
exit /b