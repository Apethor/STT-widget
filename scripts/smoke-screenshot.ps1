[CmdletBinding()]
param([switch]$SkipBuild)

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
        if ($LASTEXITCODE -ne 0) { throw 'Falha ao compilar o smoke de screenshot.' }
    } finally { Pop-Location }
}
$checkDirectory = Join-Path $aiRepository ('artifacts\validation\screenshot-' + [guid]::NewGuid().ToString('N'))
[System.IO.Directory]::CreateDirectory($checkDirectory) | Out-Null
$utf8 = [System.Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName System.Drawing
if (-not ('AITalkerScreenshotSmoke.Native' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Text;
using System.Runtime.InteropServices;
namespace AITalkerScreenshotSmoke {
    public static class Native {
        public delegate bool EnumProc(IntPtr window, IntPtr state);
        [StructLayout(LayoutKind.Sequential)] public struct Point { public int X, Y; }
        [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
        [StructLayout(LayoutKind.Sequential)] public struct CursorInfo {
            public int Size, Flags; public IntPtr Cursor; public Point Position;
        }
        [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc callback, IntPtr state);
        [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
        [DllImport("user32.dll")] public static extern IntPtr GetWindow(IntPtr window, uint command);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr GetProp(IntPtr window, string name);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr window, StringBuilder text, int count);
        [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr window, ref Point point);
        [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr window, out Rect rectangle);
        [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
        [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr window, IntPtr order, int x, int y, int width, int height, uint flags);
        [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
        [DllImport("user32.dll")] public static extern void mouse_event(uint flags, uint dx, uint dy, uint data, UIntPtr extra);
        [DllImport("user32.dll")] public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
        [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wp, IntPtr lp);
        [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr window, uint message, IntPtr wp, IntPtr lp);
        [DllImport("user32.dll")] public static extern bool GetCursorInfo(ref CursorInfo info);
        [DllImport("user32.dll")] public static extern IntPtr LoadCursor(IntPtr instance, IntPtr name);
        [DllImport("user32.dll")] public static extern void keybd_event(byte key, byte scan, uint flags, UIntPtr extra);
        public static void Escape() {
            keybd_event(27, 0, 0, UIntPtr.Zero);
            System.Threading.Thread.Sleep(50);
            keybd_event(27, 0, 2, UIntPtr.Zero);
        }
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
        public static IntPtr Packed(int x, int y) {
            return new IntPtr(unchecked((int)((uint)(ushort)x | ((uint)(ushort)y << 16))));
        }
        public static Point Screen(IntPtr window, int x, int y) {
            var point = new Point { X=x, Y=y };
            if (!ClientToScreen(window, ref point)) throw new Exception("ClientToScreen falhou.");
            return point;
        }
        public static void Move(IntPtr window, int x, int y) {
            var point = Screen(window, x, y);
            SetCursorPos(point.X, point.Y);
            PostMessage(window, 0x0200, IntPtr.Zero, Packed(x, y));
        }
        public static IntPtr Cursor() {
            var info = new CursorInfo { Size=Marshal.SizeOf(typeof(CursorInfo)) };
            return GetCursorInfo(ref info) ? info.Cursor : IntPtr.Zero;
        }
    }
}
'@
}

function Read-State([scriptblock]$Accept = { param($value) $true }) {
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    do {
        $path = Join-Path $checkDirectory 'window-state.json'
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
        $process.Refresh()
        if ($process.HasExited) { throw "O smoke encerrou: $($process.ExitCode)." }
        Start-Sleep -Milliseconds 80
    } while ($limit.Elapsed.TotalSeconds -lt 15)
    $caller = (Get-PSCallStack)[1]
    throw "O smoke de screenshot não chegou ao estado esperado na linha $($caller.ScriptLineNumber). Evidências: $checkDirectory"
}

function Command([string]$Action) {
    $beforeFrame = (Read-State).Frame
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        (@{screenshot=$Action} | ConvertTo-Json -Compress), $utf8)
    $null = Read-State { param($value) $value.Frame -ge $beforeFrame + 2 }
}

function Screenshot-Command([hashtable]$Payload) {
    $beforeFrame = (Read-State).Frame
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        ($Payload | ConvertTo-Json -Compress -Depth 4), $utf8)
    return Read-State { param($value) $value.Frame -ge $beforeFrame + 2 }
}

function Scroll-TranscriptLink {
    $beforeFrame = (Read-State).Frame
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        '{"scroll_transcript":true}', $utf8)
    return Read-State { param($value)
        $value.Frame -ge $beforeFrame + 2 -and
        $null -ne $value.TranscriptLink -and
        $value.TranscriptLink.Y -ge $value.TranscriptViewport.Y - 1 -and
        $value.TranscriptLink.Y + $value.TranscriptLink.Height -le
            $value.TranscriptViewport.Y + $value.TranscriptViewport.Height + 1
    }
}

function Wait-Window([string]$Title) {
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    do {
        $handle = [AITalkerScreenshotSmoke.Native]::Find($process.Id, $Title)
        if ($handle -ne [IntPtr]::Zero) {
            if ($Title -eq 'Selecionar screenshot') {
                # Observe the active capture before testing cancellation;
                # a state file from before the command also says "not busy".
                $null = Read-State { param($value) $value.ScreenshotBusy }
            }
            return $handle
        }
        if ($process.HasExited) { throw 'O smoke encerrou antes de abrir a janela.' }
        Start-Sleep -Milliseconds 80
    } while ($limit.Elapsed.TotalSeconds -lt 10)
    throw "A janela '$Title' não abriu."
}

function Wait-Owned([IntPtr]$Window, [IntPtr]$Owner) {
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    while ([AITalkerScreenshotSmoke.Native]::GetWindow($Window, 4) -ne $Owner) {
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw 'O editor não ficou associado à janela principal fixada.' }
        Start-Sleep -Milliseconds 80
    }
}

function Point-For($Rect, $Scale) {
    return @([int](($Rect.X+$Rect.Width/2)*$Scale), [int](($Rect.Y+$Rect.Height/2)*$Scale))
}

function Click([IntPtr]$Window, $Rect, $Scale) {
    $point = Point-For $Rect $Scale
    [AITalkerScreenshotSmoke.Native]::Move($Window, $point[0], $point[1])
    [AITalkerScreenshotSmoke.Native]::PostMessage($Window, 0x0201, [IntPtr]1,
        [AITalkerScreenshotSmoke.Native]::Packed($point[0], $point[1])) | Out-Null
    Start-Sleep -Milliseconds 60
    [AITalkerScreenshotSmoke.Native]::PostMessage($Window, 0x0202, [IntPtr]::Zero,
        [AITalkerScreenshotSmoke.Native]::Packed($point[0], $point[1])) | Out-Null
}

function Drag([IntPtr]$Window, [int]$X, [int]$Y, [int]$ToX, [int]$ToY) {
    [AITalkerScreenshotSmoke.Native]::Move($Window, $X, $Y)
    [AITalkerScreenshotSmoke.Native]::PostMessage($Window, 0x0201, [IntPtr]1,
        [AITalkerScreenshotSmoke.Native]::Packed($X, $Y)) | Out-Null
    Start-Sleep -Milliseconds 80
    [AITalkerScreenshotSmoke.Native]::Move($Window, $ToX, $ToY)
    [AITalkerScreenshotSmoke.Native]::PostMessage($Window, 0x0200, [IntPtr]1,
        [AITalkerScreenshotSmoke.Native]::Packed($ToX, $ToY)) | Out-Null
    Start-Sleep -Milliseconds 120
    [AITalkerScreenshotSmoke.Native]::PostMessage($Window, 0x0202, [IntPtr]::Zero,
        [AITalkerScreenshotSmoke.Native]::Packed($ToX, $ToY)) | Out-Null
}

function Physical-Click([IntPtr]$Window, $Rect, $Scale) {
    [AITalkerScreenshotSmoke.Native]::SetForegroundWindow($Window) | Out-Null
    $point = Point-For $Rect $Scale
    [AITalkerScreenshotSmoke.Native]::Move($Window, $point[0], $point[1])
    Start-Sleep -Milliseconds 100
    [AITalkerScreenshotSmoke.Native]::mouse_event(2, 0, 0, 0, [UIntPtr]::Zero)
    Start-Sleep -Milliseconds 70
    [AITalkerScreenshotSmoke.Native]::mouse_event(4, 0, 0, 0, [UIntPtr]::Zero)
}

function Physical-Drag([IntPtr]$Window, [int]$X, [int]$Y, [int]$ToX, [int]$ToY) {
    [AITalkerScreenshotSmoke.Native]::SetForegroundWindow($Window) | Out-Null
    $point = [AITalkerScreenshotSmoke.Native]::Screen($Window, $X, $Y)
    [AITalkerScreenshotSmoke.Native]::SetCursorPos($point.X, $point.Y) | Out-Null
    Start-Sleep -Milliseconds 100
    [AITalkerScreenshotSmoke.Native]::mouse_event(2, 0, 0, 0, [UIntPtr]::Zero)
    for ($step=1; $step -le 8; $step++) {
        $point = [AITalkerScreenshotSmoke.Native]::Screen($Window,
            [int]($X+($ToX-$X)*$step/8), [int]($Y+($ToY-$Y)*$step/8))
        [AITalkerScreenshotSmoke.Native]::SetCursorPos($point.X, $point.Y) | Out-Null
        Start-Sleep -Milliseconds 50
    }
    [AITalkerScreenshotSmoke.Native]::mouse_event(4, 0, 0, 0, [UIntPtr]::Zero)
}

function Assert-ScreenshotControl([string]$Name, $Rect, $Screenshot) {
    if ($null -eq $Rect -or $Rect.Width -le 0 -or $Rect.Height -le 0 -or
        $Rect.X -lt -1 -or $Rect.Y -lt -1 -or
        $Rect.X+$Rect.Width -gt $Screenshot.CanvasWidth+1 -or
        $Rect.Y+$Rect.Height -gt $Screenshot.CanvasHeight+1) {
        throw "O controle '$Name' não ficou visível no canvas do editor."
    }
}

function Capture-ScreenshotNative([IntPtr]$Window, [string]$Name) {
    $client = [AITalkerScreenshotSmoke.Native+Rect]::new()
    if (-not [AITalkerScreenshotSmoke.Native]::GetClientRect($Window, [ref]$client)) {
        throw 'Não foi possível medir o client do editor da screenshot.'
    }
    $origin = [AITalkerScreenshotSmoke.Native]::Screen($Window, 0, 0)
    $pixels = [System.Drawing.Bitmap]::new($client.Right-$client.Left, $client.Bottom-$client.Top)
    $graphics = [System.Drawing.Graphics]::FromImage($pixels)
    try {
        $graphics.CopyFromScreen($origin.X, $origin.Y, 0, 0, $pixels.Size)
        $pixels.Save((Join-Path $checkDirectory "$Name.png"))
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory "$Name.json"),
            (@{X=$origin.X; Y=$origin.Y; Width=$pixels.Width; Height=$pixels.Height} |
                ConvertTo-Json -Compress), $utf8)
    } finally { $graphics.Dispose(); $pixels.Dispose() }
}

