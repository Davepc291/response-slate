#requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Step 9F-7D production Go API runtime. Mirrors the ops/windows/whisper
# Task Scheduler conventions (per-user logon task, ownership verification,
# WhatIf-gated mutation, fail-closed configuration) without reusing that
# module's names, so both can be imported in the same session safely.
#
# 9F-7K correction: the task's action is a PowerShell wrapper
# (Run-Api.ps1) that launches gfr-api.exe as a SEPARATE child process via
# Start-Process — gfr-api.exe is a grandchild of the scheduled task, not
# its direct action process. Stop-ScheduledTask only terminates the
# wrapper it launched directly; Windows does not cascade termination to a
# grandchild automatically. A kill-on-close Job Object was tried and
# empirically disproven on this system: both the wrapper and the API
# process are already nested inside an existing job (Task Scheduler's own
# per-task hosting job), and SetInformationJobObject on a second,
# independently created job reports success while silently failing to
# persist KILL_ON_JOB_CLOSE for an already-jobbed process (confirmed via
# QueryInformationJobObject readback in an isolated sandbox test — see the
# 9F-7K investigation). Stop-GfrApi instead terminates the tracked child
# PID directly, using the exact identity (PID + path + StartTicks) already
# recorded in process.json and already verified by
# Test-GfrApiProcessIdentity/Get-GfrApiStatus, rather than relying on any
# OS-level cascade. Log rotation here happens once per Start rather than
# continuously mid-run; see Get-GfrApiStatus/Start-GfrApi.
$script:RequiredHttpAddr = '127.0.0.1:8080'

