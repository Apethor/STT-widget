[CmdletBinding()]
param([switch]$SkipBuild, [string]$Scale = '1', [switch]$RecorderOnly, [switch]$PlaybackOnly, [switch]$BacklogOnly, [switch]$Release011Only, [switch]$Release012Only, [switch]$UndoHydrationOnly)

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
        if ($LASTEXITCODE -ne 0) { throw 'Falha ao compilar o smoke da janela.' }
    } finally { Pop-Location }
}
if (-not (Test-Path -LiteralPath $executable)) { throw 'Compile o smoke antes de usar -SkipBuild.' }
$checkDirectory = Join-Path $aiRepository ('artifacts\validation\window-' + [guid]::NewGuid().ToString('N'))
[System.IO.Directory]::CreateDirectory($checkDirectory) | Out-Null
$utf8 = [System.Text.UTF8Encoding]::new($false)
if ($UndoHydrationOnly) {
    [IO.File]::WriteAllText((Join-Path $checkDirectory 'undo-hydration.fixture.json'), '{}', $utf8)
}

if (-not ('AITalkerPointer.Native' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace AITalkerPointer {
    public static class Native {
        [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
        [StructLayout(LayoutKind.Sequential)] public struct Point { public int X, Y; }
        [StructLayout(LayoutKind.Sequential)] public struct CursorInfo {
            public int Size, Flags; public IntPtr Cursor; public Point Position;
        }
        [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
        [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window, out Rect rect);
        [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr window, out Rect rect);
        [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr window, IntPtr target, uint flags);
        [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr window, int command);
        [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
        [DllImport("user32.dll")] public static extern IntPtr GetDC(IntPtr window);
        [DllImport("user32.dll")] public static extern int ReleaseDC(IntPtr window, IntPtr dc);
        [DllImport("gdi32.dll")] public static extern bool BitBlt(IntPtr target, int x, int y, int width, int height,
            IntPtr source, int sourceX, int sourceY, uint operation);
        [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr window, ref Point point);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr GetProp(IntPtr window, string name);
        [DllImport("user32.dll")] public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
        [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr window, IntPtr order, int x, int y, int width, int height, uint flags);
        [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
        [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
        [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
        [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr window);
        [DllImport("user32.dll")] public static extern bool IsZoomed(IntPtr window);
        [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
        [DllImport("user32.dll")] public static extern bool GetCursorInfo(ref CursorInfo info);
        [DllImport("user32.dll")] public static extern IntPtr LoadCursor(IntPtr instance, IntPtr name);
        public static IntPtr Packed(int x, int y) {
            return new IntPtr(unchecked((int)((uint)(ushort)x | ((uint)(ushort)y << 16))));
        }
        public static void Move(IntPtr window, int x, int y) {
            Point screen = new Point { X=x, Y=y };
            if (!ClientToScreen(window, ref screen)) throw new Exception("ClientToScreen falhou.");
            SetCursorPos(screen.X, screen.Y);
            PostMessage(window, 0x0200, IntPtr.Zero, Packed(x, y));
        }
        public static IntPtr Cursor() {
            CursorInfo info = new CursorInfo { Size=Marshal.SizeOf(typeof(CursorInfo)) };
            return GetCursorInfo(ref info) ? info.Cursor : IntPtr.Zero;
        }
        public static bool CursorReadable() {
            CursorInfo info = new CursorInfo { Size=Marshal.SizeOf(typeof(CursorInfo)) };
            return GetCursorInfo(ref info);
        }
        public static long Hit(IntPtr window, int x, int y) {
            Point screen = new Point { X=x, Y=y };
            ClientToScreen(window, ref screen);
            return SendMessage(window, 0x0084, IntPtr.Zero, Packed(screen.X, screen.Y)).ToInt64();
        }
    }
}
'@
}

function Read-State([scriptblock]$Accept = { param($state) $true }, [int]$TimeoutSeconds = 10) {
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
                # Retry a transient race with the application's atomic write.
            }
        }
        $process.Refresh()
        if ($process.HasExited) { throw "O smoke encerrou: $($process.ExitCode)." }
        Start-Sleep -Milliseconds 100
    } while ($limit.Elapsed.TotalSeconds -lt $TimeoutSeconds)
    $caller = (Get-PSCallStack)[1]
    $predicate = $Accept.ToString().Trim()
    throw "O canvas nativo não chegou ao estado esperado na linha $($caller.ScriptLineNumber): $predicate. Evidências: $checkDirectory"
}

function Capture-Canvas([string]$Name) {
    if ($Release012Only -or $Release011Only -or $BacklogOnly -or $UndoHydrationOnly) {
        $frame = (Read-State).Frame
        $null = Read-State { param($value) $value.Frame -ge $frame+2 }
        Capture-OwnedWindowClient $window $Name
        return
    }
    [System.IO.File]::WriteAllText(
        (Join-Path $checkDirectory 'window-command.json'),
        (@{capture=$Name} | ConvertTo-Json -Compress), $utf8
    )
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    $imagePath = Join-Path $checkDirectory ($Name + '.png')
    while (-not (Test-Path -LiteralPath $imagePath) -or (Get-Item -LiteralPath $imagePath).Length -eq 0) {
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw "A captura $Name não foi produzida." }
        Start-Sleep -Milliseconds 100
    }
}

function Capture-OwnedWindowClient([IntPtr]$Handle, [string]$Name) {
    [uint32]$captureOwner012 = 0
    [AITalkerPointer.Native]::GetWindowThreadProcessId($Handle, [ref]$captureOwner012) | Out-Null
    if ($Handle -eq [IntPtr]::Zero -or $captureOwner012 -ne $process.Id -or
        [IO.Path]::GetFileName($Name) -ne $Name) {
        throw 'A captura não pertence ao processo e diretório isolados.'
    }
    Add-Type -AssemblyName System.Drawing
    $client012 = New-Object AITalkerPointer.Native+Rect
    $origin012 = New-Object AITalkerPointer.Native+Point
    if (-not [AITalkerPointer.Native]::GetClientRect($Handle, [ref]$client012) -or
        -not [AITalkerPointer.Native]::ClientToScreen($Handle, [ref]$origin012) -or
        -not [AITalkerPointer.Native]::IsWindowVisible($Handle)) { throw 'Não foi possível medir o cliente visível do smoke.' }
    [AITalkerPointer.Native]::SetWindowPos($Handle, [IntPtr](-1), 0, 0, 0, 0, 0x0043) | Out-Null
    [AITalkerPointer.Native]::SetForegroundWindow($Handle) | Out-Null
    Start-Sleep -Milliseconds 300
    $image012 = [System.Drawing.Bitmap]::new($client012.Right, $client012.Bottom)
    $graphics012 = [System.Drawing.Graphics]::FromImage($image012)
    try {
        # This is the client rectangle of the verified, visible, topmost smoke
        # HWND only. No monitor-wide or other-application capture is taken.
        $graphics012.CopyFromScreen($origin012.X, $origin012.Y, 0, 0, $image012.Size)
        $image012.Save((Join-Path $checkDirectory ($Name+'.png')), [System.Drawing.Imaging.ImageFormat]::Png)
    } finally {
        $graphics012.Dispose()
        $image012.Dispose()
    }
}

function Control-Point($Rect, $Scale, [switch]$NearTop) {
    $vertical = if ($NearTop) { 5 } else { $Rect.Height / 2 }
    return @([int](($Rect.X + $Rect.Width / 2) * $Scale), [int](($Rect.Y + $vertical) * $Scale))
}

function Click-Control($Rect, $Scale) {
    $point = Control-Point $Rect $Scale -NearTop
    if ([AITalkerPointer.Native]::Hit($window, $point[0], $point[1]) -ne 1) {
        throw 'Uma ação do cabeçalho foi interceptada pela região nativa.'
    }
    [AITalkerPointer.Native]::Move($window, $point[0], $point[1])
    [AITalkerPointer.Native]::PostMessage($window, 0x0201, [IntPtr]1, [AITalkerPointer.Native]::Packed($point[0], $point[1])) | Out-Null
    Start-Sleep -Milliseconds 50
    [AITalkerPointer.Native]::PostMessage($window, 0x0202, [IntPtr]::Zero, [AITalkerPointer.Native]::Packed($point[0], $point[1])) | Out-Null
}

function Click-Volume($Rect, $Scale, [float]$Fraction) {
    # Keep endpoint clicks inside the widget after rounding at fractional DPI.
    $innerX = [Math]::Min($Rect.Width - 1, [Math]::Max(1, $Rect.Width * $Fraction))
    $x = [int](($Rect.X + $innerX) * $Scale)
    $y = [int](($Rect.Y + $Rect.Height / 2) * $Scale)
    [AITalkerPointer.Native]::Move($window, $x, $y)
    [AITalkerPointer.Native]::PostMessage($window, 0x0201, [IntPtr]1, [AITalkerPointer.Native]::Packed($x, $y)) | Out-Null
    Start-Sleep -Milliseconds 50
    [AITalkerPointer.Native]::PostMessage($window, 0x0202, [IntPtr]::Zero, [AITalkerPointer.Native]::Packed($x, $y)) | Out-Null
}

function Double-ClickThumb($Rect, $Scale) {
    $point = Control-Point $Rect $Scale
    # Keep this gesture separate from the preceding single click.
    Start-Sleep -Milliseconds 650
    [AITalkerPointer.Native]::Move($window, $point[0], $point[1])
    foreach ($click in 1..2) {
        [AITalkerPointer.Native]::PostMessage($window, 0x0201, [IntPtr]1,
            [AITalkerPointer.Native]::Packed($point[0], $point[1])) | Out-Null
        Start-Sleep -Milliseconds 50
        [AITalkerPointer.Native]::PostMessage($window, 0x0202, [IntPtr]::Zero,
            [AITalkerPointer.Native]::Packed($point[0], $point[1])) | Out-Null
        Start-Sleep -Milliseconds 60
    }
}

function Test-TranscriptionMenus {
    foreach ($width in @(320, 480)) {
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
            (@{resize=@{Width=$width; Height=900}; scroll_top=$true} | ConvertTo-Json -Compress), $utf8)
        $state = Read-State { param($value) [Math]::Abs($value.CanvasWidth-$width) -lt 1 -and $value.ScrollY -eq 0 }
        $beforeHeight = $state.CanvasHeight
        Click-Control $state.CloudButton $state.Scale
        $state = Read-State { param($value) $null -ne $value.CloudMenu -and $value.MenuItems.Count -eq 3 }
        if ($state.MenuChildren -ne 0 -or ($state.MenuLabels -join '|') -ne 'AWS Cloud|Whisper local|API de áudio') {
            throw 'O menu de motor voltou a misturar modelos ou submenus.'
        }
        Assert-Shortcut $state.CloudMenu $state
        foreach ($item in $state.MenuItems) {
            $point = Control-Point $item $state.Scale
            [AITalkerPointer.Native]::Move($window, $point[0], $point[1])
        }
        if ($width -eq 320) { Capture-Canvas 'engine-menu' }
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"close_menu":true}', $utf8)
        $state = Read-State { param($value) $null -eq $value.CloudMenu }
        foreach ($stream in @($false, $true)) {
            $mode = if ($stream) { 'stream' } else { 'complete' }
            $frame = $state.Frame
            [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
                (@{provider='bedrock';stream=$stream} | ConvertTo-Json -Compress), $utf8)
            $state = Read-State { param($value) $value.TranscriptionProvider -eq 'bedrock' -and $value.Frame -gt $frame + 2 }
            Click-Control $state.QualityButton $state.Scale
            $count = if ($stream) { 5 } else { 6 }
            $state = Read-State { param($value) $null -ne $value.CloudMenu -and $value.MenuItems.Count -eq $count }
            Assert-Shortcut $state.CloudMenu $state
            if ($state.MenuChildren -ne 0 -or [Math]::Abs($state.CanvasWidth-$width) -gt 1 -or
                [Math]::Abs($state.CanvasHeight-$beforeHeight) -gt 1) {
                throw 'Escolher o modelo contextual abriu um submenu ou mudou a janela.'
            }
            if ($width -eq 320) { Capture-Canvas ('model-' + $mode) }
            [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"close_menu":true}', $utf8)
            $state = Read-State { param($value) $null -eq $value.CloudMenu }
        }
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"provider":"whisper"}', $utf8)
        $state = Read-State { param($value) $value.TranscriptionProvider -eq 'whisper' -and -not $value.StreamEnabled }
        Click-Control $state.QualityButton $state.Scale
        $state = Read-State { param($value) $null -ne $value.CloudMenu -and $value.MenuItems.Count -eq 12 }
        Assert-Shortcut $state.CloudMenu $state
        if (($state.MenuLabels -join '|') -match 'Voxtral|Transcribe|Sonic') { throw 'Whisper ainda abriu modelos Cloud.' }
        if ($width -eq 320) { Capture-Canvas 'model-local' }
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
            '{"close_menu":true,"provider":"bedrock","stream":false}', $utf8)
        $state = Read-State { param($value) $null -eq $value.CloudMenu -and $value.TranscriptionProvider -eq 'bedrock' }
    }
    Write-Host 'Menus de motor e modelos: escolhas sem duplicação, modelos contextuais Cloud/Stream/Whisper e limites em 320 e 480 conferidos.'
}

