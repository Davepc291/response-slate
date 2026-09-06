#requires -Version 5.1
[CmdletBinding()] param()
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'WhisperRuntime.psm1') -Force
try {
    $context = Get-GfrContext
    $config = Read-GfrConfiguration $context
    foreach ($path in @($context.Data, $context.Logs, $context.Temp)) {
        $null = Assert-GfrPath $path
        if (-not (Test-Path -LiteralPath $path -PathType Container)) { throw 'Runtime data directory missing.' }
    }
    $null = Assert-GfrPath $context.State
    Assert-GfrPortAvailable
    Add-Type -Path (Join-Path $PSScriptRoot 'RuntimeHost.cs')
    $command = (ConvertTo-GfrArgument $config.Executable) + ' ' + (Get-GfrWhisperArguments $config $context)
    $code = [Gfr.Whisper.RuntimeHost]::Run($config.Executable, $command, $config.FFmpeg, $context.Temp,
        $context.Logs, $context.State, $context.Sid, ([long]$config.LogMaxMB * 1MB), $config.LogArchives)
    exit $code
} catch {
    # Scheduler receives a failure code; never print arbitrary runtime exceptions.
    Write-Error 'GFR Whisper runtime failed validation or execution. Run Status-Whisper.ps1.' -ErrorAction Continue
    exit 1
}
