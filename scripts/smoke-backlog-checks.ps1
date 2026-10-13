# Loaded by smoke-window.ps1; all actions target its isolated native window.

function Backlog-Command($Command) {
    $frame = (Read-State).Frame
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'window-command.json'),
        ($Command | ConvertTo-Json -Compress -Depth 6), $utf8)
    return Read-State { param($value) $value.Frame -ge $frame + 2 }
}

function Drag-Tab($Source, $Target, $Scale) {
    $start = Control-Point $Source $Scale
    $finish = @([int](($Target.X + $Target.Width * 0.7) * $Scale),
        [int](($Target.Y + $Target.Height / 2) * $Scale))
    [AITalkerPointer.Native]::Move($window, $start[0] - 8, $start[1])
    Start-Sleep -Milliseconds 60
    [AITalkerPointer.Native]::Move($window, $start[0], $start[1])
    [AITalkerPointer.Native]::PostMessage($window, 0x0201, [IntPtr]1,
        [AITalkerPointer.Native]::Packed($start[0], $start[1])) | Out-Null
    Start-Sleep -Milliseconds 100
    for ($step = 1; $step -le 6; $step++) {
        $x = [int]($start[0] + ($finish[0] - $start[0]) * $step / 6)
        $y = [int]($start[1] + ($finish[1] - $start[1]) * $step / 6)
        [AITalkerPointer.Native]::PostMessage($window, 0x0200, [IntPtr]1,
            [AITalkerPointer.Native]::Packed($x, $y)) | Out-Null
        Start-Sleep -Milliseconds 70
    }
    [AITalkerPointer.Native]::PostMessage($window, 0x0202, [IntPtr]::Zero,
        [AITalkerPointer.Native]::Packed($finish[0], $finish[1])) | Out-Null
}

function Get-VisibleBacklogTreeLabel($State, [string]$Text) {
    $tree = $State.Backlog.Settings.Tree
    return @($State.Backlog.Settings.TreeLabels | Where-Object {
        $_.Text -eq $Text -and $_.Rect.Y -ge $tree.Y - 1 -and
        $_.Rect.Y + $_.Rect.Height -le $tree.Y + $tree.Height + 1
    } | Sort-Object { $_.Rect.Y })[0]
}

function Scroll-BacklogTree($State, [int]$Delta) {
    [uint32]$treeOwner = 0
    [AITalkerPointer.Native]::GetWindowThreadProcessId($window, [ref]$treeOwner) | Out-Null
    if ($window -eq [IntPtr]::Zero -or $treeOwner -ne $process.Id) {
        throw 'A árvore não pertence ao processo isolado.'
    }
    $tree = $State.Backlog.Settings.Tree
    $point = Control-Point $tree $State.Scale
    if ([AITalkerPointer.Native]::Hit($window, $point[0], $point[1]) -ne 1) {
        throw 'A rolagem da árvore não está na área cliente do smoke.'
    }
    [AITalkerPointer.Native]::Move($window, $point[0], $point[1])
    # Fyne processes cursor movement on its next event tick; wheel input uses
    # that stored cursor position, unlike clicks that flush pending movement.
    $moveFrame = $State.Frame
    $wheelState = Read-State { param($value) $value.Frame -ge $moveFrame + 2 }
    $screen = New-Object AITalkerPointer.Native+Point
    $screen.X = $point[0]
    $screen.Y = $point[1]
    if (-not [AITalkerPointer.Native]::ClientToScreen($window, [ref]$screen)) {
        throw 'Não foi possível localizar a árvore para rolagem nativa.'
    }
    $scrollFrame = $wheelState.Frame
    if (-not [AITalkerPointer.Native]::PostMessage($window, 0x020A,
        [IntPtr]([int]$Delta -shl 16), [AITalkerPointer.Native]::Packed($screen.X, $screen.Y))) {
        throw 'Não foi possível enviar a rolagem nativa à árvore.'
    }
    return Read-State { param($value) $value.Frame -ge $scrollFrame + 2 }
}