function Test-GroupWidthAndTheme {
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        '{"group_name":"G","new_tabs":6}', $utf8)
    $state = Read-State { param($value) $value.GroupName -lt 45 -and $null -ne $value.GroupScrollThumb }
    if ($state.GroupLabel.Width -ge $state.GroupLimit) {
        throw 'O nome curto do grupo continua ocupando seu limite inteiro.'
    }
    $point = Control-Point $state.GroupScrollThumb $state.Scale
    [AITalkerPointer.Native]::Move($window, $point[0], $point[1])
    Start-Sleep -Milliseconds 400
    $state = Read-State
    if ($state.GroupScrollThumb.Height -gt 3.1) { throw 'O hover engrossou o scroll das abas.' }
    Capture-Canvas 'group-scroll-hover'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        '{"group_name":"Reunião e próximos passos da equipe","open":"group-width"}', $utf8)
    $state = Read-State { param($value) $null -ne $value.GroupWidthPopup -and $value.GroupName -ge 110 }
    $widthPopupFrame = $state.Frame
    $state = Read-State { param($value) $value.Frame -ge $widthPopupFrame + 2 -and
        $null -ne $value.GroupWidthPopup -and $value.GroupWidthSlider.Y -ge $value.GroupWidthPopup.Y }
    Assert-Shortcut $state.GroupWidthPopup $state
    Click-Volume $state.GroupWidthSlider $state.Scale 0.6
    $state = Read-State { param($value) $value.GroupLimit -gt 190 -and $value.GroupName -gt 190 }
    Capture-Canvas 'group-width'
    Double-ClickThumb $state.GroupWidthThumb $state.Scale
    $state = Read-State { param($value) $value.GroupLimit -eq 112 }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"close_group_width":true}', $utf8)
    $state = Read-State { param($value) $null -eq $value.GroupWidthPopup }
    foreach ($mode in @('light', 'green', 'dark')) {
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
            (@{theme=$mode} | ConvertTo-Json -Compress), $utf8)
        $state = Read-State { param($value) $value.Theme -eq $mode -and $value.HeaderIconName -eq ('ai-talker-title-' + $mode + '.png') }
        Capture-Canvas ('theme-' + $mode)
    }
    Write-Host 'Grupos: largura natural, limite ajustável e reset; scroll fino no hover. Ícone do título nos três temas conferido.'
}

