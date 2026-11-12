@echo off
setlocal
cd /d "%~dp0"

if /I "%~1"=="--open-ui" goto open_ui

rem If the relay already answers, only open the dashboard.
if exist "shunt.exe" (
  shunt.exe ui >nul 2>&1
  if not errorlevel 1 (
    echo [shunt] Le relais est deja actif. Ouverture de l'interface...
    shunt.exe ui --webview
    exit /b 0
  )
)

if not exist "shunt.exe" goto need_go
where go >nul 2>&1
if errorlevel 1 (
  echo [shunt] Go n'est pas installe; utilisation du shunt.exe existant.
  goto after_build
)

echo [shunt] Compilation de shunt.exe...
set "CGO_ENABLED=0"
go build -trimpath -ldflags="-s -w" -o shunt.exe .\cmd\shunt
if not errorlevel 1 goto after_build
if exist "shunt.exe" (
  echo [shunt] Compilation echouee; utilisation du binaire existant.
  goto after_build
)
echo [shunt] La compilation a echoue et aucun binaire n'est disponible.
pause
exit /b 1

:need_go
echo [shunt] shunt.exe est absent et Go n'est pas installe.
echo Installez Go avec: winget install GoLang.Go
echo Ou utilisez le script POSIX: ./start-shunt.sh
pause
exit /b 1

:after_build
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
for /l %%i in (1,1,25) do (
  shunt.exe ui --webview >nul 2>&1
  if not errorlevel 1 exit /b 0
  ping -n 2 127.0.0.1 >nul
)
exit /b 1
