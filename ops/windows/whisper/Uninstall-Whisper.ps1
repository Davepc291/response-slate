#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)] param([switch]$CleanupRuntimeData)
Import-Module (Join-Path $PSScriptRoot 'WhisperRuntime.psm1') -Force
Uninstall-GfrWhisper @PSBoundParameters
