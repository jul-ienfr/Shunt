@echo off
setlocal
cd /d "%~dp0"

where go >nul 2>&1
if errorlevel 1 (
  echo [shunt] Go n'est pas installe. Installez-le avec: winget install GoLang.Go
  pause
  exit /b 1
)

echo [shunt] Compilation de shunt.exe...
set "CGO_ENABLED=0"
go build -trimpath -ldflags="-s -w" -o shunt.exe .\cmd\shunt
if errorlevel 1 (
  echo [shunt] La compilation a echoue.
  pause
  exit /b 1
)

echo [shunt] shunt.exe est a jour.
exit /b 0
