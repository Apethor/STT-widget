[CmdletBinding()]
param(
    [switch]$Package,
    [string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'go-env.ps1')
$aiRepository = Split-Path -Parent $PSScriptRoot
$aiOutputDirectory = Join-Path $aiRepository 'bin'
if ($OutputDirectory) {
    $aiOutputDirectory = $OutputDirectory
    if (-not [System.IO.Path]::IsPathRooted($aiOutputDirectory)) {
        $aiOutputDirectory = Join-Path $aiRepository $aiOutputDirectory
    }
    $aiOutputDirectory = [System.IO.Path]::GetFullPath($aiOutputDirectory)
}
$manifest = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'toolchain.json') -Raw | ConvertFrom-Json
$version = '0.12.0'
Push-Location $aiRepository
try {
    & go mod verify
    if ($LASTEXITCODE -ne 0) { throw 'A verificação das dependências falhou.' }
    $revision = (& git rev-parse --short HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Não foi possível consultar a revisão Git.' }
    $linkFlags = "-s -w -X ai-talker/internal/version.Version=$version -X ai-talker/internal/version.Revision=$revision"
    New-Item -ItemType Directory -Path $aiOutputDirectory -Force | Out-Null
    & windres --target=pe-x86-64 --input internal/branding/windows.rc `
        --output cmd/ai-talker-gui/icon_windows_amd64.syso --output-format=coff
    if ($LASTEXITCODE -ne 0) { throw 'A preparação do ícone Windows falhou.' }
    & go build -mod=readonly -trimpath -p 4 -ldflags $linkFlags -o (Join-Path $aiOutputDirectory 'stt-widget-cli.exe') ./cmd/ai-talker
    if ($LASTEXITCODE -ne 0) { throw 'A compilação do CLI falhou.' }
    & go build -mod=readonly -trimpath -p 4 -ldflags "$linkFlags -H windowsgui" -o (Join-Path $aiOutputDirectory 'STT-widget.exe') ./cmd/ai-talker-gui
    if ($LASTEXITCODE -ne 0) { throw 'A compilação da interface falhou.' }

    $codecSource = Join-Path $aiRepository ('artifacts\tools\' + $manifest.ffmpeg.folder)
    $toolsDestination = Join-Path $aiOutputDirectory 'tools'
    New-Item -ItemType Directory -Path $toolsDestination -Force | Out-Null
    foreach ($name in @('ffmpeg.exe', 'ffprobe.exe')) {
        $source = Join-Path $codecSource ('bin\' + $name)
        if (-not (Test-Path -LiteralPath $source)) {
            throw 'Prepare os codecs de distribuição com .\scripts\prepare-go.ps1.'
        }
        Copy-Item -LiteralPath $source -Destination (Join-Path $toolsDestination $name) -Force
    }
    $licensesDestination = Join-Path $aiOutputDirectory 'licenses'
    New-Item -ItemType Directory -Path $licensesDestination -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $aiRepository 'LICENSE') -Destination (Join-Path $licensesDestination 'STT-widget-LICENSE.txt') -Force
    Copy-Item -LiteralPath (Join-Path $aiRepository 'docs\THIRD-PARTY.md') -Destination $licensesDestination -Force
    Copy-Item -LiteralPath (Join-Path $aiRepository 'internal\soundboard\assets\SOURCES.md') `
        -Destination (Join-Path $licensesDestination 'soundboard-SOURCES.md') -Force
    Copy-Item -LiteralPath (Join-Path $aiRepository 'internal\whisper\LICENSE.txt') -Destination (Join-Path $licensesDestination 'whisper.cpp-LICENSE.txt') -Force
    $goLicense = Join-Path $aiRepository 'artifacts\tools\go\LICENSE'
    if (Test-Path -LiteralPath $goLicense) {
        Copy-Item -LiteralPath $goLicense -Destination (Join-Path $licensesDestination 'Go-LICENSE.txt') -Force
    }
    $modules = & go list -mod=readonly -m -f '{{.Path}}|{{.Version}}|{{.Dir}}' all
    if ($LASTEXITCODE -ne 0) { throw 'Não foi possível consultar as licenças das dependências.' }
    $moduleVersions = foreach ($module in $modules) {
        $fields = $module.Split('|', 3)
        if ($fields.Count -eq 3 -and $fields[1]) {
            $fields[0] + ' ' + $fields[1]
        }
    }
    $moduleVersions | Set-Content -LiteralPath (Join-Path $licensesDestination 'go-modules.txt') -Encoding UTF8
    foreach ($module in $modules) {
        $fields = $module.Split('|', 3)
        if ($fields.Count -ne 3 -or -not $fields[1] -or -not $fields[2]) { continue }
        $moduleName = ($fields[0] + '@' + $fields[1]) -replace '[^a-zA-Z0-9._@-]', '_'
        $moduleDestination = Join-Path $licensesDestination ('go-modules\' + $moduleName)
        $notices = Get-ChildItem -LiteralPath $fields[2] -File -Recurse |
            Where-Object { $_.Name -match '^(LICENSE|COPYING|NOTICE)(\.|$)' }
        foreach ($notice in $notices) {
            $relative = $notice.FullName.Substring($fields[2].Length).TrimStart('\', '/')
            $target = Join-Path $moduleDestination $relative
            New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
            Copy-Item -LiteralPath $notice.FullName -Destination $target -Force
        }
    }
    foreach ($name in @('LICENSE', 'LICENSE.txt')) {
        $license = Join-Path $codecSource $name
        if (Test-Path -LiteralPath $license) {
            Copy-Item -LiteralPath $license -Destination (Join-Path $licensesDestination 'FFmpeg-LICENSE.txt') -Force
        }
    }
    Copy-Item -LiteralPath (Join-Path $aiRepository 'docs\PACOTE-WINDOWS.md') -Destination (Join-Path $aiOutputDirectory 'LEIA-ME.md') -Force
    Write-Host "Executáveis preparados em $aiOutputDirectory."
    if ($Package) {
        $packagesDirectory = Join-Path $aiRepository 'dist'
        New-Item -ItemType Directory -Path $packagesDirectory -Force | Out-Null
        $archive = Join-Path $packagesDirectory "STT-widget-$version-windows-amd64-$revision.zip"
        if (Test-Path -LiteralPath $archive) {
            throw "O pacote já existe: $archive. Preserve-o ou escolha outra revisão."
        }
        Compress-Archive -Path (Join-Path $aiOutputDirectory '*') -DestinationPath $archive -CompressionLevel Optimal
        Get-FileHash -LiteralPath $archive -Algorithm SHA256
    }
} finally {
    Pop-Location
}
