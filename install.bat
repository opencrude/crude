@echo off
setlocal
rem Thin Windows PowerShell 5.1+ wrapper. All options belong to install.ps1.
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
exit /b %ERRORLEVEL%
