# Original tone synthesis. Generated WAVs are dedicated to the public domain
# under CC0 1.0; no game recording, melody or downloaded sample is used.
[CmdletBinding()]
param([string]$OutputDirectory = (Join-Path $PSScriptRoot '..\internal\soundboard\assets'))

$ErrorActionPreference = 'Stop'
$toneRate = 48000
$toneDirectory = [IO.Path]::GetFullPath($OutputDirectory)
[IO.Directory]::CreateDirectory($toneDirectory) | Out-Null

function Write-ToneWav([string]$Name, [double[]]$Samples) {
    $peak = 0.0
    foreach ($sample in $Samples) { $peak = [Math]::Max($peak, [Math]::Abs($sample)) }
    if ($peak -le 0 -or [double]::IsNaN($peak) -or [double]::IsInfinity($peak)) {
        throw "O efeito $Name não contém um sinal válido."
    }
    $pcm = [byte[]]::new($Samples.Length * 2)
    for ($sampleIndex = 0; $sampleIndex -lt $Samples.Length; $sampleIndex++) {
        $value = [int16][Math]::Round(0.75 * 32767 * $Samples[$sampleIndex] / $peak)
        $encoded = [BitConverter]::GetBytes($value)
        $pcm[$sampleIndex * 2] = $encoded[0]
        $pcm[$sampleIndex * 2 + 1] = $encoded[1]
    }
    $target = Join-Path $toneDirectory $Name
    $stream = [IO.File]::Create($target)
    $writer = [IO.BinaryWriter]::new($stream)
    try {
        $writer.Write([Text.Encoding]::ASCII.GetBytes('RIFF'))
        $writer.Write([uint32](36 + $pcm.Length))
        $writer.Write([Text.Encoding]::ASCII.GetBytes('WAVEfmt '))
        $writer.Write([uint32]16)
        $writer.Write([uint16]1)
        $writer.Write([uint16]1)
        $writer.Write([uint32]$toneRate)
        $writer.Write([uint32]($toneRate * 2))
        $writer.Write([uint16]2)
        $writer.Write([uint16]16)
        $writer.Write([Text.Encoding]::ASCII.GetBytes('data'))
        $writer.Write([uint32]$pcm.Length)
        $writer.Write($pcm)
    } finally {
        $writer.Dispose()
    }
    Get-FileHash -LiteralPath $target -Algorithm SHA256
}

# A smooth rising sweep, with a fast attack and a soft release.
$sweepDuration = 0.68
$sweepSamples = [double[]]::new([int]($toneRate * $sweepDuration))
$sweepRatio = 900.0 / 180.0
for ($toneIndex = 0; $toneIndex -lt $sweepSamples.Length; $toneIndex++) {
    $time = $toneIndex / [double]$toneRate
    $phase = 2 * [Math]::PI * 180 * $sweepDuration / [Math]::Log($sweepRatio) *
        ([Math]::Pow($sweepRatio, $time / $sweepDuration) - 1)
    $envelope = (1 - [Math]::Exp(-$time / 0.008)) * [Math]::Exp(-5 * $time) *
        [Math]::Min(1, ($sweepDuration - $time) / 0.04)
    $sweepSamples[$toneIndex] = $envelope *
        ([Math]::Sin($phase) + 0.25 * [Math]::Sin(2 * $phase) + 0.1 * [Math]::Sin(3 * $phase))
}
Write-ToneWav 'impulse.wav' $sweepSamples

# Three overlapping bell strikes with inharmonic overtones.
$bellDuration = 1.25
$bellSamples = [double[]]::new([int]($toneRate * $bellDuration))
$bellFrequencies = @(523.25, 782.4, 1171.7)
$bellStarts = @(0.0, 0.13, 0.28)
for ($toneIndex = 0; $toneIndex -lt $bellSamples.Length; $toneIndex++) {
    $time = $toneIndex / [double]$toneRate
    $sample = 0.0
    for ($bellIndex = 0; $bellIndex -lt $bellStarts.Count; $bellIndex++) {
        $elapsed = $time - $bellStarts[$bellIndex]
        if ($elapsed -lt 0) { continue }
        $phase = 2 * [Math]::PI * $bellFrequencies[$bellIndex] * $elapsed
        $envelope = (1 - [Math]::Exp(-$elapsed / 0.006)) * [Math]::Exp(-5.5 * $elapsed)
        $sample += $envelope *
            ([Math]::Sin($phase) + 0.23 * [Math]::Sin(2.73 * $phase) + 0.1 * [Math]::Sin(5.81 * $phase))
    }
    $bellSamples[$toneIndex] = $sample * [Math]::Min(1, ($bellDuration - $time) / 0.06)
}
Write-ToneWav 'sparkle.wav' $bellSamples
