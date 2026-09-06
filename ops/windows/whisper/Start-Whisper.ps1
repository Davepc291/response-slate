#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)] param()
Import-Module (Join-Path $PSScriptRoot 'WhisperRuntime.psm1') -Force
Start-GfrWhisper @PSBoundParameters
