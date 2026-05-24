@echo off
setlocal enabledelayedexpansion

REM Crude installer for Windows
REM Usage: install.bat [stable|beta]

set "CHANNEL=%~1"
if "%CHANNEL%"=="" set "CHANNEL=stable"

if /i not "%CHANNEL%"=="stable" if /i not "%CHANNEL%"=="beta" (
    echo Error: unknown channel '%CHANNEL%'. Use 'stable' or 'beta'.
    exit /b 1
)

REM Determine binary name
if /i "%CHANNEL%"=="beta" (
    set "BINARY_NAME=crude-beta.exe"
    set "BUILD_CMD=go build -tags beta -o crude-beta.exe ."
) else (
    set "BINARY_NAME=crude.exe"
    set "BUILD_CMD=go build -o crude.exe ."
)

echo === Crude Installer ===
echo Channel : %CHANNEL%
echo.

REM Determine install directory
set "INSTALL_DIR=%LOCALAPPDATA%\crude\bin"
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"

echo Install dir: %INSTALL_DIR%
echo.

REM Build
echo Building %BINARY_NAME% ...
%BUILD_CMD%
if errorlevel 1 (
    echo Error: build failed.
    exit /b 1
)
echo Build succeeded.

REM Install
echo Installing to %INSTALL_DIR%\%BINARY_NAME% ...
copy /y "%BINARY_NAME%" "%INSTALL_DIR%\%BINARY_NAME%" >nul
if errorlevel 1 (
    echo Error: failed to copy binary to install directory.
    del /f "%BINARY_NAME%" 2>nul
    exit /b 1
)

REM Clean up local binary
del /f "%BINARY_NAME%" 2>nul

REM Check if install dir is in PATH
echo %PATH% | find /i "%INSTALL_DIR%" >nul 2>&1
if errorlevel 1 (
    echo.
    echo WARNING: %INSTALL_DIR% is not in your PATH.
    echo Add it by running:
    echo   setx PATH "%%PATH%%;%INSTALL_DIR%"
)

echo.
echo === Installed successfully ===
echo   %INSTALL_DIR%\%BINARY_NAME%
echo.
echo Run 'crude' ^(or 'crude-beta'^) to get started.

endlocal
