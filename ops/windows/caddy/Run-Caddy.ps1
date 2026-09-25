#requires -Version 5.1
[CmdletBinding()] param()
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'CaddyRuntime.psm1') -Force
try {
    $context = Get-GfrCaddyContext
    $config = Read-GfrCaddyConfiguration $context
    foreach ($path in @($context.Data, $context.Logs, $context.CaddyDataDir, $context.CaddyConfigDir)) {
        $null = Assert-GfrCaddyPath $path
        if (-not (Test-Path -LiteralPath $path -PathType Container)) { throw 'Runtime data directory missing.' }
    }
    $null = Assert-GfrCaddyPath $context.State
    Assert-GfrCaddyPortsAvailable

    # Keep Caddy's own storage (ACME account/certs) and admin config cache
    # fully inside the managed data directory, and point it at the reviewed
    # Caddyfile and the deployed frontend build.
    $env:XDG_DATA_HOME = $context.CaddyDataDir
    $env:XDG_CONFIG_HOME = $context.CaddyConfigDir
    $env:GFR_FRONTEND_ROOT = $config.FrontendRoot

    $stdout = Join-Path $context.Logs 'stdout.log'
    $stderr = Join-Path $context.Logs 'stderr.log'
    foreach ($name in @('stdout', 'stderr')) {
        $current = Join-Path $context.Logs "$name.log"
        if ((Test-Path -LiteralPath $current) -and (Get-Item -LiteralPath $current).Length -ge 10MB) {
            for ($i = 3; $i -ge 1; $i--) {
                $src = if ($i -eq 1) { $current } else { Join-Path $context.Logs "$name.log.$($i - 1)" }
                $dst = Join-Path $context.Logs "$name.log.$i"
                if (Test-Path -LiteralPath $src) {
                    if ($i -eq 3 -and (Test-Path -LiteralPath $dst)) { Remove-Item -LiteralPath $dst -Force }
                    Move-Item -LiteralPath $src -Destination $dst -Force
                }
            }
        }
    }

    $process = Start-Process -FilePath $config.CaddyExecutable `
        -ArgumentList @('run', '--config', $config.CaddyfilePath, '--adapter', 'caddyfile') `
        -WorkingDirectory $context.Data -NoNewWindow -PassThru `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    $state = [pscustomobject]@{ Pid = $process.Id; StartTicks = $process.StartTime.ToUniversalTime().Ticks }
    ($state | ConvertTo-Json) | Set-Content -LiteralPath $context.State -Encoding UTF8
    $process.WaitForExit()
    exit $process.ExitCode
} catch {
    Write-Error 'GFR Caddy runtime failed validation or execution. Run Status-Caddy.ps1.' -ErrorAction Continue
    exit 1
}
