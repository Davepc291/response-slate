#requires -Version 5.1
[CmdletBinding()] param()
Import-Module (Join-Path $PSScriptRoot 'CaddyRuntime.psm1') -Force
Get-GfrCaddyStatus
