#requires -Version 5.1
[CmdletBinding()] param()
Import-Module (Join-Path $PSScriptRoot 'ApiRuntime.psm1') -Force
Get-GfrApiStatus
