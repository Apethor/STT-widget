[CmdletBinding()]
param([switch]$SkipBuild, [string]$Scale = '1')

$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'smoke-window.ps1') -UndoHydrationOnly -SkipBuild:$SkipBuild -Scale $Scale
