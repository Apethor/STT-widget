@echo off
setlocal
pushd "%~dp0.."
if exist "bin\STT-widget.exe" (
    start "" "bin\STT-widget.exe"
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0iniciar.ps1"
)
popd
