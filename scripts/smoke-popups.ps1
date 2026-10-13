[CmdletBinding()]
param([switch]$SkipBuild, [string]$Scale = '1')

$ErrorActionPreference = 'Stop'
$aiRepository = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'go-env.ps1')
$smokeTools = Join-Path $aiRepository 'artifacts\tools\window-smoke'
[System.IO.Directory]::CreateDirectory($smokeTools) | Out-Null
$executable = Join-Path $smokeTools 'STT-widget Window Smoke.exe'
if (-not $SkipBuild) {
    Push-Location $aiRepository
    try {
        & go build -mod=readonly -trimpath -p 4 -tags=windowsmoke -ldflags '-H windowsgui' -o $executable ./scripts/smoke-window.go
        if ($LASTEXITCODE -ne 0) { throw 'Falha ao compilar o smoke de menus.' }
    } finally { Pop-Location }
}
if (-not (Test-Path -LiteralPath $executable)) { throw 'Compile o smoke antes de usar -SkipBuild.' }
$checkDirectory = Join-Path $aiRepository ('artifacts\validation\popups-' + [guid]::NewGuid().ToString('N'))
[System.IO.Directory]::CreateDirectory($checkDirectory) | Out-Null
$utf8 = [System.Text.UTF8Encoding]::new($false)

