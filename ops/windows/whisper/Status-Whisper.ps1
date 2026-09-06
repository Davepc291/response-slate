#requires -Version 5.1
[CmdletBinding()] param()
Import-Module (Join-Path $PSScriptRoot 'WhisperRuntime.psm1') -Force
Get-GfrWhisperStatus
