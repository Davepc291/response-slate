#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)]
param([string]$Executable = (Join-Path $env:USERPROFILE 'RadioTools\whisper-cpp\Release\whisper-server.exe'),
    [string]$Model = (Join-Path $env:USERPROFILE 'RadioTools\whisper-cpp\models\ggml-small.en.bin'),
    [string]$FFmpeg, [ValidateRange(1,64)][int]$LogMaxMB = 10, [ValidateRange(1,10)][int]$LogArchives = 3)
Import-Module (Join-Path $PSScriptRoot 'WhisperRuntime.psm1') -Force
Install-GfrWhisper @PSBoundParameters
