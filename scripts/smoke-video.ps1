[CmdletBinding()]
param([switch]$SkipBuild, [switch]$UseGoRun)

$ErrorActionPreference = 'Stop'
$aiRepository = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'go-env.ps1')
$videoTools = Join-Path $aiRepository 'artifacts\tools\window-smoke'
[System.IO.Directory]::CreateDirectory($videoTools) | Out-Null
$harnessExecutable = Join-Path $videoTools 'STT-widget Video Window Smoke.exe'
$videoExecutable = Join-Path $videoTools 'STT-widget Video Proof.exe'
if (-not $SkipBuild) {
    Push-Location $aiRepository
    try {
        & go build -mod=readonly -trimpath -p 4 -tags=windowsmoke -ldflags '-H windowsgui' -o $harnessExecutable ./scripts/smoke-window.go
        if ($LASTEXITCODE -ne 0) { throw 'Falha ao compilar o harness da prova de vídeo.' }
        if (-not $UseGoRun) {
            & go build -mod=readonly -trimpath -p 4 -ldflags '-H windowsgui' -o $videoExecutable ./scripts/smoke-video.go
            if ($LASTEXITCODE -ne 0) { throw 'Falha ao compilar o helper da prova de vídeo.' }
        }
    } finally { Pop-Location }
}
$videoEvidence = Join-Path $aiRepository ('artifacts\validation\video-gdigrab-' + [guid]::NewGuid().ToString('N'))
$videoDirectory = Join-Path $videoEvidence 'capture'
[System.IO.Directory]::CreateDirectory($videoEvidence) | Out-Null
[System.IO.Directory]::CreateDirectory($videoDirectory) | Out-Null
$videoUTF8 = [System.Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName System.Drawing
if (-not ('AITalkerVideoProof.Native' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Text;
using System.Runtime.InteropServices;
namespace AITalkerVideoProof {
    public static class Native {
        public delegate bool EnumProc(IntPtr window, IntPtr state);
        [StructLayout(LayoutKind.Sequential)] public struct Point { public int X, Y; }
        [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
        [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc callback, IntPtr state);
        [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr window, StringBuilder text, int count);
        [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr window, ref Point point);
        [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr window, out Rect rectangle);
        [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr window, IntPtr order, int x, int y, int width, int height, uint flags);
        [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
        [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
        [DllImport("user32.dll")] public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
        [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wp, IntPtr lp);
        public static IntPtr Find(uint pid, string title) {
            IntPtr found = IntPtr.Zero;
            EnumWindows((window, _) => {
                uint owner; GetWindowThreadProcessId(window, out owner);
                if (owner != pid) return true;
                var text = new StringBuilder(512);
                GetWindowText(window, text, text.Capacity);
                if (text.ToString() == title) { found = window; return false; }
                return true;
            }, IntPtr.Zero);
            return found;
        }
        public static Point Origin(IntPtr window) {
            var point = new Point();
            if (!ClientToScreen(window, ref point)) throw new Exception("ClientToScreen falhou.");
            return point;
        }
    }
}
'@
}

function Video-State([scriptblock]$Accept = { param($value) $true }) {
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    do {
        $path = Join-Path $videoEvidence 'window-state.json'
        if (Test-Path -LiteralPath $path) {
            try {
                $sharing = [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete
                $reader = [System.IO.StreamReader]::new([System.IO.File]::Open(
                    $path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, $sharing))
                try {
                    $value = $reader.ReadToEnd() | ConvertFrom-Json
                    if (& $Accept $value) { return $value }
                } finally { $reader.Dispose() }
            } catch [System.IO.IOException] {}
        }
        if ($harness.HasExited) { throw 'O harness encerrou durante a prova de vídeo.' }
        if ($limit.Elapsed.TotalSeconds -gt 30) { throw "O harness não chegou ao estado pedido. Evidências: $videoEvidence" }
        Start-Sleep -Milliseconds 80
    } while ($true)
}

function Video-Theme([string]$Theme) {
    $beforeFrame = (Video-State).Frame
    [System.IO.File]::WriteAllText((Join-Path $videoEvidence 'window-command.json'),
        (@{theme=$Theme} | ConvertTo-Json -Compress), $videoUTF8)
    return Video-State { param($value) $value.Theme -eq $Theme -and $value.Frame -ge $beforeFrame+3 }
}

function Video-Reference([string]$Name) {
    $pixels = [System.Drawing.Bitmap]::new($videoRegion.Width, $videoRegion.Height)
    $graphics = [System.Drawing.Graphics]::FromImage($pixels)
    try {
        $graphics.CopyFromScreen($videoRegion.X, $videoRegion.Y, 0, 0, $pixels.Size)
        $path = Join-Path $videoEvidence "$Name.png"
        $pixels.Save($path)
        return $path
    } finally { $graphics.Dispose(); $pixels.Dispose() }
}

$harness, $helper = $null, $null
$videoDPI = [AITalkerVideoProof.Native]::SetThreadDpiAwarenessContext([IntPtr](-4))
try {
    $harness = Start-Process -FilePath $harnessExecutable -ArgumentList ('"' + $videoEvidence + '"') `
        -WorkingDirectory $aiRepository -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $videoEvidence 'harness-stdout.log') `
        -RedirectStandardError (Join-Path $videoEvidence 'harness-stderr.log')
    $state = Video-State
    $window = [AITalkerVideoProof.Native]::Find($harness.Id, 'STT-widget')
    if ($window -eq [IntPtr]::Zero) {
        $window = [AITalkerVideoProof.Native]::Find($harness.Id, ('STT-widget ' + $state.Backlog.Version))
    }
    if ($window -eq [IntPtr]::Zero) { throw 'A janela do PID do harness não foi encontrada.' }
    [AITalkerVideoProof.Native]::SetWindowPos($window, [IntPtr](-1), 100, 60, 0, 0, 0x0011) | Out-Null
    [AITalkerVideoProof.Native]::SetForegroundWindow($window) | Out-Null
    $state = Video-Theme 'dark'
    Start-Sleep -Milliseconds 350
    $origin = [AITalkerVideoProof.Native]::Origin($window)
    $client = [AITalkerVideoProof.Native+Rect]::new()
    if (-not [AITalkerVideoProof.Native]::GetClientRect($window, [ref]$client)) { throw 'Não foi possível medir o client do harness.' }
    $videoRegion = @{
        X=$origin.X+[int](($state.RecordPanel.X+25)*$state.Scale)
        Y=$origin.Y+[int](($state.RecordPanel.Y+60)*$state.Scale)
        Width=200
        Height=120
    }
    if ($videoRegion.X+$videoRegion.Width -gt $origin.X+$client.Right -or
        $videoRegion.Y+$videoRegion.Height -gt $origin.Y+$client.Bottom) {
        throw 'O retângulo solicitado saiu do client do harness.'
    }
    [AITalkerVideoProof.Native]::SetCursorPos($origin.X+$client.Right-12, $origin.Y+12) | Out-Null
    $before = Video-Reference 'before-dark'
    $after = Join-Path $videoEvidence 'after-light.png'
    $requestPath = Join-Path $videoEvidence 'video-request.json'
    [System.IO.File]::WriteAllText($requestPath,
        (@{Directory=$videoDirectory; HarnessPID=$harness.Id; Window=$window.ToInt64();
            Region=$videoRegion; Before=$before; After=$after} | ConvertTo-Json -Depth 4), $videoUTF8)
    $helperArguments = @('-request', ('"' + $requestPath + '"'))
    if ($UseGoRun) {
        $videoExecutable = (Get-Command go -ErrorAction Stop).Source
        $helperArguments = @('run', '-mod=readonly', '-trimpath', '-p', '4', '-ldflags',
            '"-H windowsgui"', './scripts/smoke-video.go', '-request', ('"' + $requestPath + '"'))
    }
    [System.IO.File]::WriteAllText((Join-Path $videoEvidence 'execution-mode.json'),
        (@{GoRun=[bool]$UseGoRun; Executable=$videoExecutable} | ConvertTo-Json -Compress), $videoUTF8)
    $helper = Start-Process -FilePath $videoExecutable -ArgumentList $helperArguments `
        -WorkingDirectory $aiRepository -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $videoEvidence 'video-stdout.log') `
        -RedirectStandardError (Join-Path $videoEvidence 'video-stderr.log')
    $phaseLimit = [System.Diagnostics.Stopwatch]::StartNew()
    $phaseDeadlineSeconds = 30
    if ($UseGoRun) { $phaseDeadlineSeconds = 120 }
    do {
        $phasePath = Join-Path $videoDirectory 'video-phase.json'
        if (Test-Path -LiteralPath $phasePath) {
            $phase = Get-Content -LiteralPath $phasePath -Raw | ConvertFrom-Json
            if ($phase.phase -eq 'paused') { break }
        }
        if ($helper.HasExited) { throw "O helper de vídeo encerrou antes da pausa. Evidências: $videoEvidence" }
        if ($phaseLimit.Elapsed.TotalSeconds -gt $phaseDeadlineSeconds) { throw "A prova de vídeo não chegou à pausa. Evidências: $videoEvidence" }
        Start-Sleep -Milliseconds 80
    } while ($true)
    $state = Video-Theme 'light'
    Start-Sleep -Milliseconds 350
    $null = Video-Reference 'after-light'
    [System.IO.File]::WriteAllText((Join-Path $videoDirectory 'video-resume.json'), '{"Resume":true}', $videoUTF8)
    if (-not $helper.WaitForExit(60000) -or $helper.ExitCode -ne 0) {
        throw "A prova de vídeo falhou após a retomada. Evidências: $videoEvidence"
    }
    $result = Get-Content -LiteralPath (Join-Path $videoDirectory 'video-result.json') -Raw | ConvertFrom-Json
    if ($result.HashBefore -ne $result.HashAfter -or -not $result.ResumeContextCancelled -or
        [Math]::Abs($result.AudioSeconds-2.4) -gt 0.001 -or [Math]::Abs($result.MP4AudioSeconds-2.4) -gt 0.12 -or
        [Math]::Abs($result.VideoSeconds-2.4) -gt (1/30) -or $result.VideoFrames -ne 72 -or
        $result.SceneDifference -lt 30 -or $result.BeforeError -gt 12 -or $result.AfterError -gt 12 -or
        -not $result.OutlineAbsentFromSavedFrames -or $result.OutlineBoundaryError -gt 12 -or
        -not $result.InnerOutlineExcluded -or $result.InnerOutlineBoundaryError -gt 12) {
        throw 'O resultado não comprova WAV preservado, pausa/contexto, quadros e duração A/V.'
    }
    Write-Host "Gdigrab real: retângulo restrito ao PID $($harness.Id), duas cenas/tons alinhados, pausa/contexto, borda ausente dos pixels salvos e hash WAV passaram. Evidências: $videoEvidence"
    [AITalkerVideoProof.Native]::PostMessage($window, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
    if (-not $harness.WaitForExit(10000) -or $harness.ExitCode -ne 0) { throw 'O harness de vídeo não encerrou normalmente.' }
} finally {
    if ($null -ne $helper) {
        $helper.Refresh()
        if (-not $helper.HasExited) {
            [System.IO.File]::WriteAllText((Join-Path $videoDirectory 'video-abort.json'), '{"abort":true}', $videoUTF8)
            $helper.WaitForExit(15000) | Out-Null
        }
    }
    foreach ($ownedProcess in @($helper, $harness)) {
        if ($null -ne $ownedProcess) {
            $ownedProcess.Refresh()
            if (-not $ownedProcess.HasExited) {
                $ownedProcess.Kill()
                $ownedProcess.WaitForExit(5000) | Out-Null
            }
            $ownedProcess.Dispose()
        }
    }
    if ($videoDPI -ne [IntPtr]::Zero) {
        [AITalkerVideoProof.Native]::SetThreadDpiAwarenessContext($videoDPI) | Out-Null
    }
}
