#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)] param([switch]$CleanupRuntimeData)
Import-Module (Join-Path $PSScriptRoot 'ApiRuntime.psm1') -Force
Uninstall-GfrApi @PSBoundParameters
