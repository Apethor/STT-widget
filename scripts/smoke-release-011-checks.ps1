# Loaded by smoke-window.ps1; every action targets its isolated native window.

function Ready-Release011Menu([int]$Count = 0, [string]$Label = '') {
    $state = Read-State { param($value)
        ($Count -eq 0 -or $value.MenuLabels.Count -eq $Count) -and
        ($Label -eq '' -or ($value.MenuLabels -contains $Label))
    }
    $openedFrame = $state.Frame
    return Read-State { param($value)
        $value.Frame -ge $openedFrame+2 -and
        ($Count -eq 0 -or $value.MenuLabels.Count -eq $Count) -and
        ($Label -eq '' -or ($value.MenuLabels -contains $Label))
    }
}

function Click-Release011Check($Rect, $Scale) {
    # Fyne Check only accepts taps within the icon/label's natural width.
    $activeRect = [pscustomobject]@{
        X = $Rect.X
        Y = $Rect.Y
        Width = [Math]::Min(24, $Rect.Width)
        Height = $Rect.Height
    }
    Click-Volume $activeRect $Scale 0.5
}

function Test-Release011Controls {
    $state = Backlog-Command @{ resize=@{Width=1100;Height=950}; scroll_top=$true }
    $state = Read-State { param($value) [Math]::Abs($value.CanvasWidth-1100) -lt 1 -and $value.ScrollY -eq 0 }
    if ($state.Backlog.WindowTitle -ne 'STT-widget' -or
        ($state.Backlog.HeaderTexts -join '|') -match '0\.11|0\.10') {
        throw 'A versão continua visível no título.'
    }
    foreach ($control in @($state.Backlog.Soundboard, $state.Backlog.Video)) {
        if ([Math]::Abs($state.Camera.Width-$control.Width) -gt 1 -or
            [Math]::Abs($state.Camera.Height-$control.Height) -gt 1) {
            throw 'Câmera, botoneira e vídeo não têm o mesmo tamanho.'
        }
    }
    foreach ($control in @($state.CloudButton, $state.QualityButton)) {
        if ([Math]::Abs($control.Height-$state.ProcessCollapse.Height) -gt 2) {
            throw 'Um controle da Transcrição não acompanha o tamanho compacto de Processar texto.'
        }
    }
    if ([Math]::Abs($state.Backlog.TranscriptCopy.Height-$state.Backlog.ProcessCopy.Height) -gt 2 -or
        [Math]::Abs($state.Backlog.TranscriptCopy.Width-20) -gt 1 -or
        [Math]::Abs($state.CloudButton.Width-20) -gt 1) {
        throw 'Os botões compactos não conservaram seu tamanho base.'
    }
    $editorGap = $state.Backlog.TranscriptEditor.Y -
        ($state.Backlog.TranscriptSource.Y+$state.Backlog.TranscriptSource.Height)
    if ($editorGap -lt 0 -or $editorGap -gt 8.1) {
        throw "O espaço acima do editor da Transcrição não ficou compacto: $editorGap."
    }
    Capture-Canvas 'release011-title-controls'
    Write-Host 'BL-060/064/065: título sem versão, controles compactos iguais e espaçamento superior do editor conferidos.'

    $count = @($state.Backlog.Groups).Count
    Click-Control $state.Backlog.CreateGroup $state.Scale
    $state = Read-State { param($value) @($value.Backlog.Groups).Count -eq $count+1 }
    $closed = $state.Backlog.Groups[-1]
    Click-Control $closed.More $state.Scale
    $state = Ready-Release011Menu -Label 'Fechar grupo…'
    $index = [array]::IndexOf($state.MenuLabels, 'Fechar grupo…')
    Click-Control $state.MenuItems[$index] $state.Scale
    $state = Read-State { param($value) @($value.Backlog.Groups).Count -eq $count }
    Click-Control $state.Backlog.CreateGroup $state.Scale
    $state = Read-State { param($value) @($value.Backlog.Groups).Count -eq $count+1 }
    $replacement = $state.Backlog.Groups[-1]
    if ($replacement.ID -le $closed.ID -or $replacement.Name -ne $closed.Name) {
        throw 'Fechar/criar grupo não reutilizou o menor número livre preservando a identidade.'
    }
    Capture-Canvas 'release011-groups'
    Write-Host 'BL-059: criação/fechamento pelo menu e reutilização do menor número livre passaram.'

    # Verify the three presentations first without replacing the existing tab.
    foreach ($mode in @('small','medium','large')) {
        $state = Backlog-Command @{recorder_size=$mode;resize=@{Width=1400;Height=950};scroll_top=$true}
        $state = Read-State { param($value) $value.RecorderMode -eq $mode -and [Math]::Abs($value.CanvasWidth-1400) -lt 1 }
        $preferredHeight = $state.RecordPanel.Height
        $state = Backlog-Command @{resize=@{Width=480;Height=950};scroll_top=$true}
        $state = Read-State { param($value) $value.RecorderMode -eq $mode -and [Math]::Abs($value.CanvasWidth-480) -lt 1 }
        $narrowHeight = $state.RecordPanel.Height
        if ($state.RecordPanel.Height -le 40) { throw "O modo $mode ficou recolhido." }
        Capture-Canvas "release011-recorder-$mode-narrow"
        $state = Backlog-Command @{ resize=@{Width=1400;Height=950};scroll_top=$true }
        $state = Read-State { param($value) $value.RecorderMode -eq $mode -and [Math]::Abs($value.CanvasWidth-1400) -lt 1 }
        if ($mode -ne 'small' -and [Math]::Abs($state.RecordPanel.Height-$narrowHeight) -gt 2) {
            throw "Alargar alterou a altura do modo ${mode}: $narrowHeight / $($state.RecordPanel.Height)."
        }
        if ($mode -eq 'small' -and ($state.RecordPanel.Height -gt $narrowHeight+2 -or
            [Math]::Abs($state.RecordPanel.Height-$preferredHeight) -gt 2)) {
            throw 'Pequeno não conservou a altura escolhida depois da adaptação temporária estreita.'
        }
        if ($mode -eq 'large' -and $state.RecorderWave.Y -le $state.RecorderClock.Y+10) {
            throw 'Grande virou uma linha compacta ao alargar.'
        }
        if ($mode -eq 'medium' -and $state.Record.Y -le $state.RecorderWave.Y+5) {
            throw 'Médio virou uma linha compacta ao alargar.'
        }
        if ($mode -eq 'small') {
            $clockMiddle = $state.RecorderClock.Y+$state.RecorderClock.Height/2
            $waveMiddle = $state.RecorderWave.Y+$state.RecorderWave.Height/2
            if ([Math]::Abs($clockMiddle-$waveMiddle) -gt 2) {
                throw 'Pequeno largo não alinhou relógio e waveform na mesma linha.'
            }
        }
        Capture-Canvas "release011-recorder-$mode-wide"
    }
    Write-Host 'BL-062: Pequeno/Médio/Grande mantiveram modo e altura ao alargar; Médio e Grande conservam suas linhas.'

    $state = Backlog-Command @{recorder_size='small';resize=@{Width=1400;Height=950}}
    Click-Control $state.Record $state.Scale
    $state = Read-State { param($value) $value.Recording }
    $state = Backlog-Command @{stream_seconds=10}
    $state = Read-State { param($value) $value.RecordSeconds -ge 10 -and $value.CopyAudioPathVisible }
    $caption = $state.Backlog.RecorderCaption
    if ($caption.X+$caption.Width -gt $state.RecorderClock.X+1 -or
        $state.CopyAudioPath.X -ge $caption.X) {
        throw 'Pequeno não posicionou cópia/arquivo antes do relógio.'
    }
    $messageMiddle = $state.Backlog.RecorderMessage.Y+$state.Backlog.RecorderMessage.Height/2
    $panelMiddle = $state.RecordPanel.Y+$state.RecordPanel.Height/2
    if ([Math]::Abs($messageMiddle-$panelMiddle) -gt 4) {
        throw 'A mensagem do Pequeno não ficou centralizada na altura.'
    }
    $state = Backlog-Command @{recorder_size='medium';resize=@{Width=480;Height=950}}
    if ($state.Backlog.RecorderMessage.Y -lt $state.RecorderWave.Y+$state.RecorderWave.Height-1) {
        throw 'A mensagem do Médio estreito não ficou abaixo da waveform.'
    }
    $state = Backlog-Command @{resize=@{Width=1400;Height=950}}
    if ($state.Backlog.RecorderMessage.Y -gt $state.RecorderWave.Y+1) {
        throw 'A mensagem do Médio largo não subiu para a faixa superior.'
    }
    Click-Control $state.Backlog.RecorderCollapse $state.Scale
    $state = Read-State { param($value) $value.RecorderCollapsed -and $value.Recording -and $value.RecordPanel.Height -lt 80 }
    Assert-Shortcut $state.RecorderRestore $state
    Assert-Shortcut $state.RecorderStop $state
    Capture-Canvas 'release011-recorder-collapsed'
    Click-Control $state.RecorderStop $state.Scale
    $state = Read-State { param($value) -not $value.Recording -and -not $value.Transcribing -and $value.PlayEnabled }
    if (-not $state.RecorderCollapsed -or -not (Test-Path -LiteralPath $state.AudioSource)) {
        throw 'Parar com o gravador recolhido perdeu o áudio ou o estado da aba.'
    }
    Click-Control $state.RecorderRestore $state.Scale
    $state = Read-State { param($value) -not $value.RecorderCollapsed }
    foreach ($layout in @('stacked','inline','column')) {
        $state = Backlog-Command @{backlog=@{layout=$layout}}
        $state = Read-State { param($value) $value.Backlog.Layout -eq $layout }
        $expandedHeight = $state.RecordPanel.Height
        Click-Control $state.Backlog.RecorderCollapse $state.Scale
        $state = Read-State { param($value) $value.RecorderCollapsed -and $value.RecordPanel.Height -lt 80 }
        Capture-Canvas "release011-recorder-collapsed-$layout"
        Click-Control $state.RecorderRestore $state.Scale
        $state = Read-State { param($value) -not $value.RecorderCollapsed }
        if ($state.RecordPanel.Height -lt $expandedHeight-2) {
            throw "Expandir perdeu a altura no layout $layout."
        }
    }
    Write-Host 'BL-057/066: posições por modo, recolhimento durante captura e Parar acessível passaram.'

    $source = $state.AudioSource
    $hash = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
    foreach ($label in @('0,5×','0,75×')) {
        Click-Control $state.Backlog.SpeedButton $state.Scale
        $state = Ready-Release011Menu -Count 8
        $index = [array]::IndexOf($state.MenuLabels, $label)
        if ($index -lt 0) { throw "Velocidade $label ausente." }
        Click-Control $state.MenuItems[$index] $state.Scale
        $rate = [double]::Parse($label.TrimEnd('×').Replace(',','.'),[Globalization.CultureInfo]::InvariantCulture)
        $state = Read-State { param($value) $value.Backlog.Speed -eq $rate }
        Click-Control $state.Pause $state.Scale
        $state = Read-State { param($value) $value.Playing -and $value.PlaybackReady -and $value.Backlog.PlayerSpeed -eq $rate }
        Click-Control $state.StopPlayback $state.Scale
        $state = Read-State { param($value) -not $value.Playing -and $value.Backlog.Speed -eq $rate }
    }
    if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ne $hash) {
        throw 'As velocidades lentas alteraram o WAV original.'
    }
    Capture-Canvas 'release011-slow-playback'
    Write-Host 'BL-058: 0,5×/0,75× pelo menu/player, escolha após Stop e original preservado passaram.'

    $state = Backlog-Command @{resize=@{Width=480;Height=950};scroll_top=$true}
    Click-Control $state.Settings $state.Scale
    $state = Read-State { param($value) $value.SettingsOpen -and -not $value.Backlog.Settings.Loading }
    $state = Backlog-Command @{backlog=@{settings_page='connections/aws';draft_aws_profile='smoke-draft';draft_aws_region='us-east-1';draft_bucket='synthetic-bucket'}}
    $narrowWidth = $state.Backlog.Settings.DialogSize.Width
    Capture-Canvas 'release011-settings-narrow'
    $state = Backlog-Command @{resize=@{Width=1100;Height=950}}
    $settings = $state.Backlog.Settings
    if ($settings.DialogSize.Width -lt $narrowWidth+400 -or $settings.Page -ne 'connections/aws' -or
        $settings.AWSProfile -ne 'smoke-draft' -or $settings.AWSRegion -ne 'us-east-1' -or
        $settings.BucketName -ne 'synthetic-bucket') {
        throw 'O modal não acompanhou a janela ou perdeu página/valores em edição.'
    }
    Capture-Canvas 'release011-settings-wide'
    if ($settings.BucketEnabled -or -not $settings.BucketFieldDisabled -or -not $settings.BucketTestDisabled) {
        throw 'S3 começou habilitado sem escolha explícita.'
    }
    Click-Release011Check $settings.BucketUse $state.Scale
    $state = Read-State { param($value) $value.Backlog.Settings.BucketEnabled -and -not $value.Backlog.Settings.BucketFieldDisabled }
    Click-Control $state.Backlog.Settings.BucketTest $state.Scale
    $state = Read-State { param($value) $value.Backlog.Settings.BucketResult.Contains('Envio, leitura e limpeza confirmados') }
    Capture-Canvas 'release011-s3'
    Click-Release011Check $state.Backlog.Settings.BucketUse $state.Scale
    $state = Read-State { param($value) -not $value.Backlog.Settings.BucketEnabled -and $value.Backlog.Settings.BucketName -eq 'synthetic-bucket' }
    Write-Host 'BL-061/063: modal redimensionado sem perder rascunho; checkbox/nome e teste S3 com contrato simulado no canvas passaram.'

    $state = Backlog-Command @{backlog=@{settings_page='audio/capture'}}
    $state = Read-State { param($value) $value.Backlog.Settings.Page -eq 'audio/capture' -and
        $value.Backlog.Settings.DeviceRefresh.X -gt 0 -and $value.Backlog.Settings.DeviceRefresh.Y -gt 0 }
    $settings = $state.Backlog.Settings
    if ($settings.MicrophoneChoices.Count -lt 2 -or $settings.PlaybackChoices.Count -lt 2 -or
        $settings.DeviceRefresh.Width -le 0 -or $settings.Playback.Width -le 0) {
        throw 'Configurações de áudio não mostram atualização/entradas/saída Sistema.'
    }
    $enumerations = $state.Backlog.DeviceEnumerationCount
    Click-Control $settings.DeviceRefresh $state.Scale
    $state = Read-State { param($value) -not $value.Backlog.Settings.Loading -and $value.Backlog.DeviceEnumerationCount -ge $enumerations+2 }
    Capture-Canvas 'release011-audio-devices'
    Click-TreeLabel 'Vídeo'
    $state = Read-State { param($value) $value.Backlog.Settings.Page -eq 'video' -and
        $value.Backlog.Settings.VideoPreset.X -gt 0 -and $value.Backlog.Settings.VideoPreset.Y -gt 0 }
    foreach ($choice in @('Baixa','Média','Alta')) {
        Click-Control $state.Backlog.Settings.VideoPreset $state.Scale
        $state = Ready-Release011Menu -Count 3
        $index = [array]::IndexOf($state.MenuLabels,$choice)
        if ($index -lt 0) { throw "Preset $choice não apareceu." }
        Click-Control $state.MenuItems[$index] $state.Scale
        $state = Read-State { param($value) $value.Backlog.Settings.SelectedVideo -eq $choice }
    }
    Capture-Canvas 'release011-video-presets'
    $state = Backlog-Command @{close_settings=$true}
    Click-Control $state.Device $state.Scale
    $state = Read-State { param($value) $value.DevicesOpen -and $value.DevicesReady -and
        $value.Backlog.CaptureDeviceRefresh.X -gt 0 -and $value.Backlog.CaptureDeviceRefresh.Y -gt 0 }
    $enumerations = $state.Backlog.DeviceEnumerationCount
    Click-Control $state.Backlog.CaptureDeviceRefresh $state.Scale
    $state = Read-State { param($value) $value.DevicesReady -and $value.Backlog.DeviceEnumerationCount -ge $enumerations+2 }
    Capture-Canvas 'release011-device-refresh'
    $state = Backlog-Command @{close_devices=$true}
    Click-Control $state.Folder $state.Scale
    $state = Ready-Release011Menu -Count 3
    if (-not ($state.MenuLabels -contains 'Captura de vídeo')) {
        throw 'O menu de pastas não oferece Capturas de vídeo.'
    }
    Capture-Canvas 'release011-folders'
    $state = Backlog-Command @{close_menu=$true}
    Write-Host 'BL-067/069/070/072: pasta de vídeos, atualização nos dois locais, saída Sistema e três presets por mouse passaram.'

    [System.IO.File]::WriteAllText((Join-Path $checkDirectory 'release011-final.json'),
        ($state | ConvertTo-Json -Depth 12),$utf8)
    Click-Control $state.Close $state.Scale
    if (-not $process.WaitForExit(10000) -or $process.ExitCode -ne 0) {
        throw 'O smoke da 0.11 não encerrou normalmente.'
    }
}