function Assert-GfrApiPath {
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

function ConvertTo-GfrApiArgument {
    param([AllowEmptyString()][string]$Value)
    if ($Value -match '[\x00-\x1f]') { throw 'Control characters are not allowed in arguments.' }
    $escaped = [regex]::Replace($Value, '(\\*)"', '$1$1\"')
    $escaped = [regex]::Replace($escaped, '(\\+)$', '$1$1')
    return '"' + $escaped + '"'
}

function Assert-GfrApiExecutable {
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

function Get-GfrApiContext {
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $data = Assert-GfrApiPath (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'GreenwichFireResponder\Api')
    $binDir = Assert-GfrApiPath (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'GreenwichFireResponder\ApiBin')
    $wrapper = Assert-GfrApiPath (Join-Path $PSScriptRoot 'Run-Api.ps1') -ExistingFile
    $shell = Assert-GfrApiPath (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') -ExistingFile
    [pscustomobject]@{
        Sid = $sid; TaskName = "GFR-Api-$sid"; TaskPath = '\'
        Description = "Greenwich Fire Responder production API; owner $sid"
        Data = $data; BinDir = $binDir; Config = Join-Path $data 'runtime.json'; State = Join-Path $data 'process.json'
        Logs = Join-Path $data 'logs'; EnvFile = Join-Path $data 'api.env'
        Wrapper = $wrapper; Shell = $shell
    }
}

function Get-GfrApiTaskArguments {
    param($Context)
    return (@('-NoLogo', '-NoProfile', '-NonInteractive', '-WindowStyle', 'Hidden', '-ExecutionPolicy', 'RemoteSigned',
        '-File', $Context.Wrapper) | ForEach-Object { ConvertTo-GfrApiArgument $_ }) -join ' '
}

function New-GfrApiConfiguration {
    param($Context, [string]$Executable, [string]$HttpAddr = $script:RequiredHttpAddr,
        [ValidateRange(1,64)][int]$LogMaxMB = 10, [ValidateRange(1,10)][int]$LogArchives = 3)
    $Executable = Assert-GfrApiPath $Executable -ExistingFile
    if ([IO.Path]::GetFileName($Executable) -ine 'gfr-api.exe') { throw 'Expected the production binary to be named gfr-api.exe.' }
    if ($Executable.StartsWith($Context.Data + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'The API binary must be built outside the managed runtime data directory.' }
    if ($HttpAddr -ne $script:RequiredHttpAddr) { throw "The production API must bind exactly to $script:RequiredHttpAddr (loopback only)." }
    Assert-GfrApiExecutable $Executable
    [pscustomobject]@{ Version = 1; OwnerSid = $Context.Sid; Executable = $Executable; HttpAddr = $HttpAddr
        LogMaxMB = $LogMaxMB; LogArchives = $LogArchives }
}

function Read-GfrApiConfiguration {
    param($Context)
    $null = Assert-GfrApiPath $Context.Config -ExistingFile
    $config = Get-Content -LiteralPath $Context.Config -Raw | ConvertFrom-Json
    if ($config.Version -ne 1 -or $config.OwnerSid -ne $Context.Sid) { throw 'Runtime configuration ownership mismatch.' }
    return New-GfrApiConfiguration $Context $config.Executable $config.HttpAddr $config.LogMaxMB $config.LogArchives
}

function Assert-GfrApiPortAvailable {
    # Reserving all IPv4 interfaces detects loopback and wildcard conflicts.
    $socket = [Net.Sockets.Socket]::new([Net.Sockets.AddressFamily]::InterNetwork, [Net.Sockets.SocketType]::Stream, [Net.Sockets.ProtocolType]::Tcp)
    try { $socket.ExclusiveAddressUse = $true; $socket.Bind([Net.IPEndPoint]::new([Net.IPAddress]::Any, 8080)) }
    catch { throw 'Port 8080 is occupied; stop its owner manually. No process was stopped.' }
    finally { $socket.Dispose() }
}

function ConvertTo-GfrApiAccountSid {
    param([string]$Account)
    return ([Security.Principal.NTAccount]::new($Account)).Translate([Security.Principal.SecurityIdentifier]).Value
}

function Resolve-GfrApiPrincipalSid {
    param([string]$UserId)
    try {
        if ([string]::IsNullOrWhiteSpace($UserId)) { throw 'Missing principal.' }
        if ($UserId -match '^S-1-') { return ([Security.Principal.SecurityIdentifier]::new($UserId)).Value }
        if ($UserId -notmatch '[\\@]') { $UserId = [Environment]::MachineName + '\' + $UserId }
        elseif ($UserId.StartsWith('.\')) { $UserId = [Environment]::MachineName + $UserId.Substring(1) }
        return ConvertTo-GfrApiAccountSid $UserId
    }
    catch { throw 'Task principal could not be resolved; refusing to manage it.' }
}

function Get-GfrApiOwnedTask {
    param($Context)
    $task = Get-ScheduledTask -TaskPath $Context.TaskPath -ErrorAction Stop | Where-Object { $_.TaskName -ceq $Context.TaskName }
    if (-not $task) { return $null }
    if (@($task).Count -ne 1 -or @($task.Actions).Count -ne 1 -or $task.Description -cne $Context.Description -or
        $task.TaskPath -cne $Context.TaskPath -or
        (Resolve-GfrApiPrincipalSid $task.Principal.UserId) -ne $Context.Sid -or
        $task.Principal.LogonType -ne 'Interactive' -or $task.Principal.RunLevel -ne 'Limited' -or
        $task.Actions[0].Execute -ine $Context.Shell -or
        $task.Actions[0].WorkingDirectory -ine $Context.Data -or
        $task.Actions[0].Arguments -cne (Get-GfrApiTaskArguments $Context)) { throw 'Task ownership mismatch; refusing to manage it.' }
    return $task
}

function Assert-GfrApiRestartPolicy {
    param($Settings)
    $interval = [Xml.XmlConvert]::ToTimeSpan($Settings.RestartInterval)
    if ($interval -lt [TimeSpan]::FromMinutes(1) -or $Settings.RestartCount -ne 3) {
        throw 'GFR requires exactly three restart attempts with an interval of at least one minute.'
    }
}

function New-GfrApiTaskDefinition {
    param($Context)
    $action = New-ScheduledTaskAction -Execute $Context.Shell -Argument (Get-GfrApiTaskArguments $Context) -WorkingDirectory $Context.Data
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $Context.Sid
    $principal = New-ScheduledTaskPrincipal -UserId $Context.Sid -LogonType Interactive -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -StartWhenAvailable -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    Assert-GfrApiRestartPolicy $settings
    return New-ScheduledTask -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description $Context.Description
}

function Install-GfrApi {
    [CmdletBinding(SupportsShouldProcess)]
    param([string]$Executable, [string]$HttpAddr = $script:RequiredHttpAddr,
        [ValidateRange(1,64)][int]$LogMaxMB = 10, [ValidateRange(1,10)][int]$LogArchives = 3)
    $context = Get-GfrApiContext
    if (-not $Executable) { $Executable = Join-Path $context.BinDir 'gfr-api.exe' }
    if (Get-GfrApiOwnedTask $context) { throw 'The exact GFR API task already exists; uninstall it before registering again.' }
    $config = New-GfrApiConfiguration $context $Executable $HttpAddr $LogMaxMB $LogArchives
    $null = Assert-GfrApiPath $context.Config
    Assert-GfrApiPortAvailable
    $definition = New-GfrApiTaskDefinition $context
    if ($PSCmdlet.ShouldProcess($context.TaskName, 'Create user-local runtime configuration and register logon task (do not start)')) {
        foreach ($path in @($context.Data, $context.Logs)) { $null = Assert-GfrApiPath $path; [IO.Directory]::CreateDirectory($path) | Out-Null }
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
                $null = Assert-GfrApiPath $context.Config -ExistingFile
                Remove-Item -LiteralPath $context.Config -ErrorAction Stop
            }
            throw
        }
        finally { $stream.Dispose() }
        Write-Output "Registered $($context.TaskName). Provision $($context.EnvFile) with real production values before Start-Api.ps1 (see .env.production.example); this step never writes secrets."
    }
}

function Start-GfrApi {
    [CmdletBinding(SupportsShouldProcess)] param()
    $context = Get-GfrApiContext; $task = Get-GfrApiOwnedTask $context
    if (-not $task) { throw 'GFR API is not registered.' }
    if ($task.State -eq 'Running') { return }
    $null = Read-GfrApiConfiguration $context
    if (-not (Test-Path -LiteralPath $context.EnvFile -PathType Leaf)) { throw "Missing $($context.EnvFile); provision production environment values before starting (never committed to git)." }
    Assert-GfrApiPortAvailable
    if ($PSCmdlet.ShouldProcess($context.TaskName, 'Start exact scheduled task')) { Start-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath }
}

function Stop-GfrApi {
    [CmdletBinding(SupportsShouldProcess)] param()
    $context = Get-GfrApiContext; $task = Get-GfrApiOwnedTask $context
    if (-not $task) { return }
    if ($PSCmdlet.ShouldProcess($context.TaskName, 'Terminate the tracked API process and stop its scheduled task')) {
        # Terminate the actual tracked API process directly: Stop-ScheduledTask
        # only terminates the wrapper it launched, not this grandchild (see
        # module header comment — a kill-on-close Job Object was tried and
        # empirically disproven on this system). Best-effort: any failure
        # reading/verifying the tracked state (missing, stale, or identity
        # mismatch) simply falls through to the Stop-ScheduledTask +
        # Wait-GfrApiPortFree path below, which still fails loudly if
        # anything is left bound to the port.
        try {
            if (Test-Path -LiteralPath $context.State) {
                $null = Assert-GfrApiPath $context.State -ExistingFile
                $state = Get-Content -LiteralPath $context.State -Raw | ConvertFrom-Json
                $config = Read-GfrApiConfiguration $context
                $process = Get-Process -Id ([int]$state.Pid) -ErrorAction Stop
                if (Test-GfrApiProcessIdentity $state $process $config.Executable) {
                    Stop-Process -Id $process.Id -Force
                }
            }
        } catch { }
        Stop-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath
        Wait-GfrApiTaskStopped $context
        Wait-GfrApiPortFree
    }
}

# Verifies port 8080 is actually free before Stop-GfrApi reports success,
# rather than trusting that terminating the tracked process and the task's
# wrapper was sufficient. Throws loudly instead of returning a false
# "stopped" if anything is still bound.
function Wait-GfrApiPortFree {
    $deadline = [DateTime]::UtcNow.AddSeconds(5)
    do {
        if (-not @([Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners() | Where-Object Port -eq 8080).Count) { return }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw 'Port 8080 is still bound after stopping the GFR API task; an orphaned process may remain. Run Status-Api.ps1 before starting again.'
}

function Wait-GfrApiTaskStopped {
    param($Context)
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        $task = Get-GfrApiOwnedTask $Context
        if (-not $task -or $task.State -notin @('Running','Queued')) { return }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw 'The exact GFR API task has not stopped; cleanup was not attempted.'
}

function Remove-GfrApiRuntimeData {
    [CmdletBinding(SupportsShouldProcess)] param($Context)
    $expected = Get-GfrApiContext
    if ($Context.Data -cne $expected.Data) { throw 'Cleanup target mismatch.' }
    if (-not $WhatIfPreference -and (Get-GfrApiOwnedTask $expected)) { throw 'Unregister the GFR API task before cleanup.' }
    $null = Assert-GfrApiPath $Context.Data
    if (-not (Test-Path -LiteralPath $Context.Data)) { return }
    # api.env may hold production secrets and is deliberately NOT in this
    # allowlist: its presence makes cleanup refuse, forcing an explicit,
    # manual deletion decision instead of silently discarding secrets.
    foreach ($item in Get-ChildItem -LiteralPath $Context.Data -Force) {
        if ($item.Name -notin @('runtime.json', 'process.json', 'logs')) { throw 'Unknown data-directory contents (possibly api.env); cleanup refused.' }
    }
    $pending = [Collections.Generic.Queue[string]]::new(); $pending.Enqueue($Context.Data)
    $files = @(); $dirs = @()
    while ($pending.Count) {
        $dir = $pending.Dequeue()
        foreach ($item in Get-ChildItem -LiteralPath $dir -Force) {
            $full = Assert-GfrApiPath $item.FullName
            if (-not $full.StartsWith($Context.Data + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Cleanup escaped its target.' }
            if ($item.PSIsContainer) { $dirs += $full; $pending.Enqueue($full) } else { $files += $full }
        }
    }
    if ($PSCmdlet.ShouldProcess($Context.Data, 'Delete ONLY managed runtime configuration and logs (never api.env)')) {
        foreach ($file in $files) { Remove-Item -LiteralPath $file -Force }
        foreach ($dir in ($dirs | Sort-Object Length -Descending)) { Remove-Item -LiteralPath $dir -Force }
        Remove-Item -LiteralPath $Context.Data -Force
    }
}

function Uninstall-GfrApi {
    [CmdletBinding(SupportsShouldProcess)] param([switch]$CleanupRuntimeData)
    $context = Get-GfrApiContext; $task = Get-GfrApiOwnedTask $context
    if ($task) {
        if (-not $PSCmdlet.ShouldProcess($context.TaskName, 'Stop and unregister ONLY the exact GFR API task; retain runtime files')) {
            if ($WhatIfPreference -and $CleanupRuntimeData) { Remove-GfrApiRuntimeData $context -WhatIf }
            return
        }
        Stop-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath
        Wait-GfrApiTaskStopped $context
        Unregister-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath -Confirm:$false
    }
    if ($CleanupRuntimeData) { Remove-GfrApiRuntimeData $context -WhatIf:$WhatIfPreference }
}

function Test-GfrApiHealth {
    param([ValidateRange(50,5000)][int]$TimeoutMs = 2000)
    Add-Type -AssemblyName System.Net.Http
    $handler = [Net.Http.HttpClientHandler]::new(); $handler.AllowAutoRedirect = $false; $handler.UseProxy = $false
    $client = [Net.Http.HttpClient]::new($handler); $client.Timeout = [TimeSpan]::FromMilliseconds($TimeoutMs)
    try {
        $response = $client.GetAsync('http://127.0.0.1:8080/api/health', [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
        try { return [int]$response.StatusCode } finally { $response.Dispose() }
    } catch { return 0 } finally { $client.Dispose(); $handler.Dispose() }
}

function Test-GfrApiProcessIdentity {
    param($State, $Process, [string]$Executable)
    return $null -ne $Process -and $Process.Id -eq $State.Pid -and $Process.MainModule.FileName -ieq $Executable -and
        $Process.StartTime.ToUniversalTime().Ticks -eq [long]$State.StartTicks
}

function Get-GfrApiStatus {
    $context = Get-GfrApiContext; $task = $null; $taskState = 'Unavailable'
    try {
        $task = Get-GfrApiOwnedTask $context
        $taskState = if ($task) { [string]$task.State } else { 'NotRegistered' }
    } catch { $taskState = 'UnavailableOrOwnershipMismatch' }
    $processState = 'NotRecorded'; $ownedPid = $null
    if (Test-Path -LiteralPath $context.State) {
        try {
            $null = Assert-GfrApiPath $context.State -ExistingFile
            $state = Get-Content -LiteralPath $context.State -Raw | ConvertFrom-Json
            $config = Read-GfrApiConfiguration $context
            $process = Get-Process -Id ([int]$state.Pid) -ErrorAction Stop
            if (Test-GfrApiProcessIdentity $state $process $config.Executable) { $processState = 'ExactRuntimeRunning'; $ownedPid = $process.Id }
            else { $processState = 'StaleOrForeign' }
        } catch { $processState = 'UnavailableOrStale' }
    }
    $listeners = @([Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners() | Where-Object Port -eq 8080)
    $portOwnerPid = $null
    if ($listeners.Count) {
        try { $portOwnerPid = (Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction Stop | Select-Object -First 1 -ExpandProperty OwningProcess) } catch {}
    }
    $code = Test-GfrApiHealth
    [pscustomobject]@{ TaskName = $context.TaskName; TaskState = $taskState
        ProcessState = $processState; ProcessId = $ownedPid; Port8080 = $(if ($listeners.Count) { 'Listening' } else { 'Closed' })
        Addresses = @($listeners | ForEach-Object { $_.Address.ToString() }); HTTPStatus = $code; HTTPHealthy = ($code -eq 200)
        PortOwnerProcessId = $portOwnerPid
        OrphanSuspected = [bool]($listeners.Count -and (-not $ownedPid -or $portOwnerPid -ne $ownedPid)) }
}

Export-ModuleMember -Function *-GfrApi*
