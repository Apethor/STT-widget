param(
    [Parameter(Mandatory=$true, Position=0)][string]$Audio,
    [string]$Perfil,
    [string]$PastaSaida = (Join-Path (Split-Path -Parent $PSScriptRoot) 'transcricoes'),
    [switch]$Copiar,
    [string]$Markdown,
    [switch]$Json
)

$ErrorActionPreference = 'Stop'
$projectDirectory = Split-Path -Parent $PSScriptRoot
$resolvedAudio = (Resolve-Path -LiteralPath $Audio).Path
$appArguments = @('transcribe', $resolvedAudio, '--output-dir', $PastaSaida)
if ($Perfil) { $appArguments += @('--profile', $Perfil) }
if ($Copiar) { $appArguments += '--copy' }
if ($Markdown) { $appArguments += @('--markdown', $Markdown) }
if ($Json) { $appArguments += '--json' }
$appGo = Join-Path $projectDirectory 'bin\stt-widget-cli.exe'
if (-not (Test-Path -LiteralPath $appGo)) {
    throw 'Compile a versão Go com .\scripts\build-go.ps1.'
}
& $appGo @appArguments
exit $LASTEXITCODE
