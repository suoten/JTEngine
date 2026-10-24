@echo off
rem JTE one-click installer (Windows entry point)
rem Double-click to run. All real logic lives in install-jte.ps1
chcp 65001 >nul
title JTE Installer

where powershell >nul 2>nul
if errorlevel 1 (
    echo [X] PowerShell not found. This script requires Windows 10 or later.
    pause
    exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-jte.ps1" %*
set EXITCODE=%ERRORLEVEL%

echo.
if "%EXITCODE%"=="0" (
    echo Done. You can close this window.
) else (
    echo Install failed with exit code %EXITCODE%. Fix the problem above and run again.
)
pause
