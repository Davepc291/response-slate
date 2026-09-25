#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)]
param([string]$Executable, [string]$HttpAddr = '127.0.0.1:8080',
    [ValidateRange(1,64)][int]$LogMaxMB = 10, [ValidateRange(1,10)][int]$LogArchives = 3)
Import-Module (Join-Path $PSScriptRoot 'ApiRuntime.psm1') -Force
Install-GfrApi @PSBoundParameters