if (-not ('AITalkerPopups.Native' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace AITalkerPopups {
    public static class Native {
        [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr GetProp(IntPtr window, string name);
        [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
        [DllImport("user32.dll")] public static extern bool IsZoomed(IntPtr window);
    }
}
'@
}

function Read-State([scriptblock]$Accept = { param($state) $true }) {
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    do {
        $path = Join-Path $checkDirectory 'window-state.json'
        if (Test-Path -LiteralPath $path) {
            try {
                $sharing = [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete
                $reader = [System.IO.StreamReader]::new([System.IO.File]::Open(
                    $path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, $sharing
                ))
                try {
                    $state = $reader.ReadToEnd() | ConvertFrom-Json
                    if (& $Accept $state) { return $state }
                } finally { $reader.Dispose() }
            } catch [System.IO.IOException] {
                # The application atomically replaces this file; a Windows
                # reader can briefly encounter the replacement's open handle.
            }
        }
        $process.Refresh()
        if ($process.HasExited) { throw "O smoke encerrou: $($process.ExitCode)." }
        Start-Sleep -Milliseconds 100
    } while ($limit.Elapsed.TotalSeconds -lt 30)
    throw 'O canvas nativo não chegou ao estado esperado.'
}

function Send-Command($Command) {
    [System.IO.File]::WriteAllText(
        (Join-Path $checkDirectory 'window-command.json'),
        ($Command | ConvertTo-Json -Compress), $utf8
    )
}

function Assert-Size($Before, $After, [string]$Action) {
    $measurement = [pscustomobject]@{
        Action = $Action
        BeforeWidth = $Before.CanvasWidth
        BeforeHeight = $Before.CanvasHeight
        AfterWidth = $After.CanvasWidth
        AfterHeight = $After.CanvasHeight
    }
    $measurements.Add($measurement)
    $measurements | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $checkDirectory 'measurements.json') -Encoding utf8
    if ([math]::Abs($Before.CanvasWidth - $After.CanvasWidth) -gt 1 -or
        [math]::Abs($Before.CanvasHeight - $After.CanvasHeight) -gt 1) {
        throw ("$Action redimensionou a janela: {0}x{1} -> {2}x{3}. Evidências: {4}" -f
            $Before.CanvasWidth, $Before.CanvasHeight, $After.CanvasWidth, $After.CanvasHeight, $checkDirectory)
    }
}

function Capture-State([string]$Name) {
    Send-Command @{capture=$Name}
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    $path = Join-Path $checkDirectory ($Name + '.png')
    while (-not (Test-Path -LiteralPath $path) -or (Get-Item -LiteralPath $path).Length -eq 0) {
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw "A captura $Name não foi produzida." }
        Start-Sleep -Milliseconds 100
    }
}

function Test-Popups([string]$Context, [int]$Iterations) {
    foreach ($iteration in 1..$Iterations) {
        foreach ($action in @('groups', 'row', 'group-context', 'rename', 'settings', 'close-tab', 'close-group')) {
            $before = Read-State
            Send-Command @{open=$action}
            $state = Read-State { param($value) $value.Overlays -gt 0 }
            Start-Sleep -Milliseconds 400
            $state = Read-State
            Assert-Size $before $state "$Context, $action, abertura $iteration"
            if ($Context -eq 'minimum' -and $action -eq 'row') { Capture-State 'row-menu' }
            if ($Context -eq 'minimum' -and $action -in @('group-context', 'rename', 'settings', 'close-tab', 'close-group')) {
                Capture-State $action
            }
            if ($action -eq 'settings' -and $iteration -eq 1) {
                foreach ($section in 0..7) {
                    Send-Command @{settings_section=$section}
                    Start-Sleep -Milliseconds 500
                    $state = Read-State
                    Assert-Size $before $state "$Context, seção de Configurações $section"
                }
            }
            Send-Command @{close_menu=$true; close_settings=$true; close_dialog=$true}
            $state = Read-State { param($value) $value.Overlays -eq 0 -and -not $value.SettingsOpen }
            Start-Sleep -Milliseconds 300
            $state = Read-State
            Assert-Size $before $state "$Context, $action, fechamento $iteration"
        }
    }
    Write-Host "$Context`: tamanho preservado ao abrir/fechar menus e diálogos."
}

$process = $null
$measurements = [System.Collections.Generic.List[object]]::new()
$previousScale = $env:FYNE_SCALE
$env:FYNE_SCALE = $Scale
try {
    $process = Start-Process -FilePath $executable -ArgumentList ('"' + $checkDirectory + '"') `
        -WorkingDirectory $aiRepository -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $checkDirectory 'stdout.log') `
        -RedirectStandardError (Join-Path $checkDirectory 'stderr.log')
    $state = Read-State
    $process.Refresh()
    $window = $process.MainWindowHandle
    [uint32]$owner = 0
    [AITalkerPopups.Native]::GetWindowThreadProcessId($window, [ref]$owner) | Out-Null
    if ($window -eq [IntPtr]::Zero -or $owner -ne $process.Id) { throw 'A janela não pertence ao smoke.' }
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    while ([AITalkerPopups.Native]::GetProp($window, 'AI-Talker.IntegratedFrame') -eq [IntPtr]::Zero) {
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw 'A moldura integrada não foi aplicada.' }
        Start-Sleep -Milliseconds 100
    }
    Start-Sleep -Milliseconds 400
    $state = Read-State
    Send-Command @{hover=$true}
    Start-Sleep -Milliseconds 600
    $after = Read-State
    Assert-Size $state $after 'Tooltip de Configurações'
    Test-Popups 'initial' 3
    Send-Command @{resize=@{Width=320; Height=700}}
    $state = Read-State { param($value) [math]::Abs($value.CanvasWidth - 320) -lt 1 -and [math]::Abs($value.CanvasHeight - 700) -lt 1 }
    Capture-State 'minimum'
    Test-Popups 'minimum' 1
    Start-Sleep -Milliseconds 1000
    [AITalkerPopups.Native]::PostMessage($window, 0x0112, [IntPtr]0xf030, [IntPtr]::Zero) | Out-Null
    $state = Read-State { param($value) $value.CanvasWidth -gt 800 }
    if (-not [AITalkerPopups.Native]::IsZoomed($window)) { throw 'A janela não maximizou.' }
    Test-Popups 'maximized' 1
    Write-Host "Menus e Configurações mantiveram o tamanho da janela. Evidências: $checkDirectory"
} finally {
    if ($null -ne $process) {
        $process.Refresh()
        if (-not $process.HasExited) {
            # This process owns only synthetic test data.
            $process.Kill()
            $process.WaitForExit(5000) | Out-Null
        }
        $process.Dispose()
    }
    if ($null -eq $previousScale) {
        Remove-Item Env:FYNE_SCALE -ErrorAction SilentlyContinue
    } else { $env:FYNE_SCALE = $previousScale }
}
