# Loaded by smoke-window.ps1; only the isolated smoke PID and data are used.

function Click-Release012Menu([string]$Label) {
    $state = Ready-Release011Menu -Label $Label
    $index = [array]::IndexOf($state.MenuLabels, $Label)
    Click-Volume $state.MenuItems[$index] $state.Scale 0.5
}

function Select-Release012Choice($Rect, [string]$Label) {
    $state = Read-State
    Click-Volume $Rect $state.Scale 0.5
    Click-Release012Menu $Label
}

function Assert-Release012ReadyPixels([string]$Capture) {
    Add-Type -AssemblyName System.Drawing
    $state = Read-State
    if (-not $state.Release012.ReadySuccess) { throw 'Pronto não usa SuccessImportance.' }
    $bitmap = [System.Drawing.Bitmap]::new((Join-Path $checkDirectory ($Capture + '.png')))
    try {
        $rect = $state.Backlog.RecorderMessage
        $green = 0
        for ($y = [int]($rect.Y*$state.Scale); $y -lt [Math]::Min($bitmap.Height, ($rect.Y+$rect.Height)*$state.Scale); $y++) {
            for ($x = [int]($rect.X*$state.Scale); $x -lt [Math]::Min($bitmap.Width, ($rect.X+$rect.Width)*$state.Scale); $x++) {
                $pixel = $bitmap.GetPixel($x, $y)
                if ($pixel.G -gt 75 -and $pixel.G -gt $pixel.R*1.25 -and $pixel.G -gt $pixel.B*1.1) { $green++ }
            }
        }
        if ($green -lt 8) { throw "A mensagem Pronto não apresenta pixels verdes: $green." }
    } finally { $bitmap.Dispose() }
}

function Open-Release012Settings([string]$PageLabel, [string]$PageID) {
    $state = Read-State
    if (-not $state.SettingsOpen) {
        Click-Control $state.Settings $state.Scale
        $null = Read-State { param($value) $value.SettingsOpen -and -not $value.Backlog.Settings.Loading }
    }
    Click-TreeLabel $PageLabel
    return Read-State { param($value) $value.Backlog.Settings.Page -eq $PageID }
}

function Choose-Release012Mode([string]$Mode, [string]$Label) {
    $state = Read-State
    Click-Volume $state.Stream $state.Scale 0.5
    $null = Ready-Release011Menu -Count 3 -Label $Label
    Click-Release012Menu $Label
    return Read-State { param($value) $value.Release012.Mode -eq $Mode -and $value.MenuLabels.Count -eq 0 }
}

