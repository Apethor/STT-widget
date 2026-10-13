param([switch]$Console)

$ErrorActionPreference = 'Stop'
$projectDirectory = Split-Path -Parent $PSScriptRoot
$appGo = Join-Path $projectDirectory 'bin\STT-widget.exe'
if (-not (Test-Path -LiteralPath $appGo)) {
    throw 'Compile a versão Go com .\scripts\prepare-go.ps1 e .\scripts\build-go.ps1.'
}
if ($Console) {
    & $appGo
} else {
    Start-Process -FilePath $appGo -WorkingDirectory $projectDirectory -WindowStyle Hidden
}
