@echo off
cd /d "%~dp0"

title Eleball E2E Server
set PORT=8080

:: Auto-detect Go executable
set "GOEXE="
if exist "J:\workspace\Prj\tools\go\bin\go.exe" (
    set "GOEXE=J:\workspace\Prj\tools\go\bin\go.exe"
    goto :found
)
where go >nul 2>nul
if %errorlevel% equ 0 (
    for /f "delims=" %%i in ('where go') do (
        set "GOEXE=%%i"
        goto :found
    )
)
if exist "C:\Program Files\Go\bin\go.exe" (
    set "GOEXE=C:\Program Files\Go\bin\go.exe"
    goto :found
)

echo [ERROR] Go not found.
echo Please set GOEXE in this script to your go.exe path, or add Go to PATH.
pause
exit /b 1

:found
echo [INFO] Using Go: %GOEXE%

:: Build
echo.
echo ==========================================
echo   Building Eleball E2E Server...
echo ==========================================
echo.

"%GOEXE%" build -o e2e-server.exe . 2>build-error.log
if %errorlevel% neq 0 (
    echo [ERROR] Build failed. See build-error.log:
    type build-error.log
    pause
    exit /b 1
)
del build-error.log 2>nul

:: Start
echo.
echo ==========================================
echo   Eleball E2E Server Started
 echo ==========================================
echo   API:     http://localhost:%PORT%
echo   Health:  http://localhost:%PORT%/health
echo   Admin:   http://localhost:%PORT%/admin/
echo.
echo   Android Emulator: http://10.0.2.2:%PORT%
echo   Android Real Device: http://YOUR_PC_IP:%PORT%
echo.
echo   Env vars: OPENAI_API_KEY, DEEPSEEK_API_KEY
echo ==========================================
echo.
echo Press Ctrl+C to stop
echo.

e2e-server.exe