function Test-Release012Controls {
    $state = Backlog-Command @{resize=@{Width=1100;Height=950};recorder_size='small';scroll_top=$true;release012_prepare=$true}
    $state = Read-State { param($value) $value.Release012.ReadySuccess -and $value.CanvasWidth -gt 1000 -and
        $value.Backlog.Groups.Count -eq 2 -and $value.Backlog.Selected -eq 1 -and
        $value.ConversationTurns -eq 2 -and $value.PromptCount -eq 2 -and
        $value.ProcessedText -eq 'Texto processado sintético.' }
    $originalTranscript, $originalProcessed = $state.TranscriptText, $state.ProcessedText
    $null = Backlog-Command @{group_name='Grupo user fixed'}
    Capture-Canvas 'release012-ready-pt'
    Assert-Release012ReadyPixels 'release012-ready-pt'
    $state = Open-Release012Settings 'Visual' 'visual'
    Capture-Canvas 'release012-settings-pt'
    Select-Release012Choice $state.Release012.LanguageSelect 'Inglês'
    $state = Read-State { param($value)
        $value.Release012.Language -eq 'en' -and $value.Release012.PersistedLanguage -eq 'en' -and
        $value.Backlog.Settings.Page -eq 'visual' -and $value.Release012.TranscriptLabel -eq 'Transcription'
    }
    if ($state.TranscriptText -ne $originalTranscript -or $state.ProcessedText -ne $originalProcessed -or
        $state.Backlog.Groups[0].Name -ne 'Grupo user fixed' -or $state.Backlog.Groups[1].Name -ne 'Group 2') {
        throw 'A troca imediata alterou conteúdo ou nomes personalizados, ou não traduziu o grupo automático.'
    }
    Capture-Canvas 'release012-settings-en'
    $null = Backlog-Command @{close_settings=$true}
    $state = Read-State { param($value) -not $value.SettingsOpen }
    Capture-Canvas 'release012-ready-en'
    Assert-Release012ReadyPixels 'release012-ready-en'
    foreach ($size in @('narrow','wide')) {
        $width = if ($size -eq 'narrow') { 480 } else { 1400 }
        $null = Backlog-Command @{resize=@{Width=$width;Height=950};scroll_top=$true}
        $null = Read-State { param($value) [Math]::Abs($value.CanvasWidth-$width) -lt 1 }
        Capture-Canvas "release012-en-$size"
    }
    Click-Control (Read-State).Folder (Read-State).Scale
    $state = Ready-Release011Menu -Label 'Audio recordings'
    if (-not ($state.MenuLabels -contains 'Screen capture')) { throw 'O menu de pastas não usa os nomes aprovados em inglês.' }
    $null = Backlog-Command @{close_menu=$true}
    Write-Host 'BL-074/075/079: Pronto verde, idioma imediato e persistido, textos/nomes próprios preservados e pastas traduzidas.'

    foreach ($choice in @(
        @{mode='stream'; label='Stream transcription'},
        @{mode='recording'; label='Recording and text'},
        @{mode='conversation'; label='Speech to speech'}
    )) {
        $state = Choose-Release012Mode $choice.mode $choice.label
        $state = Open-Release012Settings 'Audio' 'audio'
        if ($state.Release012.SettingsMode -ne (@{
            stream='Transcrição Stream';recording='Gravação e texto';conversation='Conversa ao vivo'
        }[$choice.mode])) { throw 'O modo do menu não foi refletido em Configurações → Áudio.' }
        if ($state.Release012.ModelChoices.Count -ne 2 -or
            $state.Release012.ModelID -ne 'amazon.nova-2-5-sonic') { throw 'Nova 2.5 não é o padrão ou não há as duas versões comprovadas.' }
        $null = Backlog-Command @{close_settings=$true}
    }
    $state = Open-Release012Settings 'Audio' 'audio'
    Select-Release012Choice $state.Release012.AudioModeSelect 'Recording and text'
    $state = Read-State { param($value) $value.Release012.Mode -eq 'recording' }
    Select-Release012Choice $state.Release012.AudioModeSelect 'Speech to speech'
    $state = Read-State { param($value) $value.Release012.Mode -eq 'conversation' }
    $state = Open-Release012Settings 'Speech to speech' 'audio/conversation'
    $state = Read-State { param($value) $value.Release012.ModelSelect.X -gt 0 -and
        $value.Release012.ModelSelect.Y -gt 0 }
    Select-Release012Choice $state.Release012.ModelSelect 'amazon.nova-2-sonic-v1:0'
    $state = Read-State { param($value) $value.Release012.ModelID -eq 'amazon.nova-2-sonic-v1:0' }
    Select-Release012Choice $state.Release012.ModelSelect 'amazon.nova-2-5-sonic'
    $state = Read-State { param($value) $value.Release012.ModelID -eq 'amazon.nova-2-5-sonic' }
    Capture-Canvas 'release012-models'
    $null = Backlog-Command @{close_settings=$true}
    $null = Backlog-Command @{resize=@{Width=1400;Height=950};scroll_bottom=$true}
    $state = Read-State { param($value) $value.Release012.ResponseVisible -and $value.Release012.ResponseVoice.Width -gt 0 }
    if (-not $state.Release012.Autoplay) { throw 'Reprodução automática não começou habilitada.' }
    Click-Release011Check $state.Release012.ResponseAuto $state.Scale
    $state = Read-State { param($value) -not $value.Release012.Autoplay }
    Click-Release011Check $state.Release012.ResponseAuto $state.Scale
    $state = Read-State { param($value) $value.Release012.Autoplay }
    Select-Release012Choice $state.Release012.ResponseVoice 'leo'
    $state = Read-State { param($value) $value.Release012.VoiceID -eq 'leo' }
    Select-Release012Choice $state.Release012.ResponseOutput 'Saída sintética'
    $state = Read-State { param($value) $value.Release012.Output -eq 'Saída sintética' }
    $null = Backlog-Command @{scroll_top=$true}
    $state = Read-State
    Click-Volume $state.Record $state.Scale 0.5
    $state = Read-State { param($value) $value.Recording -and $value.Release012.Mode -eq 'conversation' }
    $null = Backlog-Command @{stream_seconds=2}
    $state = Read-State { param($value)
        $value.Release012.ResponseHistory.Count -eq 2 -and
        $value.Release012.ResponseHistory[1].Complete -and $value.Release012.InputBytes -gt 0
    } -TimeoutSeconds 15
    if ($state.ProcessedText -ne $originalProcessed -or
        $state.Release012.ResponseText -ne 'Assistant turn 2 · Grupo unchanged.' -or
        -not ($state.TranscriptText.Contains('User turn 2 · Salvar unchanged.'))) {
        throw 'As duas respostas de uma sessão não apareceram integralmente ou alteraram Processar texto.'
    }
    foreach ($turn in $state.Release012.ResponseHistory) {
        if (-not (Test-Path -LiteralPath $turn.audio_path) -or (Get-Item -LiteralPath $turn.audio_path).Length -le 44) {
            throw 'O áudio PCM de uma resposta não foi persistido.'
        }
    }
    Capture-Canvas 'release012-live-two-turns'
    $null = Backlog-Command @{scroll_bottom=$true}
    $state = Read-State
    Click-Volume $state.Release012.ResponseInterrupt $state.Scale 0.5
    $state = Read-State { param($value) $value.Release012.ResponseHistory[1].Interrupted }
    if ($state.Release012.ResponseText -ne 'Assistant turn 2 · Grupo unchanged.') {
        throw 'Interromper apagou conteúdo recebido.'
    }
    $null = Backlog-Command @{scroll_top=$true}
    $state = Read-State
    Click-Volume $state.Record $state.Scale 0.5
    $state = Read-State { param($value) -not $value.Recording -and $value.Release012.ResponseHistory.Count -eq 2 }
    $null = Backlog-Command @{scroll_bottom=$true}
    $state = Read-State
    Click-Volume $state.Release012.ResponseBack $state.Scale 0.5
    $state = Read-State { param($value) $value.Release012.ResponseIndex -eq 0 }
    if ($state.Release012.ResponseText -ne 'Assistant turn 1 · Grupo unchanged.') { throw 'Histórico não voltou à primeira resposta.' }
    Click-Volume $state.Release012.ResponsePlay $state.Scale 0.5
    $state = Read-State { param($value) $value.Release012.PlayerDuration -gt 0 }
    Click-Volume $state.Release012.ResponsePlay $state.Scale 0.5
    $state = Read-State { param($value) $value.Release012.PlayerPaused }
    Click-Volume $state.Release012.ResponsePosition $state.Scale 0.6
    $state = Read-State { param($value) $value.Release012.PlayerPaused -and $value.Release012.PlayerPosition -gt 4 }
    Click-Volume $state.Release012.ResponseStop $state.Scale 0.5
    $state = Read-State { param($value) -not $value.Release012.PlayerActive -and
        $value.Release012.PlayerDuration -eq 0 -and $value.Release012.PlayerPosition -eq 0 }
    Click-Volume $state.Release012.ResponseVolume $state.Scale 0.75
    $state = Read-State { param($value) $value.Release012.PreferenceVolume -gt 100 -and
        $value.Release012.PersistedVolume -eq $value.Release012.PreferenceVolume }
    Click-Volume $state.Release012.ResponsePlay $state.Scale 0.5
    $state = Read-State { param($value) $value.Release012.PlayerDuration -gt 0 -and $value.Release012.PlayerVolume -gt 100 }
    Click-Volume $state.Release012.ResponseNext $state.Scale 0.5
    $state = Read-State { param($value) $value.Release012.ResponseIndex -eq 1 }
    Capture-Canvas 'release012-live-history'
    $null = Backlog-Command @{scroll_top=$true}
    $state = Choose-Release012Mode 'recording' 'Recording and text'
    if (-not $state.Release012.ProcessVisible -or $state.ProcessedText -ne $originalProcessed -or
        $state.Release012.ResponseHistory.Count -ne 2 -or $state.ConversationTurns -ne 2 -or $state.PromptCount -lt 2) {
        throw 'Voltar à gravação perdeu resultado ou histórico da conversa/processamento.'
    }
    Capture-Canvas 'release012-return-process'
    Write-Host 'BL-073: três modos pela UI, ajustes sincronizados, modelos/vozes/saída, duas respostas e histórico/player preservados.'

    $state = Open-Release012Settings 'Appearance' 'visual'
    Select-Release012Choice $state.Release012.LanguageSelect 'Portuguese (Brazil)'
    $state = Read-State { param($value) $value.Release012.Language -eq 'pt-BR' -and $value.Release012.PersistedLanguage -eq 'pt-BR' }
    $null = Backlog-Command @{close_settings=$true}
    foreach ($size in @('narrow','wide')) {
        $width = if ($size -eq 'narrow') { 480 } else { 1400 }
        $null = Backlog-Command @{resize=@{Width=$width;Height=950};scroll_top=$true}
        $null = Read-State { param($value) [Math]::Abs($value.CanvasWidth-$width) -lt 1 }
        Capture-Canvas "release012-pt-$size"
    }
    . (Join-Path $PSScriptRoot 'smoke-soundboard-012.ps1')
    Test-Release012Soundboard
    $state = Read-State
    [IO.File]::WriteAllText((Join-Path $checkDirectory 'release012-final.json'), (@{
        language = $state.Release012.Language
        persisted_language = $state.Release012.PersistedLanguage
        conversation_turns = @($state.Release012.ResponseHistory).Count
        processing_turns = $state.ConversationTurns
        mode = $state.Release012.Mode
        processed_text = $state.ProcessedText
        ready_green_pixels = $true
        native_settings_language = $true
        native_modes_and_response_controls = $true
        soundboard = $state.Soundboard
    } | ConvertTo-Json -Depth 6), $utf8)
}
