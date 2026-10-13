[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$aiRepository = Split-Path -Parent $PSScriptRoot
$aiTools = Join-Path $aiRepository 'artifacts\tools'
$manifest = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'toolchain.json') -Raw | ConvertFrom-Json
New-Item -ItemType Directory -Path $aiTools -Force | Out-Null
# A nested module keeps downloaded toolchain sources out of the app's ./... scan.
$artifactModule = Join-Path $aiRepository 'artifacts\go.mod'
if (-not (Test-Path -LiteralPath $artifactModule)) {
    "module ai-talker-artifacts`n`ngo $($manifest.go.version)`n" |
        Set-Content -LiteralPath $artifactModule -Encoding ASCII
}

foreach ($name in @('go', 'gcc', 'ffmpeg')) {
    $tool = $manifest.$name
    $destination = $aiTools
    if ($name -eq 'ffmpeg') {
        $destination = Join-Path $aiTools 'ffmpeg'
        New-Item -ItemType Directory -Path $destination -Force | Out-Null
    }
    $archive = Join-Path $destination $tool.archive
    if (-not (Test-Path -LiteralPath $archive)) {
        Write-Host "Baixando $name $($tool.version)..."
        Invoke-WebRequest -Uri $tool.url -OutFile $archive -UseBasicParsing
    }
    $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($hash -ne $tool.sha256) {
        throw "O SHA256 de $archive difere do registrado em scripts/toolchain.json."
    }
    $toolDirectory = Join-Path $aiTools $tool.folder
    $marker = Join-Path $toolDirectory '.ai-talker-verified'
    $isVerified = (Test-Path -LiteralPath $marker) -and (
        (Get-Content -LiteralPath $marker -Raw).Trim() -eq $tool.sha256
    )
    if (-not $isVerified) {
        Expand-Archive -LiteralPath $archive -DestinationPath $destination -Force
        $tool.sha256 | Set-Content -LiteralPath $marker -Encoding ASCII
    }
    Write-Host "$name $($tool.version): arquivo verificado."
}

. (Join-Path $PSScriptRoot 'go-env.ps1')
& go version
if ($LASTEXITCODE -ne 0) { throw 'Não foi possível executar Go.' }
& gcc --version | Select-Object -First 1
if ($LASTEXITCODE -ne 0) { throw 'Não foi possível executar GCC.' }
& $env:AI_TALKER_FFMPEG -version | Select-Object -First 1
if ($LASTEXITCODE -ne 0) { throw 'Não foi possível executar FFmpeg.' }
