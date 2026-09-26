#requires -Version 5.1
[CmdletBinding(SupportsShouldProcess)]
param([string]$Destination = (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'GreenwichFireResponder\Frontend'))
$ErrorActionPreference = 'Stop'
$repoWeb = Resolve-Path (Join-Path $PSScriptRoot '..\..\..\web')

# The exact path ops/windows/caddy/Run-Caddy.ps1 points Caddy's file_server
# at. Pruning below only ever runs when -Destination resolves to this
# literal path, so a caller-supplied override can never cause this script to
# clear out some other directory (see Assert-GfrFrontendPath/
# Clear-GfrFrontendDestination).
$expectedDestination = [IO.Path]::GetFullPath((Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'GreenwichFireResponder\Frontend'))

# Every filename a production `ng build` for this app emits at the top level,
# plus the hashed-bundle shape Angular's build gives its JS/CSS output
# (name-HASH.ext). Anything else found in the destination means it is not a
# clean frontend deploy root — Clear-GfrFrontendDestination refuses to
# delete rather than guess what is safe to remove.
$script:AllowedFrontendFiles = @('index.html', 'favicon.ico', 'manifest.webmanifest', 'ngsw.json',
    'ngsw-worker.js', 'safety-worker.js', 'worker-basic.min.js', 'first-response-logo.png')
$script:HashedBundlePattern = '^[a-z][a-z0-9]*-[A-Z0-9]{8}\.(js|css)$'

# Mirrors ops/windows/api/ApiRuntime.psm1's Assert-GfrApiPath (kept as its
# own copy rather than a shared import — each ops/windows/* folder owns its
# path-safety checks independently so they can be imported in the same
# session without colliding).
function Assert-GfrFrontendPath {
    param([Parameter(Mandatory)][string]$Path)
    if ($Path -notmatch '^[A-Za-z]:\\' -or $Path -match '[\x00-\x1f"`$;&|<>^%!]' -or
        $Path.Substring(2).Contains(':') -or $Path -match '(^|\\)\.{1,2}(\\|$)' -or
        $Path -match '[ .](\\|$)' -or $Path.Contains('/')) { throw 'Use a safe absolute local Windows path.' }
    $full = [IO.Path]::GetFullPath($Path)
    for ($current = $full; $current; $current = [IO.Path]::GetDirectoryName($current)) {
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -LiteralPath $current -Force
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Reparse points are not supported.' }
        }
        if ($current -eq [IO.Path]::GetPathRoot($current)) { break }
    }
    return $full
}

# Deletes only previously-deployed Angular build artifacts from $Path, and
# only after every existing top-level entry is confirmed to match a known
# artifact name/shape. Never touches Caddy, API, database, or any other
# files: if anything unexpected is present, this throws instead of deleting
# anything, so a misconfigured -Destination can never wipe the wrong
# directory.
function Clear-GfrFrontendDestination {
    [CmdletBinding(SupportsShouldProcess)]
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) { return }
    $entries = @(Get-ChildItem -LiteralPath $Path -Force)
    foreach ($item in $entries) {
        $isKnownFile = -not $item.PSIsContainer -and
            ($item.Name -in $script:AllowedFrontendFiles -or $item.Name -match $script:HashedBundlePattern)
        $isKnownDir = $item.PSIsContainer -and $item.Name -eq 'icons'
        if (-not $isKnownFile -and -not $isKnownDir) {
            throw "Refusing to clean '$Path': unexpected entry '$($item.Name)' is not a known frontend build artifact."
        }
    }
    if ($PSCmdlet.ShouldProcess($Path, 'Delete only previously-deployed Angular build artifacts')) {
        foreach ($item in $entries) { Remove-Item -LiteralPath $item.FullName -Recurse -Force }
    }
}

if ($PSCmdlet.ShouldProcess($Destination, 'npm ci, ng build (production), prune stale bundles, then copy dist output')) {
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
    $safeDestination = Assert-GfrFrontendPath $Destination
    if ($safeDestination -ieq $expectedDestination) {
        Clear-GfrFrontendDestination -Path $safeDestination
    } elseif (Test-Path -LiteralPath $safeDestination) {
        Write-Warning "Destination '$safeDestination' is not the expected GFR frontend directory ($expectedDestination); skipping stale-bundle pruning and only overlaying the new build."
    }
    New-Item -ItemType Directory -Force -Path $safeDestination | Out-Null
    Copy-Item -Path (Join-Path $builtDist '*') -Destination $safeDestination -Recurse -Force
    Write-Output "Frontend production build deployed to $safeDestination"
}