function Select-ScreenshotZoom([IntPtr]$Window, [string]$Label) {
    $state = Read-State
    $beforeFrame = $state.Frame
    Assert-ScreenshotControl 'Zoom' $state.Screenshot.Zoom $state.Screenshot
    Assert-ScreenshotControl 'Lupa' $state.Screenshot.ZoomButton $state.Screenshot
    $evidenceName = 'fit'
    if ($Label -ne 'Ajustar à janela') { $evidenceName = $Label.TrimEnd('%') }
    Capture-ScreenshotNative $Window "screenshot-native-before-$evidenceName"
    Physical-Click $Window $state.Screenshot.ZoomButton $state.Screenshot.Scale
    Start-Sleep -Milliseconds 350
    Capture-ScreenshotNative $Window "screenshot-native-menu-$evidenceName"
    $state = Read-State { param($value)
        $value.Frame -gt $beforeFrame -and
        $null -ne $value.Screenshot.ZoomLabels -and $value.Screenshot.ZoomLabels.Count -gt 0
    }
    $labels = [string[]]$state.Screenshot.ZoomLabels
    $items = @($state.Screenshot.ZoomItems)
    if ($labels.Count -ne $items.Count) { throw 'A lista de zoom perdeu os destinos dos itens.' }
    $index = [Array]::IndexOf($labels, $Label)
    if ($index -lt 0) { throw "A lista de zoom não oferece '$Label'." }
    for ($item=0; $item -lt $items.Count; $item++) {
        Assert-ScreenshotControl $labels[$item] $items[$item] $state.Screenshot
    }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory "screenshot-zoom-menu-$evidenceName.json"),
        ($state.Screenshot | ConvertTo-Json -Depth 6), $utf8)
    $beforeFrame = $state.Frame
    Physical-Click $Window $items[$index] $state.Screenshot.Scale
    $expectedPercent = 0
    if ($Label -ne 'Ajustar à janela') { $expectedPercent = [double]$Label.TrimEnd('%') }
    return Read-State { param($value)
        $selected = $value.Screenshot.ZoomFit -and $Label -eq 'Ajustar à janela'
        if ($Label -ne 'Ajustar à janela') {
            $selected = -not $value.Screenshot.ZoomFit -and
                $value.Screenshot.ZoomPercent -eq $expectedPercent
        }
        $value.Frame -ge $beforeFrame+2 -and $selected -and
            ($null -eq $value.Screenshot.ZoomLabels -or $value.Screenshot.ZoomLabels.Count -eq 0)
    }
}

