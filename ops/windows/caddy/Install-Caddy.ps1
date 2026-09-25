#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)]
param([string]$CaddyExecutable, [string]$CaddyfilePath, [string]$FrontendRoot,
    [string]$Domain = 'app.gfrapp.com', [string]$ApiUpstream = '127.0.0.1:8080')
Import-Module (Join-Path $PSScriptRoot 'CaddyRuntime.psm1') -Force
Install-GfrCaddy @PSBoundParameters
