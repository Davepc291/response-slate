#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)] param([switch]$CleanupRuntimeData)
Import-Module (Join-Path $PSScriptRoot 'CaddyRuntime.psm1') -Force
Uninstall-GfrCaddy @PSBoundParameters
