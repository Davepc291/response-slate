#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)] param()
Import-Module (Join-Path $PSScriptRoot 'CaddyRuntime.psm1') -Force
Start-GfrCaddy @PSBoundParameters
