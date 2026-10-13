# Dot-source this file to use the portable toolchain in the current shell.
$aiRepository = Split-Path -Parent $PSScriptRoot
$aiTools = Join-Path $aiRepository 'artifacts\tools'
$aiGoBin = Join-Path $aiTools 'go\bin'
$aiGCCBin = Join-Path $aiTools 'mingw64\bin'
if (Test-Path -LiteralPath (Join-Path $aiGoBin 'go.exe')) {
    $env:PATH = "$aiGoBin;$env:PATH"
}
if (Test-Path -LiteralPath (Join-Path $aiGCCBin 'gcc.exe')) {
    $env:PATH = "$aiGCCBin;$env:PATH"
}
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '1'
$aiFFmpegBin = Join-Path $aiTools 'ffmpeg\ffmpeg-9.0.2-essentials_build\bin'
if (-not $env:AI_TALKER_FFMPEG -and (Test-Path -LiteralPath (Join-Path $aiFFmpegBin 'ffmpeg.exe'))) {
    $env:AI_TALKER_FFMPEG = Join-Path $aiFFmpegBin 'ffmpeg.exe'
}
if (-not $env:AI_TALKER_FFPROBE -and (Test-Path -LiteralPath (Join-Path $aiFFmpegBin 'ffprobe.exe'))) {
    $env:AI_TALKER_FFPROBE = Join-Path $aiFFmpegBin 'ffprobe.exe'
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Prepare Go com .\scripts\prepare-go.ps1.'
}
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    throw 'Prepare o compilador C com .\scripts\prepare-go.ps1.'
}
