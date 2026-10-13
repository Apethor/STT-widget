# Loaded by smoke-window.ps1. Reuses one owned GUID directory across new PIDs.

function Assert-UndoWindow {
    [uint32]$undoOwner = 0
    [AITalkerPointer.Native]::GetWindowThreadProcessId($window, [ref]$undoOwner) | Out-Null
    if ($window -eq [IntPtr]::Zero -or $null -eq $process -or $undoOwner -ne $process.Id) {
        throw 'A ação de Desfazer não pertence ao processo isolado.'
    }
}

function Save-UndoEvidence([string]$Name, $Value) {
    if ([IO.Path]::GetFileName($Name) -ne $Name) { throw 'Nome de evidência inválido.' }
    [IO.File]::WriteAllText((Join-Path $checkDirectory $Name),
        ($Value | ConvertTo-Json -Depth 40), $utf8)
}

function Get-UndoWorkspace {
    return Get-Content -Raw -LiteralPath (Join-Path $checkDirectory 'workspace-go-v1.json') |
        ConvertFrom-Json
}

function Get-UndoDeadline($State) {
    $rawDeadline = $State.Backlog.UndoDeadlineUTC
    if ($rawDeadline -is [DateTimeOffset]) { return $rawDeadline.ToUniversalTime() }
    if ($rawDeadline -is [DateTime]) {
        return [DateTimeOffset]::new($rawDeadline.ToUniversalTime())
    }
    if ([string]::IsNullOrWhiteSpace($rawDeadline)) {
        throw 'A entrada de Desfazer não exportou seu prazo absoluto.'
    }
    return [DateTimeOffset]::Parse($rawDeadline,
        [Globalization.CultureInfo]::InvariantCulture).ToUniversalTime()
}

function Click-UndoTab([uint64]$ID, [switch]$CloseTab) {
    $state = Read-State { param($value)
        @($value.Backlog.Tabs | Where-Object ID -eq $ID).Count -eq 1
    }
    $tab = @($state.Backlog.Tabs | Where-Object ID -eq $ID)[0]
    Assert-UndoWindow
    $rect = if ($CloseTab) { $tab.Close } else { $tab.Rect }
    Click-Control $rect $state.Scale
    if ($CloseTab) {
        $state = Read-State { param($value)
            @($value.Backlog.Tabs | Where-Object ID -eq $ID).Count -eq 0 -or
            $value.Backlog.ModalButtons.Count -gt 0
        }
        if (@($state.Backlog.Tabs | Where-Object ID -eq $ID).Count -ne 0) {
            $confirm = @($state.Backlog.ModalButtons | Where-Object {
                $_.Text -ne 'Cancelar' -and $_.Text -ne 'Cancel'
            })
            if ($confirm.Count -ne 1) { throw 'A confirmação de fechamento não ficou inequívoca.' }
            Assert-UndoWindow
            Click-Control $confirm[0].Rect $state.Scale
        }
        return Read-State { param($value)
            @($value.Backlog.Tabs | Where-Object ID -eq $ID).Count -eq 0 -and
            $value.Backlog.UndoVisible
        }
    }
    return Read-State { param($value) $value.Backlog.Selected -eq $ID }
}

function Invoke-NativeUndo([int]$Remaining) {
    $state = Read-State { param($value)
        $value.Backlog.UndoVisible -and $value.Backlog.UndoClose.Width -gt 0 -and
        $value.Backlog.UndoClose.Y -ge 0 -and
        $value.Backlog.UndoClose.Y + $value.Backlog.UndoClose.Height -le $value.CanvasHeight + 1
    }
    $deadline = Get-UndoDeadline $state
    Assert-UndoWindow
    Click-Control $state.Backlog.UndoClose $state.Scale
    $restored = Read-State { param($value)
        $value.Backlog.UndoCount -eq $Remaining -and
        $value.Backlog.UndoVisible -eq ($Remaining -gt 0)
    }
    if ($Remaining -gt 0 -and
        (Get-UndoDeadline $restored).UtcDateTime.Ticks -ne $deadline.UtcDateTime.Ticks) {
        throw 'Restaurar uma entrada renovou o prazo das restantes.'
    }
    return $restored
}

