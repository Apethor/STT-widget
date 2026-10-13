[CmdletBinding()]
param(
    [switch]$Integration,
    [ValidateRange(1, 2)]
    [int]$DesktopWorkers = 1
)

$ErrorActionPreference = 'Stop'

function Get-TestBatchPattern {
    param([string[]]$Names, [string[]]$AllNames)

    $unselected = @($AllNames | Where-Object { $Names -cnotcontains $_ })
    $patterns = [System.Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach ($name in $Names) {
        $pattern = [regex]::Escape($name)
        for ($length = 1; $length -le $name.Length; $length++) {
            $prefix = $name.Substring(0, $length)
            $conflict = $false
            foreach ($other in $unselected) {
                if ($other.StartsWith($prefix, [StringComparison]::Ordinal)) {
                    $conflict = $true
                    break
                }
            }
            if (-not $conflict) {
                $pattern = [regex]::Escape($prefix) + '.*'
                break
            }
        }
        $patterns.Add($pattern) | Out-Null
    }
    return '^(' + (($patterns | Sort-Object -CaseSensitive) -join '|') + ')$'
}

function Start-DesktopTestStage {
    param(
        [object]$Run,
        [string]$Executable,
        [string]$WorkingDirectory
    )

    if ($Run.Stage -eq 'functional') {
        $arguments = $Run.Batch.FunctionalArguments
        $Run.Stdout = $Run.Batch.FunctionalOutput
        $Run.Stderr = $Run.Batch.FunctionalError
    } else {
        $arguments = $Run.Batch.RaceArguments
        $Run.Stdout = $Run.Batch.RaceOutput
        $Run.Stderr = $Run.Batch.RaceError
    }
    $Run.Process = Start-Process -FilePath $Executable -ArgumentList $arguments `
        -WorkingDirectory $WorkingDirectory -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput $Run.Stdout -RedirectStandardError $Run.Stderr
    Write-Host "Desktop: lote $($Run.Batch.Name), $($Run.Stage), PID $($Run.Process.Id)."
}

function Complete-DesktopTestStage {
    param([object]$Run, [string]$LogsDirectory)

    try {
        # HasExited is checked by the dispatcher. The final wait also drains
        # redirected streams before reading their evidence and ExitCode.
        $Run.Process.WaitForExit()
        $Run.Process.Refresh()
        $exitCode = $Run.Process.ExitCode
        Get-Content -LiteralPath $Run.Stdout -Encoding UTF8
        Get-Content -LiteralPath $Run.Stderr -Encoding UTF8
        if ($null -eq $exitCode -or $exitCode -ne 0) {
            throw "O lote desktop $($Run.Batch.Name) falhou em $($Run.Stage) (exit $exitCode). Evidências: $LogsDirectory"
        }
    } finally {
        $Run.Process.Dispose()
        $Run.Process = $null
    }
}

function Stop-OwnedDesktopTestProcess {
    param([Diagnostics.Process]$Process)

    if ($Process.HasExited) {
        $Process.WaitForExit()
        return
    }
    # Only Process instances returned by our Start-Process reach this helper.
    # Kill the owned Go process and its compiler/test descendants together.
    if ($null -ne $Process.GetType().GetMethod('Kill', [type[]]@([bool]))) {
        $Process.Kill($true)
    } else {
        # Windows PowerShell 5.1 has no Kill(entireProcessTree) overload.
        # The owned process handle remains open while its exact PID is used.
        & (Join-Path $env:SystemRoot 'System32\taskkill.exe') /PID $Process.Id /T /F | Out-Null
        if ($LASTEXITCODE -ne 0 -and -not $Process.HasExited) {
            throw "Não foi possível encerrar o processo desktop próprio PID $($Process.Id)."
        }
    }
    if (-not $Process.WaitForExit(10000)) {
        throw "O processo desktop próprio PID $($Process.Id) não encerrou."
    }
}

function Invoke-DesktopTestBatches {
    param(
        [object[]]$Batches,
        [ValidateRange(1, 2)]
        [int]$Workers,
        [string]$Executable,
        [string]$WorkingDirectory,
        [string]$LogsDirectory
    )

    $active = [Collections.Generic.List[object]]::new()
    $next = 0
    try {
        while ($next -lt $Batches.Count -or $active.Count -gt 0) {
            while ($next -lt $Batches.Count -and $active.Count -lt $Workers) {
                $run = [pscustomobject]@{
                    Batch = $Batches[$next]
                    Stage = 'functional'
                    Process = $null
                    Stdout = $null
                    Stderr = $null
                }
                $active.Add($run)
                $next++
                Start-DesktopTestStage -Run $run -Executable $Executable -WorkingDirectory $WorkingDirectory
            }
            foreach ($run in @($active.ToArray())) {
                if (-not $run.Process.HasExited) {
                    continue
                }
                Complete-DesktopTestStage -Run $run -LogsDirectory $LogsDirectory
                if ($run.Stage -eq 'functional') {
                    $run.Stage = 'race'
                    Start-DesktopTestStage -Run $run -Executable $Executable -WorkingDirectory $WorkingDirectory
                } else {
                    $active.Remove($run) | Out-Null
                }
            }
            if ($active.Count -gt 0) {
                Start-Sleep -Milliseconds 100
            }
        }
    } finally {
        foreach ($run in @($active.ToArray())) {
            if ($null -eq $run.Process) {
                continue
            }
            try {
                Stop-OwnedDesktopTestProcess -Process $run.Process
            } catch {
                Write-Warning "Falha ao encerrar filho próprio: $($_.Exception.Message)"
            } finally {
                $run.Process.Dispose()
                $run.Process = $null
            }
        }
    }
}

. (Join-Path $PSScriptRoot 'go-env.ps1')
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $unformatted = & gofmt -l cmd internal
    if ($LASTEXITCODE -ne 0) { throw 'A verificação de formatação falhou.' }
    if ($unformatted) { throw ('Execute gofmt nos arquivos: ' + ($unformatted -join ', ')) }
    # Fyne's ci tag selects the headless driver; the native driver is built
    # separately by build-go.ps1 and checked by the desktop smoke test.
    & go vet -mod=readonly -tags=ci -p 4 ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet encontrou problemas.' }
    $aiDesktopPackage = 'ai-talker/internal/desktop'
    $aiPackages = @(& go list -mod=readonly -tags=ci ./...)
    if ($LASTEXITCODE -ne 0) { throw 'Não foi possível listar os pacotes.' }
    $aiOtherPackages = @($aiPackages | Where-Object { $_ -ne $aiDesktopPackage })
    & go test -mod=readonly -tags=ci -p 4 -timeout 10m $aiOtherPackages
    if ($LASTEXITCODE -ne 0) { throw 'A suíte Go falhou.' }
    & go test -mod=readonly -tags=ci -p 4 -race -timeout 10m $aiOtherPackages
    if ($LASTEXITCODE -ne 0) { throw 'O detector de corridas encontrou problemas.' }

    # Hundreds of Fyne windows also exhausted the functional process's
    # budget in 0.11.0. Both modes use every test in verified bounded batches;
    # each process releases its renderer caches before the next batch.
    $aiDesktopTests = @(& go test -mod=readonly -tags=ci -list '^Test' ./internal/desktop)
    if ($LASTEXITCODE -ne 0) { throw 'Não foi possível listar os testes desktop.' }
    $aiDesktopTests = @($aiDesktopTests | Where-Object { $_ -match '^Test\S+$' })
    if (-not $aiDesktopTests.Count) { throw 'Nenhum teste desktop foi encontrado.' }
    $aiGoExecutable = (Get-Command go -CommandType Application -ErrorAction Stop |
        Select-Object -First 1).Source
    $aiRaceLogs = Join-Path (Get-Location).Path ('artifacts\validation\go-race-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $aiRaceLogs -Force | Out-Null
    $aiDesktopTests | Set-Content -LiteralPath (Join-Path $aiRaceLogs 'tests.txt') -Encoding UTF8
    $aiDesktopBatches = [Collections.Generic.List[object]]::new()
    for ($aiTestOffset = 0; $aiTestOffset -lt $aiDesktopTests.Count; $aiTestOffset += 20) {
        $aiTestEnd = [Math]::Min($aiTestOffset + 19, $aiDesktopTests.Count - 1)
        $aiTestBatch = @($aiDesktopTests[$aiTestOffset..$aiTestEnd])
        # Use short prefixes only when they include no test outside this
        # batch. Verify the expression against the complete test inventory.
        $aiTestPattern = Get-TestBatchPattern -Names $aiTestBatch -AllNames $aiDesktopTests
        $aiMatchedTests = @($aiDesktopTests | Where-Object { $_ -cmatch $aiTestPattern })
        if (($aiMatchedTests -join "`n") -cne ($aiTestBatch -join "`n")) {
            throw 'A expressão do lote não corresponde aos testes selecionados.'
        }
        Write-Host "Desktop: testes $($aiTestOffset + 1) a $($aiTestEnd + 1) de $($aiDesktopTests.Count)."
        # Some PowerShell hosts fail to start a long regex command after
        # previous native calls (StandardOutputEncoding without redirection).
        # Explicit native stream files also retain evidence for every batch.
        $aiBatchName = '{0:D3}-{1:D3}' -f ($aiTestOffset + 1), ($aiTestEnd + 1)
        $aiBatchOutput = Join-Path $aiRaceLogs ($aiBatchName + '.stdout.log')
        $aiBatchError = Join-Path $aiRaceLogs ($aiBatchName + '.stderr.log')
        $aiTestPattern | Set-Content -LiteralPath (Join-Path $aiRaceLogs ($aiBatchName + '.pattern.txt')) -Encoding UTF8
        $aiFunctionalOutput = Join-Path $aiRaceLogs ($aiBatchName + '.functional.stdout.log')
        $aiFunctionalError = Join-Path $aiRaceLogs ($aiBatchName + '.functional.stderr.log')
        $aiFunctionalArguments = @(
            'test', '-mod=readonly', '-tags=ci', '-p', '4',
            '-timeout', '5m', '-run', ('"' + $aiTestPattern + '"'), './internal/desktop'
        )
        $aiRaceArguments = @(
            'test', '-mod=readonly', '-tags=ci', '-p', '4', '-race',
            '-timeout', '10m', '-run', ('"' + $aiTestPattern + '"'), './internal/desktop'
        )
        $aiDesktopBatches.Add([pscustomobject]@{
            Name = $aiBatchName
            FunctionalArguments = $aiFunctionalArguments
            RaceArguments = $aiRaceArguments
            FunctionalOutput = $aiFunctionalOutput
            FunctionalError = $aiFunctionalError
            RaceOutput = $aiBatchOutput
            RaceError = $aiBatchError
        })
    }
    Write-Host "Desktop: $($aiDesktopBatches.Count) lotes, $DesktopWorkers processo(s) simultâneo(s). Evidências: $aiRaceLogs"
    Invoke-DesktopTestBatches -Batches $aiDesktopBatches.ToArray() -Workers $DesktopWorkers `
        -Executable $aiGoExecutable -WorkingDirectory (Get-Location).Path -LogsDirectory $aiRaceLogs
    if ($Integration) {
        & go test -mod=readonly -p 4 -tags=integration -count=1 -timeout 2m ./internal/audio ./internal/transcript ./internal/playback ./internal/pcmoutput ./internal/secrets ./internal/video
        if ($LASTEXITCODE -ne 0) { throw 'A integração local de codecs falhou.' }
    }
} finally {
    Pop-Location
}