function Assert-Shortcut($Rect, $State) {
    if ($null -eq $Rect -or $Rect.X -lt 0 -or $Rect.Y -lt 0 -or
        $Rect.X + $Rect.Width -gt $State.CanvasWidth + 1 -or
        $Rect.Y + $Rect.Height -gt $State.CanvasHeight + 1) {
        throw 'O menu compacto foi cortado pelo limite do canvas.'
    }
}

function Drag-Control($Rect, $Scale, [int]$Delta) {
    $point = Control-Point $Rect $Scale
    [AITalkerPointer.Native]::SetWindowPos($window, [IntPtr](-1), 0, 0, 0, 0, 0x0013) | Out-Null
    [AITalkerPointer.Native]::SetForegroundWindow($window) | Out-Null
    # The previous drag can finish at the next grip's exact center. Approach it
    # again so the native driver receives a movement after releasing the mouse.
    [AITalkerPointer.Native]::Move($window, $point[0], $point[1] - 12)
    Start-Sleep -Milliseconds 60
    [AITalkerPointer.Native]::Move($window, $point[0], $point[1])
    if ([AITalkerPointer.Native]::CursorReadable()) {
        $expected = [AITalkerPointer.Native]::LoadCursor([IntPtr]::Zero, [IntPtr]32645)
        $hover = [System.Diagnostics.Stopwatch]::StartNew()
        while (($cursor = [AITalkerPointer.Native]::Cursor()) -ne $expected) {
            if ($hover.Elapsed.TotalSeconds -gt 3) { throw "O cursor da divisória não coincidiu com seu centro visual: $cursor / $expected." }
            Start-Sleep -Milliseconds 60
        }
    } elseif (-not $script:aiCursorUnavailableReported) {
        $script:aiCursorUnavailableReported = $true
        Write-Host 'GetCursorInfo indisponível neste desktop; o formato do cursor não será validado. Arraste e dimensões continuam sendo conferidos pelos efeitos na janela.'
    }
    [AITalkerPointer.Native]::PostMessage($window, 0x0201, [IntPtr]1, [AITalkerPointer.Native]::Packed($point[0], $point[1])) | Out-Null
    Start-Sleep -Milliseconds 60
    $targetY = $point[1] + [int]($Delta * $Scale)
    [AITalkerPointer.Native]::Move($window, $point[0], $targetY)
    [AITalkerPointer.Native]::PostMessage($window, 0x0200, [IntPtr]1, [AITalkerPointer.Native]::Packed($point[0], $targetY)) | Out-Null
    Start-Sleep -Milliseconds 100
    [AITalkerPointer.Native]::PostMessage($window, 0x0202, [IntPtr]::Zero, [AITalkerPointer.Native]::Packed($point[0], $targetY)) | Out-Null
}

