[CmdletBinding()]
param(
    [switch]$Screenshot,
    [switch]$WritingTabs,
    [switch]$WritingGroups,
    [switch]$IntegratedFrame,
    [string]$Executable,
    [ValidateSet('green', 'light', 'dark')][string]$Theme = 'dark',
    [ValidateSet('stacked', 'inline', 'column')][string]$Layout = 'column',
    [ValidateSet('large', 'medium', 'small')][string]$RecordingSize = 'large'
)

$ErrorActionPreference = 'Stop'
$aiRepository = Split-Path -Parent $PSScriptRoot
$gui = Join-Path $aiRepository 'bin\STT-widget.exe'
if ($Executable) {
    $gui = [System.IO.Path]::GetFullPath($Executable)
}
if (-not (Test-Path -LiteralPath $gui)) {
    throw 'Compile a interface com .\scripts\build-go.ps1.'
}
$checkDirectory = Join-Path $aiRepository ('artifacts\validation\desktop-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $checkDirectory -Force | Out-Null
$utf8 = New-Object System.Text.UTF8Encoding($false)
$settings = @{
    profile = 'ai-talker-smoke-' + [guid]::NewGuid().ToString('N')
    region = 'us-east-1'
    quality = 'quality'
    chunk_seconds = 300
    overlap_seconds = 2
    recorder_backend = 'winmm'
    pinned = $false
    appearance = @{ theme = $Theme; layout = $Layout; recording_size = $RecordingSize }
}
[System.IO.File]::WriteAllText((Join-Path $checkDirectory 'settings.json'), ($settings | ConvertTo-Json -Depth 4), $utf8)
$snapshot = @{
    text = "Este é um teste da interface Go.`n`nPortuguês, inglês e termos de programação: Go, AWS e Kubernetes."
    source = Join-Path $checkDirectory 'audio-sintetico.wav'
    duration_seconds = 125
    elapsed_seconds = 3
    model_id = 'mistral.voxtral-small-24b-2507'
    region = 'us-east-1'
    parts = @()
}
[System.IO.File]::WriteAllText((Join-Path $checkDirectory 'last-transcript.json'), ($snapshot | ConvertTo-Json), $utf8)
if ($WritingTabs -or $WritingGroups) {
    $processingOptions = @{
        model_id = 'global.openai.gpt-6-luna'
        mode = 'teams'
        teams_prompt = 'Prepare uma mensagem profissional para Teams, respeitando as orientacoes ditadas de destinatario e tom.'
    }
    $freeOptions = $processingOptions.Clone()
    $freeOptions.mode = 'free'
    $workspace = @{
        version = 1
        selected = 1
        next_id = 2
        tabs = @(
            @{
                id = 1
                name = 'Mensagem Mendes'
                renamed = $true
                text = 'Quero mandar uma mensagem pro Mendes num tom cordial dizendo que a revisao da proposta terminou e consigo enviar hoje ate as 15h.'
                source = ''
                processed_text = "Olá, Mendes!`n`nConcluí a revisão da proposta e consigo enviá-la hoje até as 15h."
                processing = $processingOptions
                undo = @{ text = 'Texto anterior recuperável.'; source = '' }
            },
            @{
                id = 2
                name = 'Aba 99'
                text = 'Explique este assunto em tres topicos.'
                source = ''
                processed_text = 'Resultado independente da segunda aba.'
                processing = $freeOptions
            }
        )
    }
    if ($WritingGroups) {
        $workspace.groups = @(1, 2)
        $workspace.next_group = 2
        $workspace.next_id = 4
        $workspace.selected = 3
        foreach ($tab in $workspace.tabs) { $tab.group = 1 }
        $workspace.tabs += @(
            @{
                id = 3
                group = 2
                name = 'Aba 1'
                text = 'Texto independente do segundo grupo.'
                source = ''
                processed_text = 'Resultado independente do segundo grupo.'
                processing = $freeOptions
            },
            @{
                id = 4
                group = 2
                name = 'Aba 99'
                text = 'Outra transcricao do segundo grupo.'
                source = ''
                processed_text = ''
                processing = $processingOptions
            }
        )
    }
    [System.IO.File]::WriteAllText(
        (Join-Path $checkDirectory 'workspace-go-v1.json'),
        ($workspace | ConvertTo-Json -Depth 8),
        $utf8
    )
}

if (-not ('AITalkerSmoke.Native' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace AITalkerSmoke {
    public static class Native {
        [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
        [StructLayout(LayoutKind.Sequential)] public struct Point { public int X, Y; }
        [StructLayout(LayoutKind.Sequential)] public struct MinMaxInfo {
            public Point Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize;
        }
        [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
        [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr window, out Rect rect);
        [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window, out Rect rect);
        [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr window, ref Point point);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr GetProp(IntPtr window, string name);
        [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr window);
        [DllImport("user32.dll")] public static extern bool IsZoomed(IntPtr window);
        [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr window);
        [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr window, IntPtr order, int x, int y, int width, int height, uint flags);
        [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
        [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
        [DllImport("user32.dll")] public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
        [DllImport("dwmapi.dll")] public static extern int DwmGetWindowAttribute(IntPtr window, int attribute, out Rect value, int size);
        public static Point Minimum(IntPtr window) {
            int size = Marshal.SizeOf(typeof(MinMaxInfo));
            IntPtr pointer = Marshal.AllocHGlobal(size);
            try {
                Marshal.StructureToPtr(new MinMaxInfo(), pointer, false);
                SendMessage(window, 0x0024, IntPtr.Zero, pointer);
                return Marshal.PtrToStructure<MinMaxInfo>(pointer).MinTrackSize;
            } finally { Marshal.FreeHGlobal(pointer); }
        }
        public static long HitTest(IntPtr window, int x, int y) {
            uint packed = (uint)(ushort)x | ((uint)(ushort)y << 16);
            return SendMessage(window, 0x0084, IntPtr.Zero, new IntPtr(unchecked((int)packed))).ToInt64();
        }
    }
}
'@
}

$previousDirectory = $env:AI_TALKER_DATA_DIR
$first = $null
$second = $null
try {
    # The missing, unique profile fails locally. This check never records
    # microphone audio, contacts AWS or changes the user's application data.
    $env:AI_TALKER_DATA_DIR = $checkDirectory
    $first = Start-Process -FilePath $gui -WorkingDirectory $aiRepository -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $checkDirectory 'stdout.log') `
        -RedirectStandardError (Join-Path $checkDirectory 'stderr.log')
    $timer = [System.Diagnostics.Stopwatch]::StartNew()
    $window = [IntPtr]::Zero
    while ($timer.Elapsed.TotalSeconds -lt 20) {
        $first.Refresh()
        if ($first.HasExited) {
            $diagnostics = Get-Content -LiteralPath (Join-Path $checkDirectory 'stderr.log') -Raw
            throw "A interface encerrou antes de abrir: código $($first.ExitCode). $diagnostics"
        }
        $window = $first.MainWindowHandle
        if ($window -ne [IntPtr]::Zero) { break }
        Start-Sleep -Milliseconds 100
    }
    if ($window -eq [IntPtr]::Zero) { throw 'A interface não criou uma janela em 20 segundos.' }
    [uint32]$owner = 0
    [AITalkerSmoke.Native]::GetWindowThreadProcessId($window, [ref]$owner) | Out-Null
    if ($owner -ne $first.Id) { throw 'A janela não pertence ao processo de verificação.' }
    Write-Host "Janela nativa aberta: $($first.MainWindowTitle)."

    foreach ($iconType in @(0, 1)) {
        $handle = [AITalkerSmoke.Native]::SendMessage($window, 0x007f, [IntPtr]$iconType, [IntPtr]::Zero)
        if ($handle -eq [IntPtr]::Zero) { throw "A janela não tem ícone nativo do tipo $iconType." }
    }
    Add-Type -AssemblyName System.Drawing
    Start-Sleep -Milliseconds 600
    foreach ($iconType in @(0, 1)) {
        $handle = [AITalkerSmoke.Native]::SendMessage($window, 0x007f, [IntPtr]$iconType, [IntPtr]::Zero)
        $nativeIcon = [System.Drawing.Icon]::FromHandle($handle)
        $nativeBitmap = $nativeIcon.ToBitmap()
        $expectedNative = [System.Drawing.Icon]::new(
            (Join-Path $aiRepository 'internal\branding\ai-talker.ico'), $nativeIcon.Width, $nativeIcon.Height
        )
        $expectedBitmap = $expectedNative.ToBitmap()
        try {
            for ($y = 0; $y -lt $nativeBitmap.Height; $y++) {
                for ($x = 0; $x -lt $nativeBitmap.Width; $x++) {
                    $actual = $nativeBitmap.GetPixel($x, $y)
                    $expected = $expectedBitmap.GetPixel($x, $y)
                    if ($actual.A -eq 0 -and $expected.A -eq 0) { continue }
                    if ($actual.ToArgb() -ne $expected.ToArgb()) {
                        throw "O ícone nativo $iconType não corresponde ao original do STT-widget."
                    }
                }
            }
            $nativeBitmap.Save((Join-Path $checkDirectory "native-icon-$iconType.png"), [System.Drawing.Imaging.ImageFormat]::Png)
        } finally {
            $nativeBitmap.Dispose()
            $expectedBitmap.Dispose()
            $expectedNative.Dispose()
            $nativeIcon.Dispose()
        }
    }
    $fileIcon = [System.Drawing.Icon]::ExtractAssociatedIcon($gui)
    if ($null -eq $fileIcon) { throw 'O executável não fornece um ícone para o Windows.' }
    $referenceIcon = [System.Drawing.Icon]::new(
        (Join-Path $aiRepository 'internal\branding\ai-talker.ico'), $fileIcon.Width, $fileIcon.Height
    )
    $fileBitmap = $fileIcon.ToBitmap()
    $referenceBitmap = $referenceIcon.ToBitmap()
    try {
        for ($y = 0; $y -lt $fileBitmap.Height; $y++) {
            for ($x = 0; $x -lt $fileBitmap.Width; $x++) {
                $actual = $fileBitmap.GetPixel($x, $y)
                $expected = $referenceBitmap.GetPixel($x, $y)
                if ($actual.A -eq 0 -and $expected.A -eq 0) { continue }
                if ($actual.ToArgb() -ne $expected.ToArgb()) {
                    throw 'O ícone extraído do executável não corresponde ao ícone do STT-widget.'
                }
            }
        }
        $fileBitmap.Save((Join-Path $checkDirectory 'executable-icon.png'), [System.Drawing.Imaging.ImageFormat]::Png)
    } finally {
        $fileBitmap.Dispose()
        $referenceBitmap.Dispose()
        $fileIcon.Dispose()
        $referenceIcon.Dispose()
    }
    Write-Host 'Ícones nativos da janela e recurso do executável conferidos.'

    if ($IntegratedFrame) {
        $frameDpiContext = [AITalkerSmoke.Native]::SetThreadDpiAwarenessContext([IntPtr](-4))
        try {
        $timer.Restart()
        while ([AITalkerSmoke.Native]::GetProp($window, 'AI-Talker.IntegratedFrame') -eq [IntPtr]::Zero -and
               $timer.Elapsed.TotalSeconds -lt 10) {
            Start-Sleep -Milliseconds 100
        }
        if ([AITalkerSmoke.Native]::GetProp($window, 'AI-Talker.IntegratedFrame') -eq [IntPtr]::Zero) {
            throw 'A barra integrada não foi instalada na janela.'
        }
        $windowRect = New-Object AITalkerSmoke.Native+Rect
        $clientOrigin = New-Object AITalkerSmoke.Native+Point
        if (-not [AITalkerSmoke.Native]::GetWindowRect($window, [ref]$windowRect) -or
            -not [AITalkerSmoke.Native]::ClientToScreen($window, [ref]$clientOrigin) -or
            $clientOrigin.X -ne $windowRect.Left -or $clientOrigin.Y -ne $windowRect.Top) {
            throw 'A área do canvas não substituiu a antiga barra de título.'
        }
        $scale = [AITalkerSmoke.Native]::GetDpiForWindow($window) / 96.0
        $minimum = [AITalkerSmoke.Native]::Minimum($window)
        if ($Layout -eq 'column' -and $minimum.X -gt 350 * $scale) {
            throw "A largura mínima da coluna continua grande: $($minimum.X), escala $scale."
        }
        $dragX = $windowRect.Left + [int](50 * $scale)
        $dragY = $windowRect.Top + [int](20 * $scale)
        if ([AITalkerSmoke.Native]::HitTest($window, $dragX, $dragY) -ne 2 -or
            [AITalkerSmoke.Native]::HitTest($window, $windowRect.Left + 1, $windowRect.Top + 1) -ne 13) {
            throw 'A barra integrada perdeu o arraste ou o redimensionamento pelo canto.'
        }
        # Caption actions now live in the canvas. There must be no invisible
        # DWM button intercepting these client points. smoke-window.ps1 checks
        # actual widget bounds, clicks, cursors and OpenGL captures separately.
        foreach ($distance in @(26, 58, 90)) {
            $buttonX = $windowRect.Right - [int]($distance * $scale)
            if ([AITalkerSmoke.Native]::HitTest($window, $buttonX, $dragY) -ne 1) {
                throw 'A região dos controles do canvas ainda foi interceptada pelo título nativo.'
            }
        }
        foreach ($command in @(0xf030, 0xf120, 0xf020, 0xf120)) {
            [AITalkerSmoke.Native]::PostMessage($window, 0x0112, [IntPtr]$command, [IntPtr]::Zero) | Out-Null
            $timer.Restart()
            do {
                Start-Sleep -Milliseconds 100
                $zoomed = [AITalkerSmoke.Native]::IsZoomed($window)
                $iconic = [AITalkerSmoke.Native]::IsIconic($window)
                $matched = switch ($command) {
                    0xf030 { $zoomed }
                    0xf020 { $iconic }
                    default { -not $zoomed -and -not $iconic }
                }
            } while (-not $matched -and $timer.Elapsed.TotalSeconds -lt 5)
            if (-not $matched) { throw "O controle nativo da janela falhou: $command." }
        }
        Write-Host "Barra integrada, arraste, cantos, área dos controles e minimizar/maximizar/restaurar conferidos. Mínimo: $($minimum.X)x$($minimum.Y)."
        } finally {
            if ($frameDpiContext -ne [IntPtr]::Zero) {
                [AITalkerSmoke.Native]::SetThreadDpiAwarenessContext($frameDpiContext) | Out-Null
            }
        }
    }

    $second = Start-Process -FilePath $gui -WorkingDirectory $aiRepository -WindowStyle Hidden -PassThru
    if (-not $second.WaitForExit(10000) -or $second.ExitCode -ne 0) {
        throw 'A segunda abertura não ativou a instância existente.'
    }
    $timer.Restart()
    $marker = Join-Path $checkDirectory 'activate.request'
    while ((Test-Path -LiteralPath $marker) -and $timer.Elapsed.TotalSeconds -lt 5) {
        Start-Sleep -Milliseconds 100
    }
    if (Test-Path -LiteralPath $marker) { throw 'A janela existente não consumiu o pedido de ativação.' }
    $first.Refresh()
    if ($first.HasExited) { throw 'A primeira instância encerrou durante a ativação.' }
    Write-Host 'Segunda abertura ativou a mesma instância.'

    if ($Screenshot) {
        Add-Type -AssemblyName System.Drawing
        # Capture only this isolated app's client area, after raising its own
        # window. No desktop-wide screenshot or user clipboard is involved.
        if (-not [AITalkerSmoke.Native]::SetWindowPos($window, [IntPtr](-1), 0, 0, 0, 0, 0x0013)) {
            throw 'Não foi possível trazer a janela de teste para a captura.'
        }
        Start-Sleep -Milliseconds 500
        $dpiContext = [AITalkerSmoke.Native]::SetThreadDpiAwarenessContext([IntPtr](-4))
        try {
            $rect = New-Object AITalkerSmoke.Native+Rect
            $point = New-Object AITalkerSmoke.Native+Point
            if (-not [AITalkerSmoke.Native]::GetClientRect($window, [ref]$rect) -or
                -not [AITalkerSmoke.Native]::ClientToScreen($window, [ref]$point)) {
                throw 'Não foi possível obter os limites da janela de teste.'
            }
            $width = $rect.Right - $rect.Left
            $height = $rect.Bottom - $rect.Top
            if ($width -lt 100 -or $height -lt 100) { throw 'A área da janela de teste é inválida.' }
            $bitmap = New-Object System.Drawing.Bitmap($width, $height)
            $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
            try {
                $graphics.CopyFromScreen($point.X, $point.Y, 0, 0, $bitmap.Size)
                $image = Join-Path $checkDirectory 'desktop.png'
                $bitmap.Save($image, [System.Drawing.Imaging.ImageFormat]::Png)
                Write-Host "Captura da janela: $image ($width x $height, posição $($point.X), $($point.Y))"
            } finally {
                $graphics.Dispose()
                $bitmap.Dispose()
            }
        } finally {
            if ($dpiContext -ne [IntPtr]::Zero) {
                [AITalkerSmoke.Native]::SetThreadDpiAwarenessContext($dpiContext) | Out-Null
            }
        }
    }
    if (-not [AITalkerSmoke.Native]::PostMessage($window, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)) {
        throw 'Não foi possível solicitar o fechamento da janela de teste.'
    }
    if (-not $first.WaitForExit(10000) -or $first.ExitCode -ne 0) {
        throw 'A interface não encerrou corretamente.'
    }
    if ($WritingTabs -or $WritingGroups) {
        $saved = Get-Content -LiteralPath (Join-Path $checkDirectory 'workspace-go-v1.json') -Raw | ConvertFrom-Json
        $expectedTabs = if ($WritingGroups) { 4 } else { 2 }
        if ($saved.tabs.Count -ne $expectedTabs -or $saved.tabs[0].name -ne 'Mensagem Mendes' -or
            -not $saved.tabs[0].renamed -or $saved.tabs[1].name -ne 'Aba 2' -or
            $saved.tabs[0].undo.text -ne 'Texto anterior recuperável.' -or
            $saved.tabs[1].processed_text -ne 'Resultado independente da segunda aba.' -or
            $saved.tabs[1].processing.mode -ne 'free') {
            throw 'O encerramento perdeu textos, nomes, numeração, modos ou desfazer das abas.'
        }
        Write-Host 'Abas, nomes, textos, numeração e desfazer preservados ao encerrar.'
        if ($WritingGroups) {
            if ($saved.groups.Count -ne 2 -or $saved.groups[0] -ne 1 -or $saved.groups[1] -ne 2 -or
                $saved.selected -ne 3 -or $saved.tabs[0].group -ne 1 -or $saved.tabs[1].group -ne 1 -or
                $saved.tabs[2].group -ne 2 -or $saved.tabs[3].group -ne 2 -or
                $saved.tabs[2].name -ne 'Aba 1' -or $saved.tabs[3].name -ne 'Aba 2' -or
                $saved.tabs[2].text -ne 'Texto independente do segundo grupo.' -or
                $saved.tabs[2].processed_text -ne 'Resultado independente do segundo grupo.') {
                throw 'O encerramento perdeu grupos, numeração independente ou a seleção do segundo grupo.'
            }
            Write-Host 'Dois grupos em linhas, numeração independente e seleção preservados ao encerrar.'
        }
    }
    Write-Host "Abertura, instância única e encerramento passaram. Dados isolados: $checkDirectory"
} finally {
    foreach ($process in @($second, $first)) {
        if ($null -eq $process) { continue }
        $process.Refresh()
        if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit(5000) | Out-Null }
        $process.Dispose()
    }
    if ($null -eq $previousDirectory) {
        Remove-Item Env:AI_TALKER_DATA_DIR -ErrorAction SilentlyContinue
    } else {
        $env:AI_TALKER_DATA_DIR = $previousDirectory
    }
}