function Find-BacklogTreeLabel([string]$Text) {
    $state = Read-State
    if ($null -ne (Get-VisibleBacklogTreeLabel $state $Text)) { return $state }
    # Tree rows are virtualized. Search both ends with real mouse-wheel input
    # instead of requiring every root to fit in the viewport simultaneously.
    foreach ($delta in @(120, -120)) {
        for ($step = 0; $step -lt 12; $step++) {
            $before = @($state.Backlog.Settings.TreeLabels | Sort-Object { $_.Rect.Y } | ForEach-Object {
                "$($_.Text):$($_.Rect.Y)"
            }) -join '|'
            $state = Scroll-BacklogTree $state $delta
            if ($null -ne (Get-VisibleBacklogTreeLabel $state $Text)) { return $state }
            $after = @($state.Backlog.Settings.TreeLabels | Sort-Object { $_.Rect.Y } | ForEach-Object {
                "$($_.Text):$($_.Rect.Y)"
            }) -join '|'
            if ($after -eq $before) { break }
        }
    }
    throw "O nó $Text não ficou acessível pela rolagem nativa da árvore."
}

function Click-TreeLabel([string]$Text) {
    $state = Find-BacklogTreeLabel $Text
    $node = Get-VisibleBacklogTreeLabel $state $Text
    Click-Control $node.Rect $state.Scale
}

function Click-ModalButton([string]$Text) {
    $state = Read-State
    $button = @($state.Backlog.ModalButtons | Where-Object Text -eq $Text)[0]
    if ($null -eq $button) { throw "A ação $Text não aparece no diálogo." }
    Click-Control $button.Rect $state.Scale
}

function Ready-EditorZoom([string]$Name) {
    $state = Read-State { param($value) $value.Backlog.$Name.Open }
    $openedFrame = $state.Frame
    return Read-State { param($value)
        $zoom = $value.Backlog.$Name
        $zoom.Open -and $value.Frame -ge $openedFrame + 2 -and
        $zoom.Slider.Width -gt 0 -and $zoom.Thumb.Width -gt 0 -and
        $zoom.Slider.X -ge 0 -and $zoom.Slider.Y -ge 0 -and
        $zoom.Slider.X + $zoom.Slider.Width -le $value.CanvasWidth + 1 -and
        $zoom.Slider.Y + $zoom.Slider.Height -le $value.CanvasHeight + 1
    }
}

