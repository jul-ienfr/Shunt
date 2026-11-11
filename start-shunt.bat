@echo off
setlocal
cd /d "%~dp0"

if /I "%~1"=="--open-ui" goto open_ui

if not exist "shunt.exe" (
  where go >nul 2>&1
  if errorlevel 1 (
    echo [shunt] shunt.exe est absent et Go n'est pas installe.
    echo Installez Go avec: winget install GoLang.Go
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
)

shunt.exe config validate >nul 2>&1
if errorlevel 1 (
  echo [shunt] Premier lancement: creation de la configuration par defaut...
  shunt.exe init
  if errorlevel 1 (
    echo [shunt] Impossible d'initialiser la configuration.
    echo Lancez: shunt.exe config validate
    pause
    exit /b 1
  )
)

shunt.exe ui >nul 2>&1
if not errorlevel 1 (
  echo [shunt] Le relais est deja actif. Ouverture de l'interface...
  shunt.exe ui --webview
  exit /b 0
)

echo [shunt] Demarrage du relais... (Ctrl+C pour arreter)
start "shunt UI" /min "%~f0" --open-ui
shunt.exe serve
set "CODE=%ERRORLEVEL%"
if not "%CODE%"=="0" (
  echo [shunt] Le relais s'est arrete avec le code %CODE%.
  pause
)
exit /b %CODE%

:open_ui
for /l %%i in (1,1,20) do (
  shunt.exe ui --webview >nul 2>&1
  if not errorlevel 1 exit /b 0
  ping -n 2 127.0.0.1 >nul
)
exit /b 1
