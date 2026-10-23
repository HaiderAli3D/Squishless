@echo off
rem Starts Squishless and opens it in the default browser (http://127.0.0.1:7878).
cd /d "%~dp0"

set "GO=go"
where go >nul 2>nul || set "GO=%USERPROFILE%\.local\gosdk\go\bin\go.exe"
if not "%GO%"=="go" if not exist "%GO%" (
    echo Go is required to run Squishless: https://go.dev/dl/
    pause
    exit /b 1
)

"%GO%" run ./cmd/squishless %*
if errorlevel 1 pause