function Test-BacklogControls {
    $state = Backlog-Command @{resize=@{Width=1100; Height=950}}
    $state = Read-State { param($value)
        [Math]::Abs($value.CanvasWidth - 1100) -lt 1 -and
        [Math]::Abs($value.CanvasHeight - 950) -lt 1
    }
    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'backlog-start.json'),
        ($state | ConvertTo-Json -Depth 12), $utf8)
    $beforeGroups = @($state.Backlog.Groups).Count
    $originalNames = @{}
    foreach ($group in $state.Backlog.Groups) { $originalNames[$group.ID] = $group.Name }
    $firstTab = $state.Backlog.Tabs[0]
    $destination = $state.Backlog.Groups[1]
    if ($state.Backlog.CreateGroup.X -ge $state.Backlog.Groups[0].Label.X) {
        throw 'O + de Novo grupo não ficou à esquerda das linhas.'
    }
    $createPoint = Control-Point $state.Backlog.CreateGroup $state.Scale
    [AITalkerPointer.Native]::Move($window, $createPoint[0], $createPoint[1])
    $createGroupFrame = $state.Frame
    $state = Read-State { param($value) $value.Frame -ge $createGroupFrame + 2 }
    Click-Control $state.Backlog.CreateGroup $state.Scale
    $state = Read-State { param($value) $value.Backlog.Groups.Count -eq $beforeGroups + 1 }
    $newGroup = $state.Backlog.Groups[-1]
    $selected = $state.Backlog.Tabs | Where-Object ID -eq $state.Backlog.Selected
    if ($selected.Group -ne $newGroup.ID -or $state.TranscriptText -ne '' -or
        $state.ProcessedText -ne '' -or $newGroup.Name -ne "Grupo $($newGroup.ID)") {
        throw 'Novo grupo não criou uma aba vazia selecionada no fim da lista.'
    }
    Drag-Tab $firstTab.Rect $destination.Row $state.Scale
    $state = Read-State { param($value)
        @($value.Backlog.Tabs | Where-Object { $_.ID -eq $firstTab.ID -and $_.Group -eq $destination.ID }).Count -eq 1
    }
    if ($state.Backlog.Groups.Count -ne $beforeGroups -or
        @($state.Backlog.Groups | Where-Object ID -eq $firstTab.Group).Count -ne 0) {
        throw 'Mover a última aba não removeu o grupo de origem vazio.'
    }
    foreach ($group in $state.Backlog.Groups) {
        if ($originalNames.ContainsKey($group.ID) -and $originalNames[$group.ID] -ne $group.Name) {
            throw 'Mover abas trocou o nome do grupo remanescente.'
        }
    }
    $workspace = Get-Content -Raw -LiteralPath (Join-Path $checkDirectory 'workspace-go-v1.json') | ConvertFrom-Json
    if (@($workspace.tabs | Where-Object { $_.id -eq $firstTab.ID -and $_.group -eq $destination.ID }).Count -ne 1) {
        throw 'A movimentação nativa não foi persistida ao soltar.'
    }
    Capture-Canvas 'backlog-groups'
    Write-Host 'BL-053/054: + lateral, seleção, arraste entre grupos, remoção de grupo vazio, nomes e persistência por mouse passaram.'

    foreach ($layout in @('column', 'stacked', 'inline')) {
        $state = Backlog-Command @{backlog=@{layout=$layout}}
        $state = Read-State { param($value) $value.Backlog.Layout -eq $layout }
        Assert-Shortcut $state.Backlog.TranscriptZoom.Button $state
        Assert-Shortcut $state.Backlog.ProcessZoom.Button $state
        Click-Control $state.Backlog.TranscriptZoom.Button $state.Scale
        $state = Ready-EditorZoom 'TranscriptZoom'
        Click-Volume $state.Backlog.TranscriptZoom.Slider $state.Scale 0.60
        $state = Read-State { param($value) $value.Backlog.TranscriptZoom.Value -eq 150 }
        if ($state.Backlog.ProcessZoom.Value -ne 100) { throw 'Zoom da Transcrição mudou o zoom de Processar.' }
        if ($layout -eq 'column') { Capture-Canvas 'backlog-zoom' }
        Double-ClickThumb $state.Backlog.TranscriptZoom.Thumb $state.Scale
        $state = Read-State { param($value) $value.Backlog.TranscriptZoom.Value -eq 100 }
        $state = Backlog-Command @{backlog=@{close_zoom=$true}}
        Click-Control $state.Backlog.ProcessZoom.Button $state.Scale
        $state = Ready-EditorZoom 'ProcessZoom'
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory ('backlog-' + $layout + '-zoom-before.json')),
            ($state | ConvertTo-Json -Depth 12), $utf8)
        Click-Volume $state.Backlog.ProcessZoom.Slider $state.Scale 1
        $state = Read-State { param($value) $value.Backlog.ProcessZoom.Value -eq 200 }
        if ($state.Backlog.TranscriptZoom.Value -ne 100) { throw 'Zoom de Processar mudou a Transcrição.' }
        Double-ClickThumb $state.Backlog.ProcessZoom.Thumb $state.Scale
        $state = Read-State { param($value) $value.Backlog.ProcessZoom.Value -eq 100 }
        $state = Backlog-Command @{backlog=@{close_zoom=$true}}
    }
    $state = Backlog-Command @{backlog=@{layout='column'}}
    Write-Host 'BL-048: lupas visíveis nos três layouts, zoom independente e duplo clique em 100% passaram.'

    Click-Control $state.Record $state.Scale
    $state = Read-State { param($value) $value.Recording }
    $state = Backlog-Command @{stream_seconds=10}
    $state = Read-State { param($value) $value.RecordSeconds -ge 10 }
    Click-Control $state.Record $state.Scale
    $state = Read-State { param($value) -not $value.Recording -and -not $value.Transcribing -and $value.PlayEnabled }
    $source = $state.AudioSource
    $sourceHash = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
    if (-not $state.Backlog.SpeedVisible -or $state.Backlog.Speed -ne 1) {
        throw 'Um áudio novo não mostrou velocidade inicial 1×.'
    }
    foreach ($label in @('1×', '1,25×', '1,5×', '2×', '2,25×', '2,5×')) {
        Click-Control $state.Backlog.SpeedButton $state.Scale
        # BL-058, approved in D-01 for 0.11, adds 0,5×/0,75× to the six existing rates.
        $state = Read-State { param($value) $value.MenuLabels.Count -eq 8 }
        $index = [array]::IndexOf($state.MenuLabels, $label)
        if ($index -lt 0) { throw "A velocidade $label não apareceu na lista." }
        Click-Control $state.MenuItems[$index] $state.Scale
        $rate = [double]::Parse($label.TrimEnd('×').Replace(',', '.'), [Globalization.CultureInfo]::InvariantCulture)
        $state = Read-State { param($value) $value.Backlog.Speed -eq $rate }
    }
    Capture-Canvas 'backlog-speed'
    Click-Control $state.Pause $state.Scale
    $state = Read-State { param($value) $value.Playing -and $value.PlaybackReady }
    if ($state.Backlog.PlayerSpeed -ne 2.5) { throw 'O player não recebeu a velocidade escolhida.' }
    Click-Control $state.StopPlayback $state.Scale
    $state = Read-State { param($value) -not $value.Playing -and $value.Backlog.Speed -eq 2.5 }
    Click-Control $state.Pause $state.Scale
    $state = Read-State { param($value) $value.Playing -and $value.PlaybackReady }
    if ($state.Backlog.PlayerSpeed -ne 2.5 -or $state.PlaybackSeconds -gt 0.8) {
        throw 'Play após Stop perdeu a velocidade ou não voltou ao início.'
    }
    Click-Control $state.StopPlayback $state.Scale
    $state = Read-State { param($value) -not $value.Playing }
    if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ne $sourceHash) {
        throw 'Mudar velocidade alterou o WAV original.'
    }
    Write-Host 'BL-036: seis velocidades anteriores no menu de oito opções, 1× por áudio novo, escolha mantida após Stop e WAV preservado passaram.'

    if (-not $state.Backlog.EvaluationVisible) { throw 'A ação de avaliar a transcrição ficou ausente.' }
    Click-Control $state.Backlog.EvaluationButton $state.Scale
    $state = Read-State { param($value) @($value.Backlog.ModalButtons | Where-Object Text -eq 'OK').Count -eq 1 }
    Click-ModalButton 'OK'
    $state = Read-State { param($value) @($value.Backlog.ModalButtons | Where-Object Text -eq 'Enviar').Count -eq 1 }
    Click-ModalButton 'Enviar'
    $state = Read-State { param($value) $null -ne $value.Backlog.EvaluationScore }
    if ($state.Backlog.EvaluationScore -ne 0) { throw 'Confirmar sem estrelas não salvou zero.' }
    Capture-Canvas 'backlog-evaluation'
    Click-Control $state.Backlog.EvaluationButton $state.Scale
    $state = Read-State { param($value) @($value.Backlog.ModalButtons | Where-Object Text -eq '☆').Count -eq 5 }
    $stars = @($state.Backlog.ModalButtons | Where-Object Text -eq '☆' | Sort-Object { $_.Rect.X })
    Click-Control $stars[-1].Rect $state.Scale
    Click-ModalButton 'OK'
    $state = Read-State { param($value) @($value.Backlog.ModalButtons | Where-Object Text -eq 'Enviar').Count -eq 1 }
    Click-ModalButton 'Enviar'
    $state = Read-State { param($value) $value.Backlog.EvaluationScore -eq 5 }
    Write-Host 'BL-038: nota zero por OK/Enviar e reavaliação para cinco estrelas passaram por mouse.'

    Click-Control $state.Settings $state.Scale
    $state = Read-State { param($value) $value.SettingsOpen -and $null -ne $value.Backlog.Settings }
    Capture-Canvas 'backlog-settings'
    Click-TreeLabel 'LLM'
    $state = Read-State { param($value) $value.Backlog.Settings.Page -eq 'llm' }
    foreach ($pair in @(
        @($state.Backlog.Settings.Model, $state.Backlog.Settings.Reasoning),
        @($state.Backlog.Settings.MergeModel, $state.Backlog.Settings.MergeReasoning))) {
        if ([Math]::Abs($pair[0].Y-$pair[1].Y) -gt 1 -or $pair[0].Width -le $pair[1].Width) {
            throw 'Modelo/Raciocínio não dividiram a linha com o raciocínio menor.'
        }
    }
    Capture-Canvas 'backlog-llm'
    Click-TreeLabel 'API'
    $state = Read-State { param($value) $value.Backlog.Settings.Page -eq 'connections/api' }
    if ($state.Backlog.Settings.APIChoices -notcontains 'Azure OpenAI / Foundry' -or
        $state.Backlog.Settings.APIChoices -notcontains 'Azure Speech · MAI-Transcribe-2') {
        throw 'O catálogo de Conexões não permite gerir os dois providers Azure.'
    }
    Assert-Shortcut $state.Backlog.Settings.APICatalog $state
    Assert-Shortcut $state.Backlog.Settings.APIProvider $state
    Click-TreeLabel 'AWS Cloud'
    $state = Read-State { param($value) $value.Backlog.Settings.Page -eq 'audio/aws' }
    if ($state.Backlog.Settings.CloudChoices -notcontains 'Nova 2.5 Sonic · ASR experimental') {
        throw 'Nova 2.5 Sonic está ausente da configuração visível.'
    }
    Click-Control $state.Backlog.Settings.Search $state.Scale
    $searchQuery = 'Identificação do executável'
    foreach ($character in $searchQuery.ToCharArray()) {
        [AITalkerPointer.Native]::PostMessage($window, 0x0102, [IntPtr][int]$character, [IntPtr]::Zero) | Out-Null
        Start-Sleep -Milliseconds 20
    }
    $state = Read-State { param($value)
        $value.Backlog.Settings.Results.Count -eq 1 -and
        $value.Backlog.Settings.SearchQuery -eq $searchQuery -and
        $value.Backlog.Settings.SearchList.Y -gt $value.Backlog.Settings.Search.Y
    }
    $result = $state.Backlog.Settings.SearchList
    $result.Height = 32
    Click-Control $result $state.Scale
    $state = Read-State { param($value)
        $value.Backlog.Settings.Page -eq 'about' -and
        $value.Backlog.Settings.CopyIdentity.Width -gt 0 -and
        $value.Backlog.Settings.CopyIdentity.Y -gt 0
    }
    if (-not $state.Backlog.Settings.About.Contains($state.Backlog.Version) -or
        -not $state.Backlog.Settings.About.Contains($state.Backlog.Revision)) {
        throw 'Sobre não identifica a versão/revisão do executável.'
    }
    Click-Control $state.Backlog.Settings.CopyIdentity $state.Scale
    $null = Read-State { param($value) Test-Path -LiteralPath (Join-Path $checkDirectory 'copied-text.json') }
    $copied = Get-Content -Raw -LiteralPath (Join-Path $checkDirectory 'copied-text.json') | ConvertFrom-Json
    if ($copied.text -ne $state.Backlog.Settings.About) { throw 'Copiar identificação diverge de Sobre.' }
    Capture-Canvas 'backlog-about'
    $state = Backlog-Command @{backlog=@{settings_page='data/cleanup'}}
    if ($state.Backlog.Settings.CleanupPeriod.Width -eq 0 -or
        $state.Backlog.Settings.CleanupSummary -eq '') {
        throw 'Limpeza não oferece período e prévia de seleção.'
    }
    Capture-Canvas 'backlog-cleanup'
    $state = Backlog-Command @{close_settings=$true}
    Write-Host 'BL-042/044/047/049/050/052/056: árvore e páginas, linha de modelos, catálogo Azure/Nova, busca e Sobre por mouse passaram.'

    foreach ($width in @(480, 320)) {
        $state = Backlog-Command @{resize=@{Width=$width; Height=950}}
        $state = Read-State { param($value) [Math]::Abs($value.CanvasWidth-$width) -lt 1 }
        Click-Control $state.Settings $state.Scale
        $state = Read-State { param($value)
            $value.SettingsOpen -and $value.Backlog.Settings.Tree.Width -ge 200
        }
        $navigation = @()
        foreach ($root in @(
            @{Label='Conexões'; Page='connections'}, @{Label='Áudio'; Page='audio'},
            @{Label='Visual'; Page='visual'}, @{Label='Vídeo'; Page='video'},
            @{Label='LLM'; Page='llm'}, @{Label='Instruções'; Page='instructions'},
            @{Label='Dados'; Page='data'}, @{Label='Sobre'; Page='about'}
        )) {
            $state = Find-BacklogTreeLabel $root.Label
            foreach ($visible in $state.Backlog.Settings.TreeLabels) {
                if ($visible.Rect.Width + 1 -lt $visible.MinWidth) {
                    throw "O nome $($visible.Text) ficou cortado na árvore com largura $width."
                }
            }
            $node = Get-VisibleBacklogTreeLabel $state $root.Label
            $tree = $state.Backlog.Settings.Tree
            if ($node.Rect.X -lt $tree.X -or
                $node.Rect.X + $node.Rect.Width -gt $tree.X + $tree.Width + 1) {
                throw "A categoria $($root.Label) ficou fora da árvore na largura $width."
            }
            Click-Control $node.Rect $state.Scale
            $state = Read-State { param($value) $value.Backlog.Settings.Page -eq $root.Page }
            if ([Math]::Abs($state.CanvasWidth-$width) -gt 1) {
                throw "Navegar para $($root.Label) ampliou a janela estreita."
            }
            $navigation += @{Label=$root.Label; Page=$state.Backlog.Settings.Page;
                CanvasWidth=$state.CanvasWidth; Tree=$tree; LabelRect=$node.Rect; MinWidth=$node.MinWidth}
        }
        [System.IO.File]::WriteAllText((Join-Path $checkDirectory "backlog-settings-navigation-$width.json"),
            ($navigation | ConvertTo-Json -Depth 6), $utf8)
        Capture-Canvas "backlog-settings-narrow-$width"
        $state = Backlog-Command @{close_settings=$true}
    }
    Write-Host 'BL-044/056: nomes, rolagem nativa e navegação para as oito categorias passaram nas larguras inicial e mínima.'

    Click-Control $state.NewAudio $state.Scale
    $state = Read-State { param($value) $value.AudioSource -eq '' -and -not $value.Backlog.SpeedVisible }
    if (-not (Test-Path -LiteralPath $source)) { throw 'Novo áudio removeu a gravação original.' }
    Click-Control $state.Close $state.Scale
    if (-not $process.WaitForExit(10000) -or $process.ExitCode -ne 0) {
        throw 'O fechamento do smoke não concluiu normalmente.'
    }
}
