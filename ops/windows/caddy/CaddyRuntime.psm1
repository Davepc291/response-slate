#requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Step 9F-7D production Caddy runtime. Mirrors the ops/windows/whisper
# Task Scheduler conventions; see ApiRuntime.psm1's header comment for why
# this does not reuse whisper's C# job-object host (Caddy does not spawn
# child processes either). Caddy itself is NOT installed by any script
# here: Install-GfrCaddy only registers a task once a real caddy.exe is
# already present and passes validation, so running these scripts before
# Caddy is installed fails closed rather than fetching or exposing anything.
$script:RequiredDomain = 'app.gfrapp.com'
$script:RequiredApiUpstream = '127.0.0.1:8080'

function Assert-GfrCaddyPath {
    param([Parameter(Mandatory)][string]$Path, [switch]$ExistingFile, [switch]$ExistingDirectory)
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
    if ($ExistingFile -and -not (Test-Path -LiteralPath $full -PathType Leaf)) { throw 'Required file is missing.' }
    if ($ExistingDirectory -and -not (Test-Path -LiteralPath $full -PathType Container)) { throw 'Required directory is missing.' }
    return $full
}

function ConvertTo-GfrCaddyArgument {
    param([AllowEmptyString()][string]$Value)
    if ($Value -match '[\x00-\x1f]') { throw 'Control characters are not allowed in arguments.' }
    $escaped = [regex]::Replace($Value, '(\\*)"', '$1$1\"')
    $escaped = [regex]::Replace($escaped, '(\\+)$', '$1$1')
    return '"' + $escaped + '"'
}

function Assert-GfrCaddyExecutable {
    param([string]$Path)
    $stream = [IO.File]::OpenRead($Path)
    try {
        if ($stream.Length -lt 64 -or $stream.ReadByte() -ne 0x4d -or $stream.ReadByte() -ne 0x5a) { throw 'Expected a Windows executable image.' }
        $reader = [IO.BinaryReader]::new($stream)
        $stream.Position = 60; $offset = $reader.ReadInt32()
        if ($offset -lt 64 -or $offset -gt $stream.Length - 4) { throw 'Invalid Windows executable header.' }
        $stream.Position = $offset
        if ($reader.ReadUInt32() -ne 0x00004550) { throw 'Invalid Windows executable signature.' }
    }
    finally { $stream.Dispose() }
}