function Stop-UndoWindow {
    $state = Read-State
    Assert-UndoWindow
    $ownedID = $process.Id
    Click-Control $state.Close $state.Scale
    if (-not $process.WaitForExit(10000)) {
        throw 'O fechamento normal não concluiu a gravação do workspace.'
    }
    $process.WaitForExit()
    $process.Refresh()
    if ($null -eq $process.ExitCode -or $process.ExitCode -ne 0) {
        throw 'O processo isolado não confirmou encerramento normal.'
    }
    $process.Dispose()
    $script:process = $null
    $script:window = [IntPtr]::Zero
    return $ownedID
}

function Restart-UndoWindow([int]$PreviousID) {
    $script:undoLaunchNumber++
    # Preserve previous evidence, while excluding old frames and commands from
    # the new process. Workspace, preferences and fixture marker stay intact.
    foreach ($name in @('window-state.json', 'window-command.json')) {
        $source = Join-Path $checkDirectory $name
        if (Test-Path -LiteralPath $source) {
            $destination = Join-Path $checkDirectory "undo-run-$($script:undoLaunchNumber)-previous-$name"
            Move-Item -LiteralPath $source -Destination $destination
        }
    }
    $script:process = Start-Process -FilePath $executable -ArgumentList ('"' + $checkDirectory + '"') `
        -WorkingDirectory $aiRepository -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $checkDirectory "undo-run-$($script:undoLaunchNumber).stdout.log") `
        -RedirectStandardError (Join-Path $checkDirectory "undo-run-$($script:undoLaunchNumber).stderr.log")
    if ($process.Id -eq $PreviousID) { throw 'A reabertura não produziu um PID diferente.' }
    $null = Read-State -TimeoutSeconds 30
    $process.Refresh()
    $script:window = $process.MainWindowHandle
    Assert-UndoWindow
    [AITalkerPointer.Native]::ShowWindow($window, 5) | Out-Null
    [AITalkerPointer.Native]::SetForegroundWindow($window) | Out-Null
    $frameWait = [Diagnostics.Stopwatch]::StartNew()
    while ([AITalkerPointer.Native]::GetProp($window, 'AI-Talker.IntegratedFrame') -eq [IntPtr]::Zero) {
        Assert-UndoWindow
        if ($frameWait.Elapsed.TotalSeconds -gt 10) { throw 'A moldura da reabertura não ficou pronta.' }
        Start-Sleep -Milliseconds 100
    }
    [AITalkerPointer.Native]::SetWindowPos($window, [IntPtr](-1), 100, 80, 0, 0, 0x0011) | Out-Null
    $state = Backlog-Command @{resize=@{Width=1100; Height=950}}
    $script:undoProcessIDs += $process.Id
    return $state
}

function Reopen-UndoBeforeDeadline($Before, [string]$CaseName) {
    $deadline = Get-UndoDeadline $Before
    $beforeRemaining = ($deadline - [DateTimeOffset]::UtcNow).TotalSeconds
    Save-UndoEvidence "$CaseName-before-close.json" $Before
    $oldID = Stop-UndoWindow
    $saved = Get-UndoWorkspace
    if (@($saved.closed_tabs).Count -ne $Before.Backlog.UndoCount) {
        throw 'Fechar o app perdeu entradas recuperáveis.'
    }
    Start-Sleep -Milliseconds 1200
    $after = Restart-UndoWindow $oldID
    if (-not $after.Backlog.UndoVisible -or
        $after.Backlog.UndoCount -ne $Before.Backlog.UndoCount -or
        (Get-UndoDeadline $after).UtcDateTime.Ticks -ne $deadline.UtcDateTime.Ticks) {
        throw 'Reabrir perdeu ou renovou a sequência de Desfazer.'
    }
    $afterRemaining = ($deadline - [DateTimeOffset]::UtcNow).TotalSeconds
    if ($afterRemaining -le 0 -or $afterRemaining -ge $beforeRemaining - 1) {
        throw 'O tempo fechado não foi descontado do prazo restante.'
    }
    if ($after.Theme -ne 'green') { throw 'O harness resemeou as preferências na reabertura.' }
    Save-UndoEvidence "$CaseName-reopened.json" @{
        BeforePID=$oldID; AfterPID=$process.Id; DataDirectory=$checkDirectory
        DeadlineUTC=$deadline.ToString('O'); BeforeRemaining=$beforeRemaining
        AfterRemaining=$afterRemaining; State=$after
    }
    Capture-Canvas "$CaseName-reopened"
    return $after
}

