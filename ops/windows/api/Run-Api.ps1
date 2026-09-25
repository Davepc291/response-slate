#requires -Version 5.1
[CmdletBinding()] param()
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'ApiRuntime.psm1') -Force
try {
    $context = Get-GfrApiContext
    $config = Read-GfrApiConfiguration $context
    foreach ($path in @($context.Data, $context.Logs)) {
        $null = Assert-GfrApiPath $path
        if (-not (Test-Path -LiteralPath $path -PathType Container)) { throw 'Runtime data directory missing.' }
    }
    if (-not (Test-Path -LiteralPath $context.EnvFile -PathType Leaf)) { throw 'Production environment file is missing.' }
    $null = Assert-GfrApiPath $context.State
    Assert-GfrApiPortAvailable

    # api.env holds KEY=VALUE lines only; comments (#) and blank lines are
    # skipped. GFR_HTTP_ADDR is always forced to the validated loopback
    # address below, regardless of what the file contains.
    Get-Content -LiteralPath $context.EnvFile | ForEach-Object {
        $line = $_.Trim()
        if (-not $line -or $line.StartsWith('#')) { return }
        $parts = $line.Split('=', 2)
        if ($parts.Count -ne 2 -or -not $parts[0]) { throw 'Malformed line in production environment file.' }
        Set-Item -Path ("Env:" + $parts[0]) -Value $parts[1]
    }
    $env:GFR_HTTP_ADDR = $config.HttpAddr

    # Rotate once per start rather than continuously mid-run (see
    # ApiRuntime.psm1 header comment for why that tradeoff is acceptable
    # here, unlike whisper's byte-exact continuous rotation).
    foreach ($name in @('stdout', 'stderr')) {
        $current = Join-Path $context.Logs "$name.log"
        if ((Test-Path -LiteralPath $current) -and (Get-Item -LiteralPath $current).Length -ge ([long]$config.LogMaxMB * 1MB)) {
            for ($i = $config.LogArchives; $i -ge 1; $i--) {
                $src = if ($i -eq 1) { $current } else { Join-Path $context.Logs "$name.log.$($i - 1)" }
                $dst = Join-Path $context.Logs "$name.log.$i"
                if (Test-Path -LiteralPath $src) {
                    if ($i -eq $config.LogArchives -and (Test-Path -LiteralPath $dst)) { Remove-Item -LiteralPath $dst -Force }
                    Move-Item -LiteralPath $src -Destination $dst -Force
                }
            }
        }
    }

    $stdout = Join-Path $context.Logs 'stdout.log'
    $stderr = Join-Path $context.Logs 'stderr.log'
    $process = Start-Process -FilePath $config.Executable -WorkingDirectory $context.Data -NoNewWindow -PassThru `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    # 9F-7K: no job-object supervision here (see ApiRuntime.psm1's header
    # comment for why that approach was tried and rejected). Stop-GfrApi
    # terminates this recorded PID directly instead, after re-verifying
    # its identity against what we write below.
    $state = [pscustomobject]@{ Pid = $process.Id; StartTicks = $process.StartTime.ToUniversalTime().Ticks }
    ($state | ConvertTo-Json) | Set-Content -LiteralPath $context.State -Encoding UTF8
    $process.WaitForExit()
    exit $process.ExitCode
} catch {
    Write-Error 'GFR API runtime failed validation or execution. Run Status-Api.ps1.' -ErrorAction Continue
    exit 1
}