function Get-GfrCaddyContext {
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $data = Assert-GfrCaddyPath (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'GreenwichFireResponder\Caddy')
    $wrapper = Assert-GfrCaddyPath (Join-Path $PSScriptRoot 'Run-Caddy.ps1') -ExistingFile
    $shell = Assert-GfrCaddyPath (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') -ExistingFile
    [pscustomobject]@{
        Sid = $sid; TaskName = "GFR-Caddy-$sid"; TaskPath = '\'
        Description = "Greenwich Fire Responder production reverse proxy; owner $sid"
        Data = $data; Config = Join-Path $data 'runtime.json'; State = Join-Path $data 'process.json'
        Logs = Join-Path $data 'logs'; CaddyDataDir = Join-Path $data 'caddy-data'; CaddyConfigDir = Join-Path $data 'caddy-config'
        Wrapper = $wrapper; Shell = $shell
    }
}

function Get-GfrCaddyTaskArguments {
    param($Context)
    return (@('-NoLogo', '-NoProfile', '-NonInteractive', '-WindowStyle', 'Hidden', '-ExecutionPolicy', 'RemoteSigned',
        '-File', $Context.Wrapper) | ForEach-Object { ConvertTo-GfrCaddyArgument $_ }) -join ' '
}

function New-GfrCaddyConfiguration {
    param($Context, [string]$CaddyExecutable, [string]$CaddyfilePath, [string]$FrontendRoot,
        [string]$Domain = $script:RequiredDomain, [string]$ApiUpstream = $script:RequiredApiUpstream)
    $CaddyExecutable = Assert-GfrCaddyPath $CaddyExecutable -ExistingFile
    $CaddyfilePath = Assert-GfrCaddyPath $CaddyfilePath -ExistingFile
    $FrontendRoot = Assert-GfrCaddyPath $FrontendRoot -ExistingDirectory
    if ([IO.Path]::GetFileName($CaddyExecutable) -ine 'caddy.exe') { throw 'Expected the executable to be named caddy.exe.' }
    foreach ($path in @($CaddyExecutable, $CaddyfilePath)) {
        if ($path.StartsWith($Context.Data + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Runtime inputs must be outside the managed data directory.' }
    }
    if (-not (Test-Path -LiteralPath (Join-Path $FrontendRoot 'index.html') -PathType Leaf)) { throw 'FrontendRoot does not contain a built index.html; run Build-Frontend.ps1 first.' }
    if ($Domain -ne $script:RequiredDomain) { throw "The production site must be $script:RequiredDomain." }
    if ($ApiUpstream -ne $script:RequiredApiUpstream) { throw "The API upstream must be exactly $script:RequiredApiUpstream (loopback only)." }
    Assert-GfrCaddyExecutable $CaddyExecutable
    [pscustomobject]@{ Version = 1; OwnerSid = $Context.Sid; CaddyExecutable = $CaddyExecutable; CaddyfilePath = $CaddyfilePath
        FrontendRoot = $FrontendRoot; Domain = $Domain; ApiUpstream = $ApiUpstream }
}

function Read-GfrCaddyConfiguration {
    param($Context)
    $null = Assert-GfrCaddyPath $Context.Config -ExistingFile
    $config = Get-Content -LiteralPath $Context.Config -Raw | ConvertFrom-Json
    if ($config.Version -ne 1 -or $config.OwnerSid -ne $Context.Sid) { throw 'Runtime configuration ownership mismatch.' }
    return New-GfrCaddyConfiguration $Context $config.CaddyExecutable $config.CaddyfilePath $config.FrontendRoot $config.Domain $config.ApiUpstream
}

function Assert-GfrCaddyPortsAvailable {
    foreach ($port in @(80, 443)) {
        $socket = [Net.Sockets.Socket]::new([Net.Sockets.AddressFamily]::InterNetwork, [Net.Sockets.SocketType]::Stream, [Net.Sockets.ProtocolType]::Tcp)
        try { $socket.ExclusiveAddressUse = $true; $socket.Bind([Net.IPEndPoint]::new([Net.IPAddress]::Any, $port)) }
        catch { throw "Port $port is occupied; stop its owner manually. No process was stopped." }
        finally { $socket.Dispose() }
    }
}

function ConvertTo-GfrCaddyAccountSid {
    param([string]$Account)
    return ([Security.Principal.NTAccount]::new($Account)).Translate([Security.Principal.SecurityIdentifier]).Value
}

function Resolve-GfrCaddyPrincipalSid {
    param([string]$UserId)
    try {
        if ([string]::IsNullOrWhiteSpace($UserId)) { throw 'Missing principal.' }
        if ($UserId -match '^S-1-') { return ([Security.Principal.SecurityIdentifier]::new($UserId)).Value }
        if ($UserId -notmatch '[\\@]') { $UserId = [Environment]::MachineName + '\' + $UserId }
        elseif ($UserId.StartsWith('.\')) { $UserId = [Environment]::MachineName + $UserId.Substring(1) }
        return ConvertTo-GfrCaddyAccountSid $UserId
    }
    catch { throw 'Task principal could not be resolved; refusing to manage it.' }
}

function Get-GfrCaddyOwnedTask {
    param($Context)
    $task = Get-ScheduledTask -TaskPath $Context.TaskPath -ErrorAction Stop | Where-Object { $_.TaskName -ceq $Context.TaskName }
    if (-not $task) { return $null }
    if (@($task).Count -ne 1 -or @($task.Actions).Count -ne 1 -or $task.Description -cne $Context.Description -or
        $task.TaskPath -cne $Context.TaskPath -or
        (Resolve-GfrCaddyPrincipalSid $task.Principal.UserId) -ne $Context.Sid -or
        $task.Principal.LogonType -ne 'Interactive' -or $task.Principal.RunLevel -ne 'Limited' -or
        $task.Actions[0].Execute -ine $Context.Shell -or
        $task.Actions[0].WorkingDirectory -ine $Context.Data -or
        $task.Actions[0].Arguments -cne (Get-GfrCaddyTaskArguments $Context)) { throw 'Task ownership mismatch; refusing to manage it.' }
    return $task
}

function Assert-GfrCaddyRestartPolicy {
    param($Settings)
    $interval = [Xml.XmlConvert]::ToTimeSpan($Settings.RestartInterval)
    if ($interval -lt [TimeSpan]::FromMinutes(1) -or $Settings.RestartCount -ne 3) {
        throw 'GFR requires exactly three restart attempts with an interval of at least one minute.'
    }
}

function New-GfrCaddyTaskDefinition {
    param($Context)
    $action = New-ScheduledTaskAction -Execute $Context.Shell -Argument (Get-GfrCaddyTaskArguments $Context) -WorkingDirectory $Context.Data
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $Context.Sid
    $principal = New-ScheduledTaskPrincipal -UserId $Context.Sid -LogonType Interactive -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -StartWhenAvailable -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    Assert-GfrCaddyRestartPolicy $settings
    return New-ScheduledTask -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description $Context.Description
}

function Install-GfrCaddy {
    [CmdletBinding(SupportsShouldProcess)]
    param([string]$CaddyExecutable, [string]$CaddyfilePath, [string]$FrontendRoot,
        [string]$Domain = $script:RequiredDomain, [string]$ApiUpstream = $script:RequiredApiUpstream)
    $context = Get-GfrCaddyContext
    if (-not $CaddyExecutable) { $CaddyExecutable = Join-Path $env:ProgramFiles 'Caddy\caddy.exe' }
    if (-not $CaddyfilePath) { $CaddyfilePath = Join-Path $PSScriptRoot 'Caddyfile.production' }
    if (-not $FrontendRoot) { $FrontendRoot = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'GreenwichFireResponder\Frontend' }
    if (Get-GfrCaddyOwnedTask $context) { throw 'The exact GFR Caddy task already exists; uninstall it before registering again.' }
    $config = New-GfrCaddyConfiguration $context $CaddyExecutable $CaddyfilePath $FrontendRoot $Domain $ApiUpstream
    $null = Assert-GfrCaddyPath $context.Config
    Assert-GfrCaddyPortsAvailable
    $definition = New-GfrCaddyTaskDefinition $context
    if ($PSCmdlet.ShouldProcess($context.TaskName, 'Create user-local runtime configuration and register logon task (do not start)')) {
        foreach ($path in @($context.Data, $context.Logs, $context.CaddyDataDir, $context.CaddyConfigDir)) { $null = Assert-GfrCaddyPath $path; [IO.Directory]::CreateDirectory($path) | Out-Null }
        $existed = Test-Path -LiteralPath $context.Config
        $mode = if ($existed) { [IO.FileMode]::Open } else { [IO.FileMode]::CreateNew }
        $stream = [IO.File]::Open($context.Config, $mode, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        $original = $null
        try {
            if ($existed) {
                $backup = [IO.MemoryStream]::new()
                try { $stream.CopyTo($backup); $original = $backup.ToArray() }
                finally { $backup.Dispose() }
            }
            $bytes = [Text.Encoding]::UTF8.GetBytes(($config | ConvertTo-Json))
            $stream.Position = 0
            $stream.SetLength(0)
            $stream.Write($bytes, 0, $bytes.Length)
            $stream.Flush()
            Register-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath -InputObject $definition -ErrorAction Stop | Out-Null
        }
        catch {
            if ($existed) {
                if ($null -ne $original) {
                    $stream.Position = 0
                    $stream.SetLength(0)
                    $stream.Write($original, 0, $original.Length)
                    $stream.Flush()
                }
            }
            else {
                $stream.Dispose()
                $null = Assert-GfrCaddyPath $context.Config -ExistingFile
                Remove-Item -LiteralPath $context.Config -ErrorAction Stop
            }
            throw
        }
        finally { $stream.Dispose() }
        Write-Output "Registered $($context.TaskName). Automatic HTTPS for $script:RequiredDomain still needs DNS pointed at this host and 80/443 forwarded before Start-Caddy.ps1 can obtain a certificate; neither is done by this step."
    }
}

function Start-GfrCaddy {
    [CmdletBinding(SupportsShouldProcess)] param()
    $context = Get-GfrCaddyContext; $task = Get-GfrCaddyOwnedTask $context
    if (-not $task) { throw 'GFR Caddy is not registered.' }
    if ($task.State -eq 'Running') { return }
    $null = Read-GfrCaddyConfiguration $context
    Assert-GfrCaddyPortsAvailable
    if ($PSCmdlet.ShouldProcess($context.TaskName, 'Start exact scheduled task')) { Start-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath }
}

function Stop-GfrCaddy {
    [CmdletBinding(SupportsShouldProcess)] param()
    $context = Get-GfrCaddyContext; $task = Get-GfrCaddyOwnedTask $context
    if (-not $task) { return }
    if ($PSCmdlet.ShouldProcess($context.TaskName, 'Stop exact scheduled task')) {
        Stop-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath
        Wait-GfrCaddyTaskStopped $context
    }
}

function Wait-GfrCaddyTaskStopped {
    param($Context)
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        $task = Get-GfrCaddyOwnedTask $Context
        if (-not $task -or $task.State -notin @('Running', 'Queued')) { return }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw 'The exact GFR Caddy task has not stopped; cleanup was not attempted.'
}

function Remove-GfrCaddyRuntimeData {
    [CmdletBinding(SupportsShouldProcess)] param($Context)
    $expected = Get-GfrCaddyContext
    if ($Context.Data -cne $expected.Data) { throw 'Cleanup target mismatch.' }
    if (-not $WhatIfPreference -and (Get-GfrCaddyOwnedTask $expected)) { throw 'Unregister the GFR Caddy task before cleanup.' }
    $null = Assert-GfrCaddyPath $Context.Data
    if (-not (Test-Path -LiteralPath $Context.Data)) { return }
    # caddy-data holds ACME account keys and certificates; it is kept out of
    # this allowlist so cleanup never silently discards live TLS material.
    foreach ($item in Get-ChildItem -LiteralPath $Context.Data -Force) {
        if ($item.Name -notin @('runtime.json', 'process.json', 'logs', 'caddy-config')) { throw 'Unknown data-directory contents (possibly caddy-data); cleanup refused.' }
    }
    $pending = [Collections.Generic.Queue[string]]::new(); $pending.Enqueue($Context.Data)
    $files = @(); $dirs = @()
    while ($pending.Count) {
        $dir = $pending.Dequeue()
        foreach ($item in Get-ChildItem -LiteralPath $dir -Force) {
            $full = Assert-GfrCaddyPath $item.FullName
            if (-not $full.StartsWith($Context.Data + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Cleanup escaped its target.' }
            if ($item.PSIsContainer) { $dirs += $full; $pending.Enqueue($full) } else { $files += $full }
        }
    }
    if ($PSCmdlet.ShouldProcess($Context.Data, 'Delete ONLY managed runtime configuration, config cache and logs (never caddy-data/certificates)')) {
        foreach ($file in $files) { Remove-Item -LiteralPath $file -Force }
        foreach ($dir in ($dirs | Sort-Object Length -Descending)) { Remove-Item -LiteralPath $dir -Force }
        Remove-Item -LiteralPath $Context.Data -Force
    }
}

function Uninstall-GfrCaddy {
    [CmdletBinding(SupportsShouldProcess)] param([switch]$CleanupRuntimeData)
    $context = Get-GfrCaddyContext; $task = Get-GfrCaddyOwnedTask $context
    if ($task) {
        if (-not $PSCmdlet.ShouldProcess($context.TaskName, 'Stop and unregister ONLY the exact GFR Caddy task; retain runtime files')) {
            if ($WhatIfPreference -and $CleanupRuntimeData) { Remove-GfrCaddyRuntimeData $context -WhatIf }
            return
        }
        Stop-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath
        Wait-GfrCaddyTaskStopped $context
        Unregister-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath -Confirm:$false
    }
    if ($CleanupRuntimeData) { Remove-GfrCaddyRuntimeData $context -WhatIf:$WhatIfPreference }
}

function Test-GfrCaddyHealth {
    param([ValidateRange(50,5000)][int]$TimeoutMs = 2000)
    # Caddy's admin API defaults to loopback-only 127.0.0.1:2019; this checks
    # the process is alive and holding a config without touching the public
    # site or requiring DNS/TLS to already work.
    Add-Type -AssemblyName System.Net.Http
    $handler = [Net.Http.HttpClientHandler]::new(); $handler.AllowAutoRedirect = $false; $handler.UseProxy = $false
    $client = [Net.Http.HttpClient]::new($handler); $client.Timeout = [TimeSpan]::FromMilliseconds($TimeoutMs)
    try {
        $response = $client.GetAsync('http://127.0.0.1:2019/config/', [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
        try { return [int]$response.StatusCode } finally { $response.Dispose() }
    } catch { return 0 } finally { $client.Dispose(); $handler.Dispose() }
}

function Test-GfrCaddyProcessIdentity {
    param($State, $Process, [string]$Executable)
    return $null -ne $Process -and $Process.Id -eq $State.Pid -and $Process.MainModule.FileName -ieq $Executable -and
        $Process.StartTime.ToUniversalTime().Ticks -eq [long]$State.StartTicks
}

function Get-GfrCaddyStatus {
    $context = Get-GfrCaddyContext; $task = $null; $taskState = 'Unavailable'
    try {
        $task = Get-GfrCaddyOwnedTask $context
        $taskState = if ($task) { [string]$task.State } else { 'NotRegistered' }
    } catch { $taskState = 'UnavailableOrOwnershipMismatch' }
    $processState = 'NotRecorded'; $ownedPid = $null
    if (Test-Path -LiteralPath $context.State) {
        try {
            $null = Assert-GfrCaddyPath $context.State -ExistingFile
            $state = Get-Content -LiteralPath $context.State -Raw | ConvertFrom-Json
            $config = Read-GfrCaddyConfiguration $context
            $process = Get-Process -Id ([int]$state.Pid) -ErrorAction Stop
            if (Test-GfrCaddyProcessIdentity $state $process $config.CaddyExecutable) { $processState = 'ExactRuntimeRunning'; $ownedPid = $process.Id }
            else { $processState = 'StaleOrForeign' }
        } catch { $processState = 'UnavailableOrStale' }
    }
    $ports = [Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners()
    $code = Test-GfrCaddyHealth
    [pscustomobject]@{ TaskName = $context.TaskName; TaskState = $taskState
        ProcessState = $processState; ProcessId = $ownedPid
        Port80 = $(if (@($ports | Where-Object Port -eq 80).Count) { 'Listening' } else { 'Closed' })
        Port443 = $(if (@($ports | Where-Object Port -eq 443).Count) { 'Listening' } else { 'Closed' })
        AdminAPIStatus = $code; AdminAPIHealthy = ($code -eq 200) }
}

Export-ModuleMember -Function *-GfrCaddy*
