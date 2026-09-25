#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)] param()
Import-Module (Join-Path $PSScriptRoot 'ApiRuntime.psm1') -Force
Stop-GfrApi @PSBoundParameters
