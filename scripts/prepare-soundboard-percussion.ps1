# Original, deterministic percussion synthesis for the bundled rimshot.
param([string]$Output = (Join-Path $PSScriptRoot '..\internal\soundboard\assets\rimshot.wav'))
$rate = 48000
$count = [int]($rate * 1.8)
$samples = New-Object byte[] ($count * 2)
$noise = [System.Random]::new(120)
for ($index = 0; $index -lt $count; $index++) {
    $time = $index / [double]$rate
    $sample = 0.0
    if ($time -lt 0.23) {
        $sample += 0.72 * [Math]::Exp(-24*$time) * [Math]::Sin(2*[Math]::PI*(85*$time-100*$time*$time))
    }
    $snare = $time - 0.22
    if ($snare -ge 0 -and $snare -lt 0.24) {
        $sample += [Math]::Exp(-23*$snare) * (0.34*($noise.NextDouble()*2-1) + 0.13*[Math]::Sin(2*[Math]::PI*180*$snare))
    }
    $cymbal = $time - 0.44
    if ($cymbal -ge 0) {
        $metal = [Math]::Sin(2*[Math]::PI*7313*$cymbal)*[Math]::Sin(2*[Math]::PI*5039*$cymbal)
        $sample += 0.36 * [Math]::Exp(-5*$cymbal) * ($noise.NextDouble()*2-1+$metal) / 2
    }
    $value = [int16]([Math]::Round([Math]::Max(-1,[Math]::Min(1,$sample))*32767))
    $encoded = [BitConverter]::GetBytes($value)
    $samples[$index*2] = $encoded[0]
    $samples[$index*2+1] = $encoded[1]
}
$resolved = [IO.Path]::GetFullPath($Output)
$file = [IO.File]::Create($resolved)
$writer = [IO.BinaryWriter]::new($file)
try {
    $writer.Write([Text.Encoding]::ASCII.GetBytes('RIFF'))
    $writer.Write([uint32](36+$samples.Length))
    $writer.Write([Text.Encoding]::ASCII.GetBytes('WAVEfmt '))
    $writer.Write([uint32]16)
    $writer.Write([uint16]1)
    $writer.Write([uint16]1)
    $writer.Write([uint32]$rate)
    $writer.Write([uint32]($rate*2))
    $writer.Write([uint16]2)
    $writer.Write([uint16]16)
    $writer.Write([Text.Encoding]::ASCII.GetBytes('data'))
    $writer.Write([uint32]$samples.Length)
    $writer.Write($samples)
} finally {
    $writer.Dispose()
}