function Screenshot-ImagePoint($Screenshot, [double]$X, [double]$Y) {
    $logicalX = $Screenshot.Image.X+$X*$Screenshot.Image.Width/180
    $logicalY = $Screenshot.Image.Y+$Y*$Screenshot.Image.Height/120
    if ($logicalX -lt $Screenshot.Viewport.X+2 -or
        $logicalX -gt $Screenshot.Viewport.X+$Screenshot.Viewport.Width-2 -or
        $logicalY -lt $Screenshot.Viewport.Y+2 -or
        $logicalY -gt $Screenshot.Viewport.Y+$Screenshot.Viewport.Height-2) {
        throw 'O ponto de marcação ficou fora da imagem visível no viewport.'
    }
    return @([int]($logicalX*$Screenshot.Scale), [int]($logicalY*$Screenshot.Scale))
}

function Select-Area([IntPtr]$Window, [int]$X, [int]$Y, [int]$ToX, [int]$ToY) {
    [AITalkerScreenshotSmoke.Native]::SendMessage($Window, 0x0201, [IntPtr]1,
        [AITalkerScreenshotSmoke.Native]::Packed($X, $Y)) | Out-Null
    [AITalkerScreenshotSmoke.Native]::SendMessage($Window, 0x0200, [IntPtr]1,
        [AITalkerScreenshotSmoke.Native]::Packed($ToX, $ToY)) | Out-Null
    [AITalkerScreenshotSmoke.Native]::SendMessage($Window, 0x0202, [IntPtr]::Zero,
        [AITalkerScreenshotSmoke.Native]::Packed($ToX, $ToY)) | Out-Null
}