function Assert-UndoTabText([uint64]$ID, [string]$Text, [string]$Processed) {
    $state = Click-UndoTab $ID
    if ($state.TranscriptText -ne $Text -or $state.ProcessedText -ne $Processed) {
        throw "A aba $ID perdeu conteúdo na restauração."
    }
    return $state
}

function Test-UndoHydrationControls {
    $script:undoLaunchNumber = 1
    $script:undoProcessIDs = @($process.Id)
    $state = Backlog-Command @{resize=@{Width=1100; Height=950}; theme='green';
        group_name='Grupo recuperável nativo'; new_tabs=1}
    $state = Read-State { param($value) $value.Backlog.Tabs.Count -eq 3 -and $value.Theme -eq 'green' }
    $firstID = $state.Backlog.Tabs[0].ID
    $firstGroup = $state.Backlog.Tabs[0].Group
    $secondID = @($state.Backlog.Tabs | Where-Object Group -ne $firstGroup)[0].ID
    $thirdID = @($state.Backlog.Tabs | Where-Object {
        $_.Group -eq $firstGroup -and $_.ID -ne $firstID
    })[0].ID
    $firstText = 'Primeira transcrição preservada na reabertura.'
    $thirdText = 'Segunda transcrição preservada na reabertura.'
    $state = Click-UndoTab $thirdID
    $state = Backlog-Command @{prompt=$thirdText}
    $state = Click-UndoTab $firstID
    $state = Backlog-Command @{prompt=$firstText}
    $baseline = Get-UndoWorkspace
    Save-UndoEvidence 'undo-baseline.json' $baseline

    # One tab, a normal application close, and a new PID in the same directory.
    $state = Click-UndoTab $thirdID -CloseTab
    $state = Reopen-UndoBeforeDeadline $state 'undo-tab'
    $state = Invoke-NativeUndo 0
    $state = Assert-UndoTabText $thirdID $thirdText ''
    Capture-Canvas 'undo-tab-restored'

    # Closing the last tab removes its group; this already has Undo support.
    $state = Click-UndoTab $thirdID -CloseTab
    $firstDeadline = Get-UndoDeadline $state
    Start-Sleep -Milliseconds 1200
    $state = Click-UndoTab $firstID -CloseTab
    if ((Get-UndoDeadline $state) -le $firstDeadline -or $state.Backlog.UndoCount -ne 2 -or
        @($state.Backlog.Groups | Where-Object ID -eq $firstGroup).Count -ne 0) {
        throw 'Fechar a última aba não removeu o grupo e renovou toda a sequência.'
    }
    $state = Reopen-UndoBeforeDeadline $state 'undo-last-group-tab'
    $state = Invoke-NativeUndo 1
    $restoredGroup = @($state.Backlog.Groups | Where-Object ID -eq $firstGroup)[0]
    if ($restoredGroup.Name -ne 'Grupo recuperável nativo' -or
        $state.Backlog.Groups[0].ID -ne $firstGroup) {
        throw 'O grupo removido perdeu nome ou posição ao reabrir e desfazer.'
    }
    $state = Assert-UndoTabText $firstID $firstText 'Texto processado sintético.'
    $state = Invoke-NativeUndo 0
    $state = Assert-UndoTabText $thirdID $thirdText ''
    Capture-Canvas 'undo-group-restored'

    # Juntar keeps its original sources separate from its live combined target.
    $state = Click-UndoTab $firstID
    $beforeJoin = Get-UndoWorkspace
    if ($state.Backlog.Join.Y + $state.Backlog.Join.Height -gt $state.CanvasHeight) {
        $state = Backlog-Command @{scroll_bottom=$true}
    }
    Assert-UndoWindow
    Click-Control $state.Backlog.Join $state.Scale
    $state = Read-State { param($value) $value.Backlog.UndoCount -eq 1 -and $value.Backlog.Tabs.Count -eq 2 }
    $combinedText = $state.TranscriptText
    if (-not $combinedText.Contains($firstText) -or -not $combinedText.Contains($thirdText)) {
        throw 'O clique nativo em Juntar não reuniu as duas origens.'
    }
    $state = Reopen-UndoBeforeDeadline $state 'undo-join'
    $state = Assert-UndoTabText $firstID $combinedText 'Texto processado sintético.'
    $state = Invoke-NativeUndo 0
    if ($state.Backlog.Tabs.Count -ne $beforeJoin.tabs.Count) { throw 'Desfazer Juntar perdeu ou duplicou abas.' }
    $state = Assert-UndoTabText $firstID $firstText 'Texto processado sintético.'
    $state = Assert-UndoTabText $thirdID $thirdText ''
    Capture-Canvas 'undo-join-restored'

    # Fechar grupo renews another group's Undo, without adding a group-wide Undo.
    $state = Click-UndoTab $secondID -CloseTab
    $priorDeadline = Get-UndoDeadline $state
    Start-Sleep -Milliseconds 1200
    $state = Backlog-Command @{open='close-group'}
    $state = Read-State { param($value) $value.Backlog.ModalButtons.Count -gt 0 }
    $confirm = @($state.Backlog.ModalButtons | Where-Object {
        $_.Text -ne 'Cancelar' -and $_.Text -ne 'Cancel'
    })
    if ($confirm.Count -ne 1) { throw 'A confirmação de Fechar grupo não ficou inequívoca.' }
    Assert-UndoWindow
    Click-Control $confirm[0].Rect $state.Scale
    $state = Read-State { param($value)
        @($value.Backlog.Tabs | Where-Object ID -eq $firstID).Count -eq 0 -and
        @($value.Backlog.Tabs | Where-Object ID -eq $thirdID).Count -eq 0
    }
    if ($state.Backlog.UndoCount -ne 1 -or (Get-UndoDeadline $state) -le $priorDeadline) {
        throw 'Fechar grupo não renovou a entrada alheia ou criou recuperação extra.'
    }
    $state = Reopen-UndoBeforeDeadline $state 'undo-group-renewal'
    $state = Invoke-NativeUndo 0
    if (@($state.Backlog.Tabs | Where-Object {
        $_.ID -eq $firstID -or $_.ID -eq $thirdID
    }).Count -ne 0) { throw 'Fechar grupo passou a restaurar suas próprias abas.' }
    Capture-Canvas 'undo-other-group-restored'

    # The persisted deadline expires while the app is closed; startup cannot reset it.
    $state = Click-UndoTab $secondID -CloseTab
    $expiry = Get-UndoDeadline $state
    $oldID = Stop-UndoWindow
    while ([DateTimeOffset]::UtcNow -le $expiry.AddSeconds(1)) {
        Start-Sleep -Milliseconds 500
    }
    $state = Restart-UndoWindow $oldID
    $state = Read-State { param($value) -not $value.Backlog.UndoVisible -and $value.Backlog.UndoCount -eq 0 }
    if (@($state.Backlog.Tabs | Where-Object ID -eq $secondID).Count -ne 0) {
        throw 'Reabrir restaurou automaticamente a aba cujo Desfazer expirou.'
    }
    Capture-Canvas 'undo-expired-after-reopen'
    Save-UndoEvidence 'undo-hydration-final.json' @{
        ProcessIDs=$script:undoProcessIDs; DataDirectory=$checkDirectory
        ExpiredDeadlineUTC=$expiry.ToString('O'); ExpiredReopenedAt=[DateTimeOffset]::UtcNow.ToString('O')
        State=$state; Tab=$true; LastTabGroup=$true; Join=$true; GroupRenewal=$true; Expiration=$true
    }
    $null = Stop-UndoWindow
    Write-Host 'Desfazer persistido: aba, última aba/grupo, Juntar, renovação por Fechar grupo e expiração real passaram.'
}