$process = $null
$previousScale = $env:FYNE_SCALE
$env:FYNE_SCALE = $Scale
$dpiContext = [AITalkerPointer.Native]::SetThreadDpiAwarenessContext([IntPtr](-4))
try {
    $process = Start-Process -FilePath $executable -ArgumentList ('"' + $checkDirectory + '"') `
        -WorkingDirectory $aiRepository -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $checkDirectory 'stdout.log') `
        -RedirectStandardError (Join-Path $checkDirectory 'stderr.log')
    $state = Read-State -TimeoutSeconds 30
    $process.Refresh()
    $window = $process.MainWindowHandle
    [uint32]$owner = 0
    [AITalkerPointer.Native]::GetWindowThreadProcessId($window, [ref]$owner) | Out-Null
    if ($window -eq [IntPtr]::Zero -or $owner -ne $process.Id) { throw 'A janela não pertence ao smoke.' }
    if (($Release012Only -or $Release011Only -or $BacklogOnly -or $UndoHydrationOnly) -and
        -not [AITalkerPointer.Native]::IsWindowVisible($window)) {
        [AITalkerPointer.Native]::ShowWindow($window, 5) | Out-Null
    }
    if ($Release012Only -or $Release011Only -or $BacklogOnly -or $UndoHydrationOnly) {
        [AITalkerPointer.Native]::SetForegroundWindow($window) | Out-Null
    }
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    while ([AITalkerPointer.Native]::GetProp($window, 'AI-Talker.IntegratedFrame') -eq [IntPtr]::Zero) {
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw 'A moldura integrada não foi aplicada.' }
        Start-Sleep -Milliseconds 100
    }
    [AITalkerPointer.Native]::SetWindowPos($window, [IntPtr](-1), 100, 80, 0, 0, 0x0011) | Out-Null
    $rect = New-Object AITalkerPointer.Native+Rect
    $origin = New-Object AITalkerPointer.Native+Point
    [AITalkerPointer.Native]::GetWindowRect($window, [ref]$rect) | Out-Null
    [AITalkerPointer.Native]::ClientToScreen($window, [ref]$origin) | Out-Null
    if ($rect.Left -ne $origin.X -or $rect.Top -ne $origin.Y -or $state.Header.Y -gt 8) {
        throw 'Ainda há uma reserva de título ou diferença entre as origens do canvas e do cliente.'
    }
    if ($state.Height -gt $state.Bottom.Y + $state.Bottom.Height - $state.Header.Y + 80 -and $state.ScrollY -ne 0) {
        throw 'A coluna iniciou com overflow forçado.'
    }
    Capture-Canvas 'initial'
    if ($UndoHydrationOnly) {
        . (Join-Path $PSScriptRoot 'smoke-backlog-checks.ps1')
        . (Join-Path $PSScriptRoot 'smoke-undo-hydration-checks.ps1')
        Test-UndoHydrationControls
        Write-Host "Conferência nativa de Desfazer persistido concluída. Evidências: $checkDirectory"
        return
    }
    if ($Release012Only) {
        . (Join-Path $PSScriptRoot 'smoke-backlog-checks.ps1')
        . (Join-Path $PSScriptRoot 'smoke-release-011-checks.ps1')
        . (Join-Path $PSScriptRoot 'smoke-release-012-checks.ps1')
        Test-Release012Controls
        Write-Host "Conferência nativa da 0.12 concluída. Evidências: $checkDirectory"
        return
    }
    if ($Release011Only) {
        . (Join-Path $PSScriptRoot 'smoke-backlog-checks.ps1')
        . (Join-Path $PSScriptRoot 'smoke-release-011-checks.ps1')
        Test-Release011Controls
        Write-Host "Conferência nativa da 0.11 concluída. Evidências: $checkDirectory"
        return
    }
    if ($BacklogOnly) {
        . (Join-Path $PSScriptRoot 'smoke-backlog-checks.ps1')
        Test-BacklogControls
        Write-Host "Conferência nativa do backlog concluída. Evidências: $checkDirectory"
        return
    }
    if ($PlaybackOnly) {
        if ($state.PlayVisible -or $state.PlayEnabled -or $state.PlayIcon -ne 'play') {
            throw 'Sem áudio carregado, o botão Play deve ficar oculto.'
        }
        Click-Control $state.Record $state.Scale
        $state = Read-State { param($value) $value.Recording }
        $captureFrame = $state.Frame
        $state = Read-State { param($value) $value.Recording -and $value.PlayEnabled -and $value.Frame -ge $captureFrame + 2 }
        Click-Control $state.Pause $state.Scale
        $state = Read-State { param($value) $value.CapturePaused -and $value.PlayIcon -eq 'play' }
        if ($state.Playing) { throw 'Pausar a gravação iniciou reprodução.' }
        Click-Control $state.Pause $state.Scale
        $state = Read-State { param($value) -not $value.CapturePaused -and $value.PlayIcon -eq 'pause' }
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"stream_seconds":3}', $utf8)
        $state = Read-State { param($value) $value.RecordSeconds -ge 3 }
        Click-Control $state.Record $state.Scale
        $state = Read-State { param($value) -not $value.Recording -and -not $value.Transcribing -and $value.PlayEnabled }
        $loadedFrame = $state.Frame
        $state = Read-State { param($value) $value.Frame -ge $loadedFrame + 2 -and $value.PlayEnabled -and -not $value.Transcribing }
        $source = $state.AudioSource
        $text = $state.TranscriptText
        if ((Split-Path -Leaf (Split-Path -Parent $source)) -ne (Get-Date -Format 'yyyy-MM-dd') -or
            -not (Test-Path -LiteralPath $source)) {
            throw 'O WAV real sintético não foi salvo na pasta do dia.'
        }
        if ($state.StopVisible -or -not $state.OutputVolumeVisible -or $state.OutputVolumeOpen) {
            throw 'Stop/volume não ficaram discretos com o áudio carregado.'
        }
        Click-Control $state.OutputVolumeButton $state.Scale
        $state = Read-State { param($value) $value.OutputVolumeOpen }
        Assert-Shortcut $state.OutputVolumePopup $state
        if ($state.OutputVolumePopup.Width -gt 250) { throw 'O volume de saída abriu um diálogo grande.' }
        Click-Volume $state.OutputVolumeSlider $state.Scale 0.35
        $state = Read-State { param($value) $value.OutputVolume -ge 60 -and $value.OutputVolume -le 80 }
        $outputVolume = $state.OutputVolume
        $savedWait = [System.Diagnostics.Stopwatch]::StartNew()
        do {
            $savedOutput = Get-Content -Raw -LiteralPath (Join-Path $checkDirectory 'settings.json') | ConvertFrom-Json
            if ($savedOutput.output_volume -eq $outputVolume) { break }
            if ($savedWait.Elapsed.TotalSeconds -gt 5) { throw 'O volume de reprodução não foi persistido.' }
            Start-Sleep -Milliseconds 100
        } while ($true)
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"close_output_volume":true}', $utf8)
        $state = Read-State { param($value) -not $value.OutputVolumeOpen }
        Capture-Canvas 'play-loaded'
        Click-Control $state.Pause $state.Scale
        $state = Read-State { param($value) $value.Playing -and $value.PlayIcon -eq 'pause' -and $value.PlaybackSeconds -gt 0.1 }
        if (-not $state.StopVisible -or $state.PlayerOutputVolume -ne $outputVolume) {
            throw 'Play não aplicou o volume salvo ou não mostrou Stop.'
        }
        Click-Control $state.Pause $state.Scale
        $state = Read-State { param($value) $value.PlaybackPaused -and $value.PlayIcon -eq 'play' }
        $position = $state.PlaybackSeconds
        Start-Sleep -Milliseconds 350
        $state = Read-State
        if ([Math]::Abs($state.PlaybackSeconds-$position) -gt 0.05) {
            throw 'A posição da reprodução continuou avançando durante a pausa.'
        }
        Capture-Canvas 'play-paused'
        Click-Volume $state.RecorderWave $state.Scale 0.7
        $state = Read-State { param($value) $value.PlaybackPaused -and $value.PlaybackSeconds -gt 1.9 -and $value.PlaybackSeconds -lt 2.3 }
        Click-Volume $state.RecorderWave $state.Scale 0.1
        $state = Read-State { param($value) $value.PlaybackPaused -and $value.PlaybackSeconds -gt 0.2 -and $value.PlaybackSeconds -lt 0.5 }
        Capture-Canvas 'play-position'
        Click-Control $state.OutputVolumeButton $state.Scale
        $state = Read-State { param($value) $value.OutputVolumeOpen -and $value.PlaybackPaused }
        Click-Volume $state.OutputVolumeSlider $state.Scale 0.6
        $state = Read-State { param($value) $value.OutputVolume -ge 110 -and $value.OutputVolume -le 130 -and
            $value.PlayerOutputVolume -eq $value.OutputVolume -and $value.PlaybackPaused }
        Double-ClickThumb $state.OutputVolumeThumb $state.Scale
        $state = Read-State { param($value) $value.OutputVolume -eq 100 -and $value.PlayerOutputVolume -eq 100 -and $value.PlaybackPaused }
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"close_output_volume":true}', $utf8)
        $state = Read-State { param($value) -not $value.OutputVolumeOpen -and $value.PlaybackPaused }
        Click-Control $state.Pause $state.Scale
        $state = Read-State { param($value) $value.Playing -and -not $value.PlaybackPaused -and $value.PlayIcon -eq 'pause' }
        $state = Read-State { param($value) -not $value.Playing -and $value.PlayEnabled -and $value.PlayIcon -eq 'play' }
        $finishedFrame = $state.Frame
        $state = Read-State { param($value) $value.Frame -ge $finishedFrame + 2 -and
            -not $value.Playing -and -not $value.StopVisible -and $value.PlayIcon -eq 'play' }
        if ($state.AudioSource -ne $source -or $state.TranscriptText -ne $text -or $state.TimerText -ne '00:03') {
            throw 'Ouvir alterou a gravação, a transcrição ou a duração total.'
        }
        Click-Control $state.Pause $state.Scale
        $state = Read-State { param($value) $value.Playing -and $value.PlaybackReady }
        $state = Read-State { param($value) $value.PlaybackSeconds -gt 1 -and $value.StopVisible }
        Click-Control $state.Pause $state.Scale
        $state = Read-State { param($value) $value.PlaybackPaused -and $value.StopVisible }
        Click-Control $state.StopPlayback $state.Scale
        $state = Read-State { param($value) -not $value.Playing -and -not $value.StopVisible -and $value.PlayIcon -eq 'play' }
        $stoppedFrame = $state.Frame
        $state = Read-State { param($value) $value.Frame -ge $stoppedFrame + 2 -and
            -not $value.Playing -and -not $value.StopVisible -and $value.PlayIcon -eq 'play' }
        if ($state.AudioSource -ne $source -or $state.TranscriptText -ne $text) {
            throw 'Stop alterou o áudio ou o texto.'
        }
        Click-Control $state.Pause $state.Scale
        $state = Read-State { param($value) $value.Playing -and $value.PlaybackReady }
        if ($state.PlaybackSeconds -gt 0.6) { throw 'Play após Stop não voltou ao início.' }
        Click-Control $state.NewAudio $state.Scale
        $state = Read-State { param($value) -not $value.Playing -and $value.AudioSource -eq '' -and -not $value.PlayEnabled }
        if (-not (Test-Path -LiteralPath $source)) { throw 'Novo áudio removeu o WAV original.' }
        Click-Control $state.Record $state.Scale
        $state = Read-State { param($value) $value.Recording }
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"stream_seconds":5}', $utf8)
        $state = Read-State { param($value) $value.RecordSeconds -ge 5 }
        Click-Control $state.Record $state.Scale
        $state = Read-State { param($value) -not $value.Recording -and -not $value.Transcribing -and
            $value.AudioHistoryCount -eq 2 -and $value.AudioResultReady }
        $newSource, $newText = $state.AudioSource, $state.TranscriptText
        $processedText = $state.ProcessedText
        Click-Control $state.AudioHistoryButton $state.Scale
        $state = Read-State { param($value) $value.AudioHistoryItems.Count -eq 2 }
        Click-Control $state.AudioHistoryItems[1] $state.Scale
        $state = Read-State { param($value) $value.AudioSource -eq $source -and -not $value.AudioLoading }
        if ($state.TranscriptText -ne $text -or $state.ProcessedText -ne $processedText) {
            throw 'O histórico não restaurou a transcrição anterior ou alterou o resultado processado.'
        }
        Capture-Canvas 'audio-history-old'
        Click-Control $state.AudioHistoryButton $state.Scale
        $state = Read-State { param($value) $value.AudioHistoryItems.Count -eq 2 }
        Click-Control $state.AudioHistoryItems[0] $state.Scale
        $state = Read-State { param($value) $value.AudioSource -eq $newSource -and -not $value.AudioLoading }
        if ($state.TranscriptText -ne $newText -or -not (Test-Path -LiteralPath $source)) {
            throw 'O histórico não recuperou o áudio mais novo ou removeu o anterior.'
        }
        Capture-Canvas 'audio-history-new'
        Write-Host "Reprodução no canvas: captura, Play/Pausa, posição na onda, volume/reset, Stop/reinício, fim, Novo áudio e histórico de duas gravações passaram. Evidências: $checkDirectory"
        return
    }
    if ($state.StreamText -ne '' -or
        [Math]::Abs($state.NewAudio.Y-$state.Device.Y) -gt 1 -or
        [Math]::Abs($state.ImportAudio.Y-$state.Stream.Y) -gt 1) {
        throw 'Os controles de fontes/Stream não ficaram na mesma linha de Novo áudio e Importar.'
    }
    if (-not $state.MicrophoneVisible -or $state.PlaybackVisible -or $state.MicrophoneVolume -ne 100) {
        throw 'O gravador não iniciou com um controle de microfone a 100%.'
    }
    if ($state.CaptureVolumesOpen) { throw 'Os sliders da captura ficaram abertos permanentemente.' }
    $recordControls = @($state.Record, $state.CaptureVolumeButton)
    if ($state.PlayVisible) { $recordControls += $state.Pause }
    foreach ($control in $recordControls) {
        if ($control.Y -lt $state.RecordPanel.Y -or
            $control.Y + $control.Height -gt $state.RecordPanel.Y + $state.RecordPanel.Height + 1) {
            throw 'Um controle ultrapassou a altura do box do gravador.'
        }
    }
    $teamFrame = $state.Frame
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"capture_mode":"Equipe"}', $utf8)
    $state = Read-State { param($value) $value.DeviceMode -eq 'team' -and $value.MicrophoneVisible -and
        $value.PlaybackVisible -and $value.Frame -ge $teamFrame + 2 }
    Click-Control $state.CaptureVolumeButton $state.Scale
    $state = Read-State { param($value) $value.CaptureVolumesOpen -and $null -ne $value.CaptureVolumePopup }
    Assert-Shortcut $state.CaptureVolumePopup $state
    if (-not $state.TeamIcon -or
        [Math]::Abs($state.MicrophoneSlider.X-$state.PlaybackSlider.X) -gt 1 -or
        [Math]::Abs($state.MicrophoneSlider.Width-$state.PlaybackSlider.Width) -gt 1 -or
        $state.MicrophoneVolume -ne 100 -or $state.PlaybackVolume -ne 100) {
        throw 'Equipe não mostrou o ícone conjunto ou as barras e centros a 100% não estão alinhados.'
    }
    foreach ($control in @($state.MicrophoneSlider, $state.PlaybackSlider)) {
        if ($control.Y -lt $state.CaptureVolumePopup.Y -or
            $control.Y + $control.Height -gt $state.CaptureVolumePopup.Y + $state.CaptureVolumePopup.Height + 1) {
            throw 'Mostrar o segundo volume fez uma barra ultrapassar o popup.'
        }
    }
    Capture-Canvas 'volume-team'
    Click-Volume $state.MicrophoneSlider $state.Scale 0.25
    $state = Read-State { param($value) $value.MicrophoneVolume -ge 35 -and $value.MicrophoneVolume -le 65 }
    Click-Volume $state.PlaybackSlider $state.Scale 0.75
    $state = Read-State { param($value) $value.PlaybackVolume -ge 135 -and $value.PlaybackVolume -le 165 }
    if ([Math]::Abs($state.MicrophoneSlider.X-$state.PlaybackSlider.X) -gt 1 -or
        [Math]::Abs($state.MicrophoneSlider.Width-$state.PlaybackSlider.Width) -gt 1) {
        throw 'Os números dos volumes mudaram o alinhamento das barras.'
    }
    Capture-Canvas 'volume-adjusted'
    $volumeWait = [System.Diagnostics.Stopwatch]::StartNew()
    do {
        $savedVolumes = Get-Content -Raw -LiteralPath (Join-Path $checkDirectory 'settings.json') | ConvertFrom-Json
        if ($savedVolumes.capture.microphone_volume -eq $state.MicrophoneVolume -and
            $savedVolumes.capture.playback_volume -eq $state.PlaybackVolume) { break }
        if ($volumeWait.Elapsed.TotalSeconds -gt 5) { throw 'Os dois volumes não foram persistidos.' }
        Start-Sleep -Milliseconds 100
    } while ($true)
    $systemVolume = $state.PlaybackVolume
    Double-ClickThumb $state.MicrophoneThumb $state.Scale
    $state = Read-State { param($value) $value.MicrophoneVolume -eq 100 -and $value.PlaybackVolume -eq $systemVolume }
    Double-ClickThumb $state.PlaybackThumb $state.Scale
    $state = Read-State { param($value) $value.MicrophoneVolume -eq 100 -and $value.PlaybackVolume -eq 100 }
    Capture-Canvas 'volume-reset'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        '{"capture_mode":"Microfone","close_capture_volumes":true}', $utf8)
    $state = Read-State { param($value) $value.DeviceMode -eq 'microphone' -and -not $value.PlaybackVisible -and
        -not $value.CaptureVolumesOpen }
    if ($state.TeamIcon) { throw 'Voltar ao microfone manteve o ícone de Equipe.' }
    Write-Host 'Gravador nativo: botão de volume, barras escondidas/alinhadas e volumes independentes persistidos; duplo clique restaura 100%.'
    $beforeWidth, $beforeHeight = $state.CanvasWidth, $state.CanvasHeight
    Click-Control $state.Device $state.Scale
    $state = Read-State { param($value) $value.DevicesOpen -and $value.DevicesReady -and $null -ne $value.DevicePopup }
    Assert-Shortcut $state.DevicePopup $state
    if ($state.DevicePopup.X -ge $state.Device.X -or $state.DevicePopup.Width -gt 310) {
        throw 'O menu do microfone não abriu para a esquerda em tamanho compacto.'
    }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"capture_mode":"Equipe"}', $utf8)
    $state = Read-State { param($value) $value.DeviceMode -eq 'team' -and $value.DevicesOpen -and $value.DevicesReady }
    Assert-Shortcut $state.DevicePopup $state
    if ($state.CanvasWidth -ne $beforeWidth -or $state.CanvasHeight -ne $beforeHeight) {
        throw 'Alternar a fonte no menu redimensionou a janela.'
    }
    Capture-Canvas 'device-shortcut-left'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"close_devices":true,"capture_mode":"Microfone"}', $utf8)
    $state = Read-State { param($value) -not $value.DevicesOpen -and $value.DeviceMode -eq 'microphone' }
    if ($state.CloudBatchOptions -ne 6 -or $state.CloudStreamOptions -ne 5) {
        throw 'O catálogo Cloud do canvas não preservou os modos compatíveis.'
    }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        '{"cloud_model":"aws.transcribe.streaming","cloud_stream_model":"amazon.nova-2-sonic-v1:0"}', $utf8)
    $state = Read-State { param($value) $value.CloudModel -eq 'aws.transcribe.streaming' -and
        $value.CloudStreamModel -eq 'amazon.nova-2-sonic-v1:0' }
    Write-Host 'Atalhos nativos: menu do microfone dentro do canvas/à esquerda e escolhas Cloud separadas por modo.'
    if ($state.Bottom.Y + $state.Bottom.Height -gt $state.ColumnViewport.Y + $state.ColumnViewport.Height + 1) {
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"scroll_bottom":true}', $utf8)
        $state = Read-State { param($value) $value.Bottom.Y + $value.Bottom.Height -le $value.ColumnViewport.Y + $value.ColumnViewport.Height + 1 }
    }
    $beforeRecorderResize = $state.Height
    Drag-Control $state.Bottom $state.Scale 160
    $state = Read-State { param($value) $value.Height -gt $beforeRecorderResize + 120 }
    Drag-Control $state.RecorderDivider $state.Scale -300
    $state = Read-State { param($value) $value.RecorderMode -eq 'medium' -and $value.RecorderSizeHint.Contains('Ampliar') }
    if ([Math]::Abs($state.CaptureVolumeButton.Y-$state.Device.Y) -gt 1 -or
        $state.CaptureVolumeButton.X -lt $state.Device.X+$state.Device.Width -or
        $state.CaptureVolumeButton.X+$state.CaptureVolumeButton.Width -gt $state.Stream.X) {
        throw 'O botão de volume não ficou entre Microfone e Stream no modo Médio.'
    }
    Capture-Canvas 'recorder-medium'
    $compactFont = $state.RecorderFont
    Drag-Control $state.RecorderDivider $state.Scale 20
    $state = Read-State { param($value) $value.RecorderMode -eq 'medium' -and $value.RecorderFont -gt $compactFont }
    Drag-Control $state.RecorderDivider $state.Scale 120
    $state = Read-State { param($value) $value.RecorderMode -eq 'large' -and $value.RecorderSizeHint.Contains('Reduzir') }
    if ([Math]::Abs($state.CaptureVolumeButton.Y-$state.Device.Y) -gt 1 -or
        $state.CaptureVolumeButton.X -lt $state.Device.X+$state.Device.Width -or
        $state.CaptureVolumeButton.X+$state.CaptureVolumeButton.Width -gt $state.Stream.X) {
        throw 'O botão de volume não ficou entre Microfone e Stream no modo Grande.'
    }
    Capture-Canvas 'recorder-large'
    $expandedWave = $state.RecorderWave.Height
    Drag-Control $state.RecorderDivider $state.Scale 30
    $state = Read-State { param($value) $value.RecorderWave.Height -gt $expandedWave + 3 }
    Drag-Control $state.RecorderDivider $state.Scale -350
    $state = Read-State { param($value) $value.RecorderMode -eq 'medium' -and $value.RecorderSizeHint.Contains('Ampliar') }
    Write-Host 'Divisória nativa: Médio/Grande automáticos, ícones, volume junto de Microfone/Stream e crescimento do relógio/gráfico conferidos.'
    if ($RecorderOnly) {
        Write-Host "Verificação limitada aos controles do gravador. Evidências: $checkDirectory"
        return
    }
    $state = Read-State
    if ($state.Bottom.Y + $state.Bottom.Height -gt $state.ColumnViewport.Y + $state.ColumnViewport.Height + 1) {
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"scroll_bottom":true}', $utf8)
        $state = Read-State { param($value) $value.Bottom.Y + $value.Bottom.Height -le $value.ColumnViewport.Y + $value.ColumnViewport.Height + 1 }
    }
    $initialScroll = $state.ScrollY
    $before = $state.Height
    Drag-Control $state.Bottom $state.Scale 220
    $state = Read-State { param($value) $value.Height -gt $before + 180 }
    Capture-Canvas 'expanded'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"scroll_bottom":true}', $utf8)
    $state = Read-State { param($value) $value.ScrollY -gt 0 -and $value.Bottom.Y -lt $value.CanvasHeight - 20 }
    Drag-Control $state.Bottom $state.Scale -260
    $state = Read-State { param($value) $value.ScrollY -le $initialScroll + 1 -and $value.Height -le $before + 1 }
    Capture-Canvas 'collapsed'
    $collapsedHeight = $state.Height
    Drag-Control $state.Bottom $state.Scale 160
    $state = Read-State { param($value) $value.Height -gt $collapsedHeight + 120 }
    $textHeight = $state.TextHeight
    Drag-Control $state.Divider $state.Scale 35
    $state = Read-State { param($value) $value.TextHeight -gt $textHeight + 20 }
    Write-Host 'Canvas nativo: centros das divisórias, arraste, expansão e retirada do overflow conferidos.'

    Click-Control $state.Minimize $state.Scale
    $limit.Restart()
    while (-not [AITalkerPointer.Native]::IsIconic($window)) {
        if ($limit.Elapsed.TotalSeconds -gt 5) { throw 'O botão visível de minimizar não respondeu ao clique.' }
        Start-Sleep -Milliseconds 100
    }
    [AITalkerPointer.Native]::PostMessage($window, 0x0112, [IntPtr]0xf120, [IntPtr]::Zero) | Out-Null
    Start-Sleep -Milliseconds 350
    $state = Read-State
    Click-Control $state.Settings $state.Scale
    $state = Read-State { param($value) $value.SettingsOpen }
    Capture-Canvas 'settings'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"close_settings":true}', $utf8)
    $state = Read-State { param($value) -not $value.SettingsOpen }
    Write-Host 'Cliques junto à borda superior de Configurações e Minimizar responderam na posição visível.'

    [AITalkerPointer.Native]::PostMessage($window, 0x0112, [IntPtr]0xf030, [IntPtr]::Zero) | Out-Null
    $state = Read-State { param($value) $value.CanvasWidth -gt 1000 }
    Capture-Canvas 'maximized'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        '{"scroll_top":true,"stream":false,"recorder_size":"medium"}', $utf8)
    $state = Read-State { param($value) $value.ScrollY -le 1 -and $value.RecorderMode -eq 'medium' -and -not $value.StreamEnabled }
    Click-Control $state.Record $state.Scale
    $state = Read-State { param($value) $value.Recording -and $value.CopyAudioPathVisible -and $value.RecorderWide }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"stream_seconds":3}', $utf8)
    $state = Read-State { param($value) $value.RecordSeconds -ge 3 }
    foreach ($wideMode in @('medium', 'large')) {
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
            ('{"recorder_size":"' + $wideMode + '"}'), $utf8)
        $state = Read-State { param($value) $value.RecorderMode -eq $wideMode -and $value.RecorderWide }
        if ([Math]::Abs($state.RecorderClock.Y - $state.Record.Y) -gt 1 -or
            [Math]::Abs($state.RecorderClock.Y - $state.RecorderWave.Y) -gt 1 -or
            $state.RecorderClock.X + $state.RecorderClock.Width -gt $state.RecorderWave.X -or
            $state.RecorderWave.X + $state.RecorderWave.Width -gt $state.Record.X -or
            $state.CopyAudioPath.X -lt $state.Discard.X + $state.Discard.Width + 20) {
            throw 'O gravador largo não alinhou o topo do relógio/onda/controles ou perdeu o intervalo antes de copiar.'
        }
    }
    Capture-Canvas 'wide-recorder'
    Click-Control $state.Record $state.Scale
    $state = Read-State { param($value) -not $value.Recording -and -not $value.Transcribing }
    Write-Host 'Gravador largo: uma linha em Médio/Grande, separação após Descartar e captura sintética preservada.'
    Click-Control $state.Settings $state.Scale
    $state = Read-State { param($value) $value.SettingsOpen }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"close_settings":true}', $utf8)
    $state = Read-State { param($value) -not $value.SettingsOpen }
    [AITalkerPointer.Native]::PostMessage($window, 0x0112, [IntPtr]0xf120, [IntPtr]::Zero) | Out-Null
    Start-Sleep -Milliseconds 350
    [AITalkerPointer.Native]::SetWindowPos($window, [IntPtr]::Zero, 0, 0, [int](320*$state.Scale), [int](900*$state.Scale), 0x0016) | Out-Null
    $state = Read-State { param($value) $value.CanvasWidth -le 330 }
    Capture-Canvas 'minimum'
    Test-TranscriptionMenus
    Test-GroupWidthAndTheme
    $state = Read-State
    # The group checks finish at 480. Return to the minimum before checking
    # independent collapse and inversion at 320 with the native driver.
    [AITalkerPointer.Native]::SetWindowPos($window, [IntPtr]::Zero, 0, 0,
        [int](320*$state.Scale), [int](900*$state.Scale), 0x0016) | Out-Null
    $state = Read-State { param($value) $value.CanvasWidth -le 330 }
    $priorTextHeight = $state.TextHeight
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"toggle_processing":true}', $utf8)
    $state = Read-State { param($value) $value.ProcessCollapsed }
    if ($state.TextHeight -le $priorTextHeight) { throw 'Recolher Processar texto não liberou espaço para transcrição.' }
    Capture-Canvas 'processing-collapsed'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"toggle_processing":true}', $utf8)
    $state = Read-State { param($value) -not $value.ProcessCollapsed }
    $priorProcessingHeight = $state.ProcessingPanel.Height
    $priorTranscript = $state.TranscriptText
    Click-Control $state.TranscriptCollapse $state.Scale
    $state = Read-State { param($value) $value.TranscriptCollapsed -and -not $value.ProcessCollapsed }
    if ($state.ProcessingPanel.Height -le $priorProcessingHeight -or $state.TranscriptText -ne $priorTranscript) {
        throw 'Recolher Transcrição não liberou espaço ou alterou o texto.'
    }
    Capture-Canvas 'transcription-collapsed'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"toggle_processing":true}', $utf8)
    $state = Read-State { param($value) $value.TranscriptCollapsed -and $value.ProcessCollapsed }
    Capture-Canvas 'both-collapsed'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"invert_panels":true}', $utf8)
    $state = Read-State { param($value) $value.InvertPanels -and $value.ProcessingPanel.Y -lt $value.TranscriptPanel.Y }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        '{"toggle_processing":true,"toggle_transcription":true}', $utf8)
    $state = Read-State { param($value) -not $value.ProcessCollapsed -and -not $value.TranscriptCollapsed -and
        $value.ProcessingPanel.Y -lt $value.TranscriptPanel.Y }
    if ($state.TranscriptText -ne $priorTranscript -or $state.CanvasWidth -gt 330) {
        throw 'Inverter/expandir perdeu texto ou aumentou a janela mínima.'
    }
    Capture-Canvas 'panels-inverted'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"invert_panels":false}', $utf8)
    $state = Read-State { param($value) -not $value.InvertPanels -and $value.TranscriptPanel.Y -lt $value.ProcessingPanel.Y }
    Write-Host 'Painéis nativos: recolhimento independente/conjunto, inversão e expansão na janela de 320 pixels conferidos.'
    $state = Read-State
    Click-Control $state.Close $state.Scale
    if (-not $process.WaitForExit(10000) -or $process.ExitCode -ne 0) { throw 'O X visível não encerrou normalmente.' }
    Write-Host "Janela maximizada/estreita, controles integrados e fechamento passaram. Capturas: $checkDirectory"
} finally {
    if ($null -ne $process) {
        $process.Refresh()
        if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit(5000) | Out-Null }
        $process.Dispose()
    }
    if ($dpiContext -ne [IntPtr]::Zero) {
        [AITalkerPointer.Native]::SetThreadDpiAwarenessContext($dpiContext) | Out-Null
    }
    if ($null -eq $previousScale) {
        Remove-Item Env:FYNE_SCALE -ErrorAction SilentlyContinue
    } else { $env:FYNE_SCALE = $previousScale }
}
