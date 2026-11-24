@echo off
taskkill /IM openlist-sync-ui.exe /F >nul 2>&1
echo OpenList Sync stopped.
timeout /t 2 >nul