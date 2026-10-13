# Loaded by smoke-window.ps1 / smoke-release-012-checks.ps1.
# All native actions target the existing isolated smoke PID and exact panel title.

if (-not ('AITalkerSoundboard.Native' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;
namespace AITalkerSoundboard {
    public static class Native {
        public delegate bool Callback(IntPtr window, IntPtr argument);
        [DllImport("user32.dll")] public static extern bool EnumWindows(Callback callback, IntPtr argument);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr window, StringBuilder text, int count);
        [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
        [DllImport("user32.dll")] public static extern int GetWindowLong(IntPtr window, int index);
        public static IntPtr Find(uint pid) {
            IntPtr found = IntPtr.Zero;
            Callback callback = (window, argument) => {
                uint actual;
                GetWindowThreadProcessId(window, out actual);
                if (actual == pid) {
                    var title = new StringBuilder(512);
                    GetWindowText(window, title, 512);
                    if (title.ToString() == "Painel de sons" || title.ToString() == "Soundboard")
                        found = window;
                }
                return true;
            };
            EnumWindows(callback, IntPtr.Zero);
            GC.KeepAlive(callback);
            return found;
        }
    }
}
'@
}

$script:soundboardCommandNumber = 0

function Soundboard-Command([hashtable]$Command) {
    $script:soundboardCommandNumber++
    $id = $script:soundboardCommandNumber
    $Command.id = $id
    [IO.File]::WriteAllText((Join-Path $checkDirectory 'soundboard-command.json'),
        ($Command | ConvertTo-Json -Depth 5 -Compress), $utf8)
    $state = Read-State { param($value) $null -ne $value.Soundboard -and $value.Soundboard.LastCommand -eq $id } -TimeoutSeconds 20
    if ($state.Soundboard.Error -ne '') { throw $state.Soundboard.Error }
    return $state
}

function Assert-SoundboardPID([IntPtr]$Handle) {
    [uint32]$actualPID = 0
    [AITalkerSoundboard.Native]::GetWindowThreadProcessId($Handle, [ref]$actualPID) | Out-Null
    if ($Handle -eq [IntPtr]::Zero -or $actualPID -ne $process.Id) {
        throw 'A ação do Painel de sons não pertence ao processo isolado do smoke.'
    }
}

function Capture-Soundboard([string]$Name) {
    if ($Release012Only) {
        $frame = (Read-State).Frame
        $null = Read-State { param($value) $value.Frame -ge $frame+2 }
        Capture-OwnedWindowClient $window $Name
        return
    }
    $null = Soundboard-Command @{action='capture';capture=$Name}
    $image = Join-Path $checkDirectory ($Name + '.png')
    if (-not (Test-Path -LiteralPath $image) -or (Get-Item -LiteralPath $image).Length -eq 0) {
        throw "A captura $Name não foi produzida."
    }
}

function Test-Release012Soundboard {
    $mainWindow = $window
    $mainState = Read-State
    $state = Read-State { param($value) $null -ne $value.Soundboard -and $value.Soundboard.Prepared } -TimeoutSeconds 30
    Click-Control $state.Backlog.Soundboard $state.Scale
    $state = Read-State { param($value) $value.Soundboard.Open -and @($value.Soundboard.Effects).Count -eq 6 -and
        $value.Soundboard.Effects[0].Rect.X -gt 0 -and $value.Soundboard.Effects[0].Rect.Y -gt 44 -and
        $value.Soundboard.Close.X -gt 0 }
    $window = [AITalkerSoundboard.Native]::Find([uint32]$process.Id)
    Assert-SoundboardPID $window
    try {
        [AITalkerPointer.Native]::SetForegroundWindow($window) | Out-Null
        $state = Read-State { param($value)
            $value.Soundboard.Close.Width -gt 0 -and $value.Soundboard.Minimize.Width -gt 0 -and
            $value.Soundboard.Maximize.Width -gt 0
        }
        $panel = $state.Soundboard
        $frameLimit = [System.Diagnostics.Stopwatch]::StartNew()
        while ([AITalkerPointer.Native]::GetProp($window,'AI-Talker.IntegratedFrame') -eq [IntPtr]::Zero) {
            Assert-SoundboardPID $window
            if ($frameLimit.Elapsed.TotalSeconds -gt 10) {
                throw 'A moldura integrada do painel não foi aplicada em dez segundos.'
            }
            Start-Sleep -Milliseconds 100
        }
        if ($panel.Title -ne 'Painel de sons' -or
            ([AITalkerSoundboard.Native]::GetWindowLong($window,-16) -band 0x00c00000) -ne 0 -or
            [AITalkerPointer.Native]::GetProp($window,'AI-Talker.IntegratedFrame') -eq [IntPtr]::Zero) {
            throw 'O painel não usa a moldura integrada sem legenda nativa.'
        }
        $effects = @($panel.Effects)
        if ($effects[0].Rect.Y -ne $effects[1].Rect.Y -or
            $effects[1].Rect.Y -ne $effects[2].Rect.Y -or
            $effects[0].Rect.X -ge $effects[1].Rect.X -or
            $effects[1].Rect.X -ge $effects[2].Rect.X -or
            $effects[3].Rect.Y -le $effects[0].Rect.Y) {
            throw 'Os seis efeitos não estão na matriz inicial de três colunas.'
        }
        Capture-Soundboard 'release012-soundboard-pt'
        $null = Soundboard-Command @{action='mute'}
        $state = Read-State { param($value) @($value.Soundboard.Effects).Count -eq 6 -and
            $value.Soundboard.Effects[0].Rect.X -gt 0 -and $value.Soundboard.Effects[0].Rect.Y -gt 44 -and
            $value.Soundboard.Close.X -gt 0 }
        $first = $state.Soundboard.Effects[0]
        Click-Control $first.Rect $state.Soundboard.Scale
        $state = Read-State { param($value) $value.Soundboard.LastEffect -eq 'applause' -and $value.Soundboard.SubmittedNS -gt 0 }
        $firstRequest = $state.Soundboard.RequestedNS
        Click-Control $first.Rect $state.Soundboard.Scale
        $state = Read-State { param($value) $value.Soundboard.RequestedNS -gt $firstRequest -and $value.Soundboard.LastEffect -eq 'applause' }
        Click-Control $state.Soundboard.Effects[1].Rect $state.Soundboard.Scale
        $state = Read-State { param($value) $value.Soundboard.LastEffect -eq 'laughter' }
        Click-Control $state.Soundboard.Stop $state.Soundboard.Scale
        $state = Read-State { param($value) -not $value.Soundboard.Playing }

        $targetX, $targetY = 130, 90
        Assert-SoundboardPID $window
        [AITalkerPointer.Native]::SetWindowPos($window,[IntPtr]::Zero,$targetX,$targetY,0,0,0x0015) | Out-Null
        $state = Read-State { param($value)
            $value.Soundboard.Position.X -eq $targetX -and $value.Soundboard.Position.Y -eq $targetY -and
            $value.Soundboard.SavedPosition.X -eq $targetX -and $value.Soundboard.SavedPosition.Y -eq $targetY
        }
        Click-Control $state.Soundboard.Maximize $state.Soundboard.Scale
        $state = Read-State { param($value) $value.Soundboard.Position.Maximized }
        if (-not [AITalkerPointer.Native]::IsZoomed($window)) { throw 'Maximizar não chegou à janela nativa.' }
        Capture-Soundboard 'release012-soundboard-maximized'
        Click-Control $state.Soundboard.Maximize $state.Soundboard.Scale
        $state = Read-State { param($value) -not $value.Soundboard.Position.Maximized -and $value.Soundboard.Position.X -eq $targetX }
        Click-Control $state.Soundboard.Minimize $state.Soundboard.Scale
        $state = Read-State { param($value) $value.Soundboard.Position.Minimized }
        if (-not [AITalkerPointer.Native]::IsIconic($window)) { throw 'Minimizar não chegou à janela nativa.' }
        Assert-SoundboardPID $window
        [AITalkerPointer.Native]::PostMessage($window,0x0112,[IntPtr]0xf120,[IntPtr]::Zero) | Out-Null
        $state = Read-State { param($value) -not $value.Soundboard.Position.Minimized }
        Click-Control $state.Soundboard.Close $state.Soundboard.Scale
        $state = Read-State { param($value) -not $value.Soundboard.Open }
        $window = $mainWindow
        [AITalkerPointer.Native]::SetForegroundWindow($window) | Out-Null
        Click-Control $state.Backlog.Soundboard $state.Scale
        $state = Read-State { param($value) $value.Soundboard.Open -and $value.Soundboard.Position.X -eq $targetX -and $value.Soundboard.Position.Y -eq $targetY -and
            @($value.Soundboard.Effects).Count -eq 6 -and $value.Soundboard.Effects[0].Rect.Y -gt 44 -and
            $value.Soundboard.Close.X -gt 0 }
        $window = [AITalkerSoundboard.Native]::Find([uint32]$process.Id)
        Assert-SoundboardPID $window
        $null = Soundboard-Command @{action='resize';size=@{Width=360;Height=650}}
        $state = Read-State { param($value) $value.Soundboard.CanvasWidth -lt 430 }
        if ($state.Soundboard.Effects[1].Rect.Y -le $state.Soundboard.Effects[0].Rect.Y) {
            throw 'A matriz não se adaptou à janela estreita.'
        }
        Capture-Soundboard 'release012-soundboard-narrow'
        $null = Soundboard-Command @{action='resize';size=@{Width=580;Height=420}}
        $state = Read-State { param($value) $value.Soundboard.CanvasWidth -ge 579 -and $value.Soundboard.CanvasWidth -le 581 }
        $null = Soundboard-Command @{action='language';language='en'}
        $state = Read-State { param($value) $value.Soundboard.Title -eq 'Soundboard' -and $value.Soundboard.Language -eq 'en' }
        if (($state.Soundboard.Effects.Text -join '|') -notmatch 'Applause|Laughter') {
            throw 'Os nomes padrão dos efeitos não acompanharam o inglês.'
        }
        Capture-Soundboard 'release012-soundboard-en'
        $null = Soundboard-Command @{action='language';language='pt-BR'}
        $state = Read-State { param($value) $value.Soundboard.Title -eq 'Painel de sons' -and $value.Soundboard.Language -eq 'pt-BR' }
        [IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),'{"close_settings":true}',$utf8)
        $state = Read-State { param($value) -not $value.SettingsOpen }
        Click-Control $state.Soundboard.Close $state.Soundboard.Scale
        $state = Read-State { param($value) -not $value.Soundboard.Open }
        $saved = Get-Content -LiteralPath (Join-Path $checkDirectory 'soundboard\soundboard.json') -Raw | ConvertFrom-Json
        if ($saved.window_position.x -ne $targetX -or $saved.window_position.y -ne $targetY) {
            throw 'O painel não persistiu a posição física no manifesto.'
        }
        Write-Host 'BL-076/077/078/080: seis efeitos preparados, cliques reais/reinício/substituição/Parar, matriz responsiva, moldura integrada, minimizar/maximizar/restaurar, posição ao reabrir e PT/EN passaram.'
    } finally {
        $window = $mainWindow
        [AITalkerPointer.Native]::SetForegroundWindow($window) | Out-Null
    }
}
