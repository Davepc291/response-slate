#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)]
param([string]$Destination = (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'GreenwichFireResponder\Frontend'))
$ErrorActionPreference = 'Stop'
$repoWeb = Resolve-Path (Join-Path $PSScriptRoot '..\..\..\web')
if ($PSCmdlet.ShouldProcess($Destination, 'npm ci, ng build (production), then copy dist output')) {
    Push-Location $repoWeb
    try {
        & npm ci
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed with exit code $LASTEXITCODE" }
        & npm run build
        if ($LASTEXITCODE -ne 0) { throw "ng build failed with exit code $LASTEXITCODE" }
    } finally { Pop-Location }
    $builtDist = Join-Path $repoWeb 'dist\web\browser'
    if (-not (Test-Path -LiteralPath $builtDist -PathType Container)) { throw "Expected build output missing: $builtDist" }
    if (-not (Test-Path -LiteralPath (Join-Path $builtDist 'index.html') -PathType Leaf)) { throw 'Build output is missing index.html.' }
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    Copy-Item -Path (Join-Path $builtDist '*') -Destination $Destination -Recurse -Force
    Write-Output "Frontend production build deployed to $Destination"
}