$process = $null
$previousScale = $env:FYNE_SCALE
$env:FYNE_SCALE = '0.85'
$dpiContext = [AITalkerScreenshotSmoke.Native]::SetThreadDpiAwarenessContext([IntPtr](-4))
try {
    $process = Start-Process -FilePath $executable -ArgumentList ('"' + $checkDirectory + '"') `
        -WorkingDirectory $aiRepository -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $checkDirectory 'stdout.log') `
        -RedirectStandardError (Join-Path $checkDirectory 'stderr.log')
    $state = Read-State
    $main = Wait-Window ('STT-widget ' + $state.Backlog.Version)
    $ready = [System.Diagnostics.Stopwatch]::StartNew()
    while ([AITalkerScreenshotSmoke.Native]::GetProp($main, 'AI-Talker.IntegratedFrame') -eq [IntPtr]::Zero) {
        if ($ready.Elapsed.TotalSeconds -gt 10) { throw 'A moldura integrada não ficou pronta.' }
        Start-Sleep -Milliseconds 80
    }
    [AITalkerScreenshotSmoke.Native]::SetWindowPos($main, [IntPtr](-1), 100, 60, 0, 0, 0x0011) | Out-Null
    [AITalkerScreenshotSmoke.Native]::SetForegroundWindow($main) | Out-Null
    Start-Sleep -Milliseconds 500
    $state = Read-State
    if ($state.StreamText -ne '' -or
        [Math]::Abs($state.NewAudio.Y-$state.ImportAudio.Y) -gt 1 -or
        [Math]::Abs($state.NewAudio.Y-$state.Device.Y) -gt 1 -or
        [Math]::Abs($state.NewAudio.Y-$state.Stream.Y) -gt 1 -or
        $state.Device.X -lt ($state.ImportAudio.X+$state.ImportAudio.Width) -or
        $state.Stream.X -lt ($state.Device.X+$state.Device.Width)) {
        throw 'Microfone e Stream não ficaram compactos na mesma linha de Novo áudio/Importar.'
    }
    # Capture only the synthetic recorder panel of this helper process.
    $start = [AITalkerScreenshotSmoke.Native]::Screen($main,
        [int](($state.RecordPanel.X+25)*$state.Scale), [int](($state.RecordPanel.Y+80)*$state.Scale))
    $baseline = [System.Drawing.Bitmap]::new(180, 120)
    $graphics = [System.Drawing.Graphics]::FromImage($baseline)
    try {
        $graphics.CopyFromScreen($start.X, $start.Y, 0, 0, $baseline.Size)
        $baseline.Save((Join-Path $checkDirectory 'baseline.png'))
    } finally { $graphics.Dispose(); $baseline.Dispose() }

    Click $main $state.Camera $state.Scale
    $overlay = Wait-Window 'Selecionar screenshot'
    $origin = [AITalkerScreenshotSmoke.Native]::Screen($overlay, 0, 0)
    $x = $start.X-$origin.X
    $y = $start.Y-$origin.Y
    $cursorLimit = [System.Diagnostics.Stopwatch]::StartNew()
    do {
        [AITalkerScreenshotSmoke.Native]::Move($overlay, $x, $y)
        [AITalkerScreenshotSmoke.Native]::SendMessage($overlay, 0x0020, $overlay, [IntPtr]1) | Out-Null
        if ([AITalkerScreenshotSmoke.Native]::Cursor() -eq
            [AITalkerScreenshotSmoke.Native]::LoadCursor([IntPtr]::Zero, [IntPtr]32515)) { break }
        if ($cursorLimit.Elapsed.TotalSeconds -gt 2) { throw 'A seleção não apresentou o cursor crosshair.' }
        Start-Sleep -Milliseconds 50
    } while ($true)
    # Reverse drag validates normalization of both axes.
    Select-Area $overlay ($x+180) ($y+120) $x $y
    $state = Read-State { param($value) $null -ne $value.Screenshot }
    $capturePath = $state.Screenshot.Path
    $baseline = [System.Drawing.Bitmap]::new((Join-Path $checkDirectory 'baseline.png'))
    $captured = [System.Drawing.Bitmap]::new($capturePath)
    try {
        if ($captured.Width -ne 180 -or $captured.Height -ne 120) { throw 'A área capturada teve tamanho incorreto.' }
        for ($py=0; $py -lt 120; $py++) {
            for ($px=0; $px -lt 180; $px++) {
                if ($baseline.GetPixel($px,$py).ToArgb() -ne $captured.GetPixel($px,$py).ToArgb()) {
                    throw "A captura não preservou o pixel original em $px,$py."
                }
            }
        }
    } finally { $baseline.Dispose(); $captured.Dispose() }
    Write-Host 'Seleção nativa: crosshair, arraste inverso e PNG com os pixels originais conferidos.'

    $editor = Wait-Window $state.Screenshot.Title
    Wait-Owned $editor $main
    [AITalkerScreenshotSmoke.Native]::SetForegroundWindow($editor) | Out-Null
    $state = Read-State { param($value)
        $value.Screenshot.CanvasWidth -gt 0 -and $value.Screenshot.Image.Width -gt 0
    }
    $initialWidth, $initialHeight = $state.Screenshot.CanvasWidth, $state.Screenshot.CanvasHeight
    $initialRevision, $initialSavedRevision = $state.Screenshot.Revision, $state.Screenshot.SavedRevision
    $hashBeforeZoom = (Get-FileHash -LiteralPath $capturePath).Hash
    $state = Screenshot-Command @{screenshot_resize=@{Width=500; Height=760}}
    $state = Read-State { param($value)
        $value.Screenshot.CanvasWidth -le 520 -and $value.Screenshot.CanvasHeight -ge 700
    }
    foreach ($zoomLabel in @('75%', '100%', '200%', 'Ajustar à janela')) {
        $state = Select-ScreenshotZoom $editor $zoomLabel
        $zoomScale = $state.Screenshot.ZoomPercent/100
        if ($state.Screenshot.ZoomFit) {
            $zoomScale = [Math]::Min($state.Screenshot.Surface.Width/180,
                $state.Screenshot.Surface.Height/120)
        }
        if ([Math]::Abs($state.Screenshot.Image.Width-180*$zoomScale) -gt 1 -or
            [Math]::Abs($state.Screenshot.Image.Height-120*$zoomScale) -gt 1) {
            throw "A imagem não usou a escala selecionada em '$zoomLabel'."
        }
        if ($state.Screenshot.Revision -ne $initialRevision -or
            $state.Screenshot.SavedRevision -ne $initialSavedRevision -or
            (Get-FileHash -LiteralPath $capturePath).Hash -ne $hashBeforeZoom) {
            throw "O zoom '$zoomLabel' alterou as marcações ou o PNG original."
        }
        $evidenceName = 'fit'
        if ($zoomLabel -ne 'Ajustar à janela') { $evidenceName = $zoomLabel.TrimEnd('%') }
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory "screenshot-zoom-$evidenceName.json"),
            ($state.Screenshot | ConvertTo-Json -Depth 6), $utf8)
        Command 'preview'
        Copy-Item -LiteralPath (Join-Path $checkDirectory 'screenshot-editor.png') `
            -Destination (Join-Path $checkDirectory "screenshot-zoom-$evidenceName.png")
    }
    $state = Select-ScreenshotZoom $editor '200%'
    $strokeStart = Screenshot-ImagePoint $state.Screenshot 120 90
    $strokeEnd = Screenshot-ImagePoint $state.Screenshot 145 90
    Physical-Drag $editor $strokeStart[0] $strokeStart[1] $strokeEnd[0] $strokeEnd[1]
    $state = Read-State { param($value) $value.Screenshot.Revision -ge 1 }
    if ((Get-FileHash -LiteralPath $capturePath).Hash -ne $hashBeforeZoom) {
        throw 'O traço foi salvo antes da ação explícita de Salvar.'
    }
    $strokeRevision = $state.Screenshot.Revision
    $state = Screenshot-Command @{screenshot_resize=@{Width=$initialWidth; Height=$initialHeight}}
    $state = Read-State { param($value)
        [Math]::Abs($value.Screenshot.CanvasWidth-$initialWidth) -le 2 -and
            [Math]::Abs($value.Screenshot.CanvasHeight-$initialHeight) -le 2
    }
    Assert-ScreenshotControl 'Salvar' $state.Screenshot.Save $state.Screenshot
    Physical-Click $editor $state.Screenshot.Save $state.Screenshot.Scale
    $state = Read-State { param($value)
        $value.Screenshot.SavedRevision -eq $strokeRevision -and -not $value.Screenshot.Busy
    }
    $savedStroke = [System.Drawing.Bitmap]::new($capturePath)
    $baseline = [System.Drawing.Bitmap]::new((Join-Path $checkDirectory 'baseline.png'))
    try {
        $changed = 0
        for ($py=0; $py -lt 120; $py++) {
            for ($px=0; $px -lt 180; $px++) {
                if ($baseline.GetPixel($px,$py).ToArgb() -eq $savedStroke.GetPixel($px,$py).ToArgb()) { continue }
                $changed++
                if ($px -lt 115 -or $px -gt 150 -or $py -lt 85 -or $py -gt 95) {
                    throw "O pincel com zoom 200% alterou um pixel fora da região esperada em $px,$py."
                }
            }
        }
        if ($changed -lt 50) { throw 'O pincel físico não deixou o traço esperado com zoom 200%.' }
        $ink = $savedStroke.GetPixel(132,90)
        if ($ink.R -ne 166 -or $ink.G -ne 92 -or $ink.B -ne 255) {
            throw 'O traço com zoom 200% não alcançou o ponto correspondente do PNG original.'
        }
        if ($savedStroke.Width -ne 180 -or $savedStroke.Height -ne 120) {
            throw 'O zoom mudou as dimensões do PNG salvo.'
        }
    } finally { $savedStroke.Dispose(); $baseline.Dispose() }
    Copy-Item -LiteralPath $capturePath -Destination (Join-Path $checkDirectory 'screenshot-zoom-stroke.png')
    $state = Screenshot-Command @{screenshot_zoom='Ajustar à janela'}
    Write-Host 'Zoom nativo: lupa/lista na janela estreita, 75/100/200/Ajustar, PNG intacto e traço no ponto original passaram.'
    Command 'text'
    $state = Read-State { param($value) $value.Screenshot.Revision -gt $strokeRevision }
    $revisionBeforeCopy = $state.Screenshot.Revision
    $hashBeforeCopy = (Get-FileHash -LiteralPath $capturePath).Hash
    Physical-Click $editor $state.Screenshot.CopyPath $state.Screenshot.Scale
    $copyLimit = [System.Diagnostics.Stopwatch]::StartNew()
    do {
        $copyPath = Join-Path $checkDirectory 'copied-text.json'
        if (Test-Path -LiteralPath $copyPath) {
            $copied = Get-Content -LiteralPath $copyPath -Raw | ConvertFrom-Json
            if ($copied.text -eq $capturePath) { break }
        }
        if ($copyLimit.Elapsed.TotalSeconds -gt 5) { throw 'Copiar caminho não usou o PNG da janela da screenshot.' }
        Start-Sleep -Milliseconds 80
    } while ($true)
    $state = Read-State { param($value) -not $value.Screenshot.Busy }
    if ($state.Screenshot.Revision -ne $revisionBeforeCopy -or
        $state.Screenshot.SavedRevision -ge $revisionBeforeCopy -or
        (Get-FileHash -LiteralPath $capturePath).Hash -ne $hashBeforeCopy) {
        throw 'Copiar caminho salvou ou alterou as marcações ainda não salvas.'
    }
    Command 'preview'
    Start-Sleep -Milliseconds 400
    Physical-Click $editor $state.Screenshot.Save $state.Screenshot.Scale
    $state = Read-State { param($value)
        $value.Screenshot.SavedRevision -eq $revisionBeforeCopy -and -not $value.Screenshot.Busy
    }
    $savedHash = (Get-FileHash -LiteralPath $capturePath).Hash
    # Compare decoded pixels because PNG encoder metadata/compression can differ.
    $saved = [System.Drawing.Bitmap]::new($capturePath)
    $baseline = [System.Drawing.Bitmap]::new((Join-Path $checkDirectory 'baseline.png'))
    try {
        $changed = 0
        for ($py=0; $py -lt 120; $py++) {
            for ($px=0; $px -lt 180; $px++) {
                if ($baseline.GetPixel($px,$py).ToArgb() -ne $saved.GetPixel($px,$py).ToArgb()) { $changed++ }
            }
        }
        if ($changed -lt 30) { throw 'Pincel/texto não apareceram no PNG salvo.' }
    } finally { $saved.Dispose(); $baseline.Dispose() }
    Command 'preview'
    Start-Sleep -Milliseconds 400
    [AITalkerScreenshotSmoke.Native]::PostMessage($editor, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
    $state = Read-State { param($value) $null -eq $value.Screenshot }
    if (-not (Test-Path -LiteralPath $capturePath) -or (Get-FileHash -LiteralPath $capturePath).Hash -ne $savedHash) {
        throw 'Fechar o editor não preservou a última imagem salva.'
    }
    Write-Host 'Editor nativo: pincel por arraste, texto, salvar no mesmo PNG e fechar preservando o arquivo conferidos.'

    # Escape and tiny clicks never save another capture.
    Command 'capture'
    $overlay = Wait-Window 'Selecionar screenshot'
    [AITalkerScreenshotSmoke.Native]::PostMessage($overlay, 0x0201, [IntPtr]1,
        [AITalkerScreenshotSmoke.Native]::Packed($x,$y)) | Out-Null
    [AITalkerScreenshotSmoke.Native]::PostMessage($overlay, 0x0202, [IntPtr]::Zero,
        [AITalkerScreenshotSmoke.Native]::Packed($x,$y)) | Out-Null
    Start-Sleep -Milliseconds 200
    if ([AITalkerScreenshotSmoke.Native]::Find($process.Id, 'Selecionar screenshot') -eq [IntPtr]::Zero) {
        throw 'Um clique sem área encerrou a seleção.'
    }
    [AITalkerScreenshotSmoke.Native]::SetForegroundWindow($overlay) | Out-Null
    [AITalkerScreenshotSmoke.Native]::Escape()
    $state = Read-State { param($value) -not $value.ScreenshotBusy }
    if (@(Get-ChildItem -LiteralPath (Join-Path $checkDirectory 'screenshots') -Filter '*.png' -Recurse -File).Count -ne 1) {
        throw 'Cancelar criou uma captura indevida.'
    }
    Command 'capture'
    $overlay = Wait-Window 'Selecionar screenshot'
    [AITalkerScreenshotSmoke.Native]::SetForegroundWindow($overlay) | Out-Null
    [AITalkerScreenshotSmoke.Native]::Escape()
    $state = Read-State { param($value) -not $value.ScreenshotBusy }
    Command 'capture'
    $overlay = Wait-Window 'Selecionar screenshot'
    [AITalkerScreenshotSmoke.Native]::PostMessage($overlay, 0x0201, [IntPtr]1,
        [AITalkerScreenshotSmoke.Native]::Packed($x,$y)) | Out-Null
    [AITalkerScreenshotSmoke.Native]::PostMessage($overlay, 0x0200, [IntPtr]1,
        [AITalkerScreenshotSmoke.Native]::Packed($x+120,$y+80)) | Out-Null
    Start-Sleep -Milliseconds 150
    [AITalkerScreenshotSmoke.Native]::SetForegroundWindow($overlay) | Out-Null
    [AITalkerScreenshotSmoke.Native]::Escape()
    $state = Read-State { param($value) -not $value.ScreenshotBusy }
    if ($null -ne $state.Screenshot -or
        [AITalkerScreenshotSmoke.Native]::Find($process.Id, 'Selecionar screenshot') -ne [IntPtr]::Zero -or
        @(Get-ChildItem -LiteralPath (Join-Path $checkDirectory 'screenshots') -Filter '*.png' -Recurse -File).Count -ne 1 -or
        [AITalkerScreenshotSmoke.Native]::Cursor() -eq
            [AITalkerScreenshotSmoke.Native]::LoadCursor([IntPtr]::Zero, [IntPtr]32515)) {
        throw 'Escape durante o arraste salvou uma imagem ou deixou a seleção/cursor ativos.'
    }
    Write-Host 'Escape físico: cancelamento antes e durante o arraste, sem imagem/editor e com ponteiro restaurado.'
    Command 'capture'
    $overlay = Wait-Window 'Selecionar screenshot'
    Select-Area $overlay $x $y ($x+180) ($y+120)
    $state = Read-State { param($value) $null -ne $value.Screenshot }
    $deletedPath = $state.Screenshot.Path
    $editor = Wait-Window $state.Screenshot.Title
    Wait-Owned $editor $main
    [AITalkerScreenshotSmoke.Native]::SetForegroundWindow($editor) | Out-Null
    Physical-Click $editor $state.Screenshot.Trash $state.Screenshot.Scale
    $state = Read-State { param($value) $null -eq $value.Screenshot }
    if ((Test-Path -LiteralPath $deletedPath) -or -not (Test-Path -LiteralPath $capturePath)) {
        throw 'A lixeira excluiu um arquivo diferente ou não excluiu sua captura.'
    }
    Command 'links'
    $state = Read-State { param($value) $value.ReadingLinks -and $null -ne $value.ResultLink }
    $state = Screenshot-Command @{capture='processed-link-before'}
    $state = Read-State { param($value)
        $value.ReadingLinks -and $null -ne $value.ResultLink -and
        $value.ResultLink.Y -ge $value.ProcessingPanel.Y - 1 -and
        $value.ResultLink.Y+$value.ResultLink.Height -le
            $value.ColumnViewport.Y+$value.ColumnViewport.Height+1
    }
    [AITalkerScreenshotSmoke.Native]::SetForegroundWindow($main) | Out-Null
    Start-Sleep -Milliseconds 200
    Capture-ScreenshotNative $main 'processed-link-native-before'
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'processed-link-state.json'),
        ($state | ConvertTo-Json -Depth 8), $utf8)
    $sourceText = $state.ProcessedText
    Physical-Click $main $state.ResultLink $state.Scale
    $limit = [System.Diagnostics.Stopwatch]::StartNew()
    $openedPath = Join-Path $checkDirectory 'opened-url.json'
    while (-not (Test-Path -LiteralPath $openedPath)) {
        if ($limit.Elapsed.TotalSeconds -gt 10) {
            Capture-ScreenshotNative $main 'processed-link-native-after'
            throw "O hyperlink não recebeu o clique nativo. Evidências: $checkDirectory"
        }
        Start-Sleep -Milliseconds 80
    }
    if ((Get-Content -LiteralPath $openedPath -Raw | ConvertFrom-Json).url -ne 'https://example.com/docs?q=go#intro') {
        throw 'O hyperlink não manteve o endereço completo.'
    }
    Physical-Click $main $state.ResultView $state.Scale
    $state = Read-State { param($value) -not $value.ReadingLinks }
    if ($state.ProcessedText -ne $sourceText) { throw 'Voltar ao editor alterou o texto/URL.' }
    # The editor's larger minimum height can move its toolbar below the
    # column viewport. Scroll it into view before sending another click.
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        '{"scroll_bottom":true}', $utf8)
    $state = Read-State { param($value)
        -not $value.ReadingLinks -and
        ($value.ResultView.Y+$value.ResultView.Height/2) -lt
        ($value.ColumnViewport.Y+$value.ColumnViewport.Height)
    }
    Click $main $state.ResultView $state.Scale
    $state = Read-State { param($value) $value.ReadingLinks }
    Write-Host 'Links nativos: nome compacto, clique com destino exato e alternância leitura/edição passaram.'
    Command 'record'
    $state = Read-State { param($value) $value.Recording }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"stream_seconds":1}', $utf8)
    $state = Read-State { param($value) $value.RecordSeconds -ge 1 }
    $offlineText = $state.TranscriptText
    Command 'capture'
    $overlay = Wait-Window 'Selecionar screenshot'
    Select-Area $overlay $x $y ($x+180) ($y+120)
    $state = Read-State { param($value) $null -ne $value.Screenshot }
    $recordedImage = $state.Screenshot.Path
    $editor = Wait-Window $state.Screenshot.Title
    Wait-Owned $editor $main
    [AITalkerScreenshotSmoke.Native]::PostMessage($editor, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
    $state = Read-State { param($value) $null -eq $value.Screenshot }
    if (-not $state.Recording) { throw 'Fechar o editor parou o áudio simulado.' }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"stream_seconds":2}', $utf8)
    $state = Read-State { param($value) $value.RecordSeconds -ge 3 }
    if ($state.TranscriptText -ne $offlineText -or $state.Transcribing) {
        throw 'Uma captura sem Stream começou a transcrever antes de parar.'
    }
    Command 'record'
    $state = Read-State { param($value) -not $value.Recording -and -not $value.Transcribing -and $value.TranscriptReading }
    if ($state.TranscriptText.IndexOf('antes da screenshot') -lt 0 -or
        $state.TranscriptText.IndexOf('antes da screenshot') -gt $state.TranscriptText.IndexOf($recordedImage) -or
        $state.TranscriptText.IndexOf('depois da screenshot') -lt $state.TranscriptText.IndexOf($recordedImage) -or
        -not $state.TranscriptText.Contains($recordedImage) -or
        $state.TranscriptText.Contains('Link dos screenshots')) {
        throw 'A gravação sem Stream não colocou o link entre a fala anterior e a seguinte.'
    }
    $state = Scroll-TranscriptLink
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        '{"capture":"transcript-links"}', $utf8)
    $limit.Restart()
    while (-not (Test-Path -LiteralPath (Join-Path $checkDirectory 'transcript-links.png'))) {
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw 'A evidência da transcrição não foi capturada.' }
        Start-Sleep -Milliseconds 80
    }
    # Capturing the painter can refresh the layout. Use its current geometry
    # and scroll the actual reading pane again before sending the mouse click.
    $state = Scroll-TranscriptLink
    Physical-Click $main $state.TranscriptLink $state.Scale
    $imageOpened = Join-Path $checkDirectory 'opened-image.json'
    $limit.Restart()
    while (-not (Test-Path -LiteralPath $imageOpened)) {
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw 'O link da screenshot não recebeu o clique nativo.' }
        Start-Sleep -Milliseconds 80
    }
    if ((Get-Content -LiteralPath $imageOpened -Raw | ConvertFrom-Json).path -ne $recordedImage) {
        throw 'O hyperlink de imagem não abriu o arquivo correspondente.'
    }
    Physical-Click $main $state.TranscriptCopy $state.Scale
    $copyPath = Join-Path $checkDirectory 'copied-text.json'
    $limit.Restart()
    # The screenshot's Copy Path check already created this file. Wait for
    # the current asynchronous copy instead of accepting that older value.
    do {
        if (Test-Path -LiteralPath $copyPath) {
            $copied = Get-Content -LiteralPath $copyPath -Raw | ConvertFrom-Json
            if ($copied.text -eq $state.TranscriptText) { break }
        }
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw 'Copiar não preservou o texto e o caminho completo.' }
        Start-Sleep -Milliseconds 80
    } while ($true)
    Write-Host 'Gravação sem Stream: screenshot entre as falas, hyperlink de imagem e cópia do caminho completo passaram.'
    if (-not $state.RetranscribeEnabled) { throw 'O áudio concluído não oferece retranscrição.' }
    $previousText, $previousSource = $state.TranscriptText, $state.AudioSource
    Physical-Click $main $state.Retranscribe $state.Scale
    $state = Read-State { param($value) $value.Transcribing }
    if ($state.TranscriptText -ne $previousText -or $state.RetranscribeEnabled) {
        throw 'Retranscrever apagou o texto antes de concluir ou permitiu cliques simultâneos.'
    }
    $state = Read-State { param($value) -not $value.Transcribing -and $value.TranscriptText.Contains('Nova transcrição sintética') }
    $firstRun = $state.TranscriptionRun
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'retranscription-first.json'),
        (@{state=$state; source=$previousSource; screenshot=$recordedImage;
        count=($state.TranscriptText.Split(@($recordedImage), [System.StringSplitOptions]::None).Count - 1)} | ConvertTo-Json -Depth 8), $utf8)
    if (-not $state.RetranscribeEnabled -or $firstRun -eq '' -or $state.AudioSource -ne $previousSource -or
        $state.TranscriptionModel -ne 'mistral.voxtral-small-24b-2507' -or
        ($state.TranscriptText.Split(@($recordedImage), [System.StringSplitOptions]::None).Count - 1) -ne 1) {
        throw 'A nova transcrição perdeu o áudio, modelo ou screenshot da gravação; confira retranscription-first.json.'
    }
    $qualityFrame = $state.Frame
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"quality":"fast"}', $utf8)
    $state = Read-State { param($value) $value.Frame -ge ($qualityFrame+2) }
    Physical-Click $main $state.Retranscribe $state.Scale
    $state = Read-State { param($value) -not $value.Transcribing -and $value.TranscriptionModel -eq 'mistral.voxtral-mini-3b-2507' }
    if ($state.TranscriptionRun -eq $firstRun -or $state.AudioSource -ne $previousSource -or
        -not $state.TranscriptText.Contains('Nova transcrição sintética · mistral.voxtral-mini') -or
        $state.TranscriptText.Contains('mistral.voxtral-small')) {
        throw 'Trocar o modelo não atualizou a transcrição da mesma gravação com uma nova tentativa.'
    }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"capture":"retranscription"}', $utf8)
    $captureLimit = [System.Diagnostics.Stopwatch]::StartNew()
    while (-not (Test-Path -LiteralPath (Join-Path $checkDirectory 'retranscription.png'))) {
        if ($captureLimit.Elapsed.TotalSeconds -gt 10) { throw 'A evidência de retranscrição não foi capturada.' }
        Start-Sleep -Milliseconds 80
    }
    Write-Host 'Retranscrever: clique nativo, troca de modelo, mesma aba, screenshot e tentativas independentes passaram.'
    $beforeWidth, $beforeHeight = $state.CanvasWidth, $state.CanvasHeight
    Physical-Click $main $state.Device $state.Scale
    $state = Read-State { param($value) $value.DevicesOpen }
    if ($state.DevicePopup.Width -gt 320 -or $state.DevicePopup.Height -gt 320 -or $state.SettingsOpen) {
        throw 'O atalho de dispositivo abriu uma tela grande de configurações.'
    }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"close_devices":true}', $utf8)
    $state = Read-State { param($value) -not $value.DevicesOpen }
    if ($state.CanvasWidth -ne $beforeWidth -or $state.CanvasHeight -ne $beforeHeight) {
        throw 'O seletor de dispositivos mudou o tamanho da janela.'
    }
    Physical-Click $main $state.Stream $state.Scale
    $state = Read-State { param($value) $value.StreamEnabled }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"reset_text":true}', $utf8)
    $state = Read-State { param($value) $value.TranscriptText -eq '' }
    Command 'record'
    $state = Read-State { param($value) $value.Recording }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"stream_seconds":4}', $utf8)
    $state = Read-State { param($value) $value.RecordSeconds -ge 4 }
    $state = Read-State { param($value) $value.TranscriptText.Contains('antes da screenshot') -and $value.StreamPending -gt 0 }
    if (-not $state.Recording) { throw 'O texto parcial apareceu somente depois de parar.' }
    Command 'capture'
    $overlay = Wait-Window 'Selecionar screenshot'
    Select-Area $overlay $x $y ($x+180) ($y+120)
    $state = Read-State { param($value) $null -ne $value.Screenshot -and $value.TranscriptText.Contains($value.Screenshot.Path) }
    $streamImage = $state.Screenshot.Path
    if (-not $state.Recording -or -not $state.TranscriptReading -or $state.TranscriptText -match '\[\d{2}:\d{2}\.\d{3}\]') {
        throw 'O link não apareceu no instante da captura durante o stream.'
    }
    $editor = Wait-Window $state.Screenshot.Title
    [AITalkerScreenshotSmoke.Native]::PostMessage($editor, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
    $state = Read-State { param($value) $null -eq $value.Screenshot }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"stream_seconds":4}', $utf8)
    $state = Read-State { param($value) $value.TranscriptText.Contains('depois da screenshot') }
    $state = Read-State { param($value) $value.TranscriptText.Contains('antes da screenshot') }
    if ($state.TranscriptText.IndexOf('antes da screenshot') -gt $state.TranscriptText.IndexOf($streamImage) -or
        $state.TranscriptText.IndexOf('depois da screenshot') -lt $state.TranscriptText.IndexOf($streamImage)) {
        throw 'O reconhecimento atrasado saiu da ordem da fala e da screenshot.'
    }
    $streamText = $state.TranscriptText
    Command 'record'
    $state = Read-State { param($value) -not $value.Recording -and -not $value.Transcribing }
    if ($state.TranscriptText -ne $streamText) { throw 'Parar duplicou a transcrição do stream.' }
    $state = Scroll-TranscriptLink
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"capture":"stream-links"}', $utf8)
    Start-Sleep -Milliseconds 350
    $state = Scroll-TranscriptLink
    Remove-Item -LiteralPath $imageOpened
    Physical-Click $main $state.TranscriptLink $state.Scale
    $limit.Restart()
    while (-not (Test-Path -LiteralPath $imageOpened)) {
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw 'O link do stream não recebeu o clique nativo.' }
        Start-Sleep -Milliseconds 80
    }
    if ((Get-Content -LiteralPath $imageOpened -Raw | ConvertFrom-Json).path -ne $streamImage) {
        throw 'O link do stream abriu outra screenshot.'
    }
    Remove-Item -LiteralPath $copyPath
    Physical-Click $main $state.TranscriptCopy $state.Scale
    $limit.Restart()
    while (-not (Test-Path -LiteralPath $copyPath)) {
        if ($limit.Elapsed.TotalSeconds -gt 10) { throw 'A cópia do stream não respondeu.' }
        Start-Sleep -Milliseconds 80
    }
    if ((Get-Content -LiteralPath $copyPath -Raw | ConvertFrom-Json).text -ne $streamText) {
        throw 'Copiar no stream perdeu o caminho ou a linha do tempo.'
    }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"prompt":"Primeiro pedido nativo."}', $utf8)
    $state = Read-State { param($value) $value.TranscriptText -eq 'Primeiro pedido nativo.' }
    Physical-Click $main $state.Process $state.Scale
    $state = Read-State { param($value) $value.ConversationTurns -eq 1 -and -not $value.ProcessBusy }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"prompt":"Continue este assunto."}', $utf8)
    $state = Read-State { param($value) $value.TranscriptText -eq 'Continue este assunto.' }
    Physical-Click $main $state.Follow $state.Scale
    $state = Read-State { param($value) $value.ConversationTurns -eq 2 -and -not $value.ProcessBusy }
    $request = Get-Content -LiteralPath (Join-Path $checkDirectory 'process-request.json') -Raw | ConvertFrom-Json
    if ($request.text -ne 'Continue este assunto.' -or @($request.history).Count -ne 1 -or
        $request.history[0].prompt -ne 'Primeiro pedido nativo.') {
        throw 'Reprocessar não continuou a conversa anterior.'
    }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'), '{"capture":"conversation"}', $utf8)
    Start-Sleep -Milliseconds 350
    Write-Host 'Controles nativos: dispositivos, Stream com screenshot na fala e Reprocessar com contexto passaram.'
    Command 'capture'
    $overlay = Wait-Window 'Selecionar screenshot'
    [AITalkerScreenshotSmoke.Native]::PostMessage($main, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
    if (-not $process.WaitForExit(10000) -or $process.ExitCode -ne 0) { throw 'O smoke não encerrou normalmente.' }
    Write-Host "Cancelamento, clique sem área, exclusão isolada e encerramento durante seleção passaram. Evidências: $checkDirectory"
} finally {
    if ($null -ne $process) {
        $process.Refresh()
        if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit(5000) | Out-Null }
        $process.Dispose()
    }
    if ($dpiContext -ne [IntPtr]::Zero) {
        [AITalkerScreenshotSmoke.Native]::SetThreadDpiAwarenessContext($dpiContext) | Out-Null
    }
    if ($null -eq $previousScale) { Remove-Item Env:FYNE_SCALE -ErrorAction SilentlyContinue }
    else { $env:FYNE_SCALE = $previousScale }
}
