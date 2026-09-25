#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)]
param([string]$Output = (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'GreenwichFireResponder\ApiBin\gfr-api.exe'))
$ErrorActionPreference = 'Stop'
$repoBackend = Resolve-Path (Join-Path $PSScriptRoot '..\..\..\backend')
if ($PSCmdlet.ShouldProcess($Output, 'go build the production API binary from ./cmd/api')) {
    $outDir = Split-Path -Parent $Output
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
    Push-Location $repoBackend
    try {
        & go build -o $Output ./cmd/api
        if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }
    } finally { Pop-Location }
    Write-Output "Built $Output"
}
