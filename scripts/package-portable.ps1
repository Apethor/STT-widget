[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$ReleaseArchive,
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[a-fA-F0-9]{64}$')]
    [string]$ExpectedSHA256,
    [string]$OutputArchive
)

$ErrorActionPreference = 'Stop'
$aiRepository = Split-Path -Parent $PSScriptRoot
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Read-ArchiveFiles([string]$Path) {
    $archive = [System.IO.Compression.ZipFile]::OpenRead($Path)
    $files = [System.Collections.Generic.Dictionary[string, object]]::new([System.StringComparer]::OrdinalIgnoreCase)
    $hash = [System.Security.Cryptography.SHA256]::Create()
    try {
        foreach ($entry in $archive.Entries) {
            $name = $entry.FullName.Replace('\', '/')
            if ($name.StartsWith('/') -or $name.Contains(':') -or
                @($name.Split('/') | Where-Object { $_ -eq '..' -or $_ -eq '.' }).Count -gt 0) {
                throw "O ZIP contem um caminho invalido: $name."
            }
            if ($name.EndsWith('/')) { continue }
            if ($files.ContainsKey($name)) { throw "O ZIP contem uma entrada duplicada: $name." }
            $stream = $entry.Open()
            try {
                $digest = [System.BitConverter]::ToString($hash.ComputeHash($stream)).Replace('-', '').ToLowerInvariant()
            } finally {
                $stream.Dispose()
            }
            $files.Add($name, [pscustomobject]@{ path = $name; bytes = $entry.Length; sha256 = $digest })
        }
    } finally {
        $hash.Dispose()
        $archive.Dispose()
    }
    return ,$files
}

if (-not [System.IO.Path]::IsPathRooted($ReleaseArchive)) {
    $ReleaseArchive = Join-Path $aiRepository $ReleaseArchive
}
$ReleaseArchive = (Get-Item -LiteralPath $ReleaseArchive).FullName
$sourceHash = (Get-FileHash -LiteralPath $ReleaseArchive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($sourceHash -ne $ExpectedSHA256.ToLowerInvariant()) {
    throw 'O SHA256 da release nao corresponde ao informado. Nenhum pacote foi criado.'
}
$sourceName = [System.IO.Path]::GetFileName($ReleaseArchive)
if ($sourceName -notmatch '^STT-widget-(?<version>\d+\.\d+\.\d+)-windows-amd64-(?<revision>[a-fA-F0-9]+)\.zip$') {
    throw 'Informe um ZIP de release Windows AMD64 identificado por versao e revisao.'
}
$version, $revision = $Matches.version, $Matches.revision.ToLowerInvariant()
$sourceFiles = Read-ArchiveFiles $ReleaseArchive
foreach ($required in @('STT-widget.exe', 'stt-widget-cli.exe', 'tools/ffmpeg.exe', 'tools/ffprobe.exe', 'LEIA-ME.md')) {
    if (-not $sourceFiles.ContainsKey($required)) { throw "A release esta incompleta: falta $required." }
}
foreach ($name in $sourceFiles.Keys) {
    if ($name -notin @('STT-widget.exe', 'stt-widget-cli.exe', 'LEIA-ME.md') -and
        -not $name.StartsWith('tools/', [System.StringComparison]::OrdinalIgnoreCase) -and
        -not $name.StartsWith('licenses/', [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "A release contem um arquivo fora da distribuicao: $name. Dados de usuario nao sao empacotados."
    }
}

if (-not $OutputArchive) {
    $OutputArchive = Join-Path $aiRepository "dist\STT-widget-$version-portable-windows-amd64-$revision.zip"
} elseif (-not [System.IO.Path]::IsPathRooted($OutputArchive)) {
    $OutputArchive = Join-Path $aiRepository $OutputArchive
}
$OutputArchive = [System.IO.Path]::GetFullPath($OutputArchive)
if (Test-Path -LiteralPath $OutputArchive) {
    throw "O pacote ja existe e sera preservado: $OutputArchive."
}
$outputParent = Split-Path -Parent $OutputArchive
New-Item -ItemType Directory -Path $outputParent -Force | Out-Null
$runID = [guid]::NewGuid().ToString('N')
$staging = Join-Path $aiRepository "artifacts\releases\portable-$version-$revision-$runID"
New-Item -ItemType Directory -Path $staging | Out-Null
Expand-Archive -LiteralPath $ReleaseArchive -DestinationPath $staging

$additions = @(
    @{ source = Join-Path $PSScriptRoot 'portable\Abrir STT-widget Portable.vbs'; target = 'Abrir STT-widget Portable.vbs' },
    @{ source = Join-Path $aiRepository 'docs\PORTABLE-WINDOWS.md'; target = 'COMECE-AQUI.md' },
    @{ source = Join-Path $aiRepository 'tests\fixtures\speech.wav'; target = 'exemplos\voz-sintetica.wav' }
)
$expected = [System.Collections.Generic.Dictionary[string, object]]::new([System.StringComparer]::OrdinalIgnoreCase)
foreach ($file in $sourceFiles.Values) { $expected.Add($file.path, $file) }
foreach ($addition in $additions) {
    $destination = Join-Path $staging $addition.target
    New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
    Copy-Item -LiteralPath $addition.source -Destination $destination
    $details = Get-Item -LiteralPath $destination
    $name = $addition.target.Replace('\', '/')
    $expected.Add($name, [pscustomobject]@{
        path = $name
        bytes = $details.Length
        sha256 = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
    })
}

Compress-Archive -Path (Join-Path $staging '*') -DestinationPath $OutputArchive -CompressionLevel Optimal
$packaged = Read-ArchiveFiles $OutputArchive
if ($packaged.Count -ne $expected.Count) {
    throw 'A quantidade de arquivos do ZIP portatil nao corresponde a distribuicao preparada.'
}
foreach ($name in $expected.Keys) {
    if (-not $packaged.ContainsKey($name) -or $packaged[$name].bytes -ne $expected[$name].bytes -or
        $packaged[$name].sha256 -ne $expected[$name].sha256) {
        throw "A verificacao do arquivo empacotado falhou: $name."
    }
}

$archiveDetails = Get-Item -LiteralPath $OutputArchive
$archiveHash = (Get-FileHash -LiteralPath $OutputArchive -Algorithm SHA256).Hash.ToLowerInvariant()
$validationDirectory = Join-Path $aiRepository 'artifacts\validation'
New-Item -ItemType Directory -Path $validationDirectory -Force | Out-Null
$evidence = Join-Path $validationDirectory "portable-package-$version-$revision-$runID.json"
$record = [ordered]@{
    checked_utc = [DateTime]::UtcNow.ToString('o')
    archive = $OutputArchive
    bytes = $archiveDetails.Length
    sha256 = $archiveHash
    version = $version
    binary_revision = $revision
    source_archive = $ReleaseArchive
    source_sha256 = $sourceHash
    preserved_release_files = $sourceFiles.Count
    verified_files = $packaged.Count
    initial_data_included = $false
    staging_directory = $staging
    files = @($packaged.Values | Sort-Object path)
}
$utf8 = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText($evidence, ($record | ConvertTo-Json -Depth 6), $utf8)

[pscustomobject]@{
    archive = $OutputArchive
    bytes = $archiveDetails.Length
    sha256 = $archiveHash
    preserved_release_files = $sourceFiles.Count
    verified_files = $packaged.Count
    staging_directory = $staging
    evidence = $evidence
}
