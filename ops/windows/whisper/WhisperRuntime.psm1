#requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$script:ModelSHA1 = 'DB8A495A91D927739E50B3FC1CC4C6B8F6C2D022'

function Assert-GfrPath {
    param([Parameter(Mandatory)][string]$Path, [switch]$ExistingFile)
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
    if ($ExistingFile -and -not (Test-Path -LiteralPath $full -PathType Leaf)) { throw 'Required runtime file is missing.' }
    return $full
}

function ConvertTo-GfrArgument {
    param([AllowEmptyString()][string]$Value)
    if ($Value -match '[\x00-\x1f]') { throw 'Control characters are not allowed in arguments.' }
    # Windows CommandLineToArgvW/CRT quoting; no shell evaluates this string.
    $escaped = [regex]::Replace($Value, '(\\*)"', '$1$1\"')
    $escaped = [regex]::Replace($escaped, '(\\+)$', '$1$1')
    return '"' + $escaped + '"'
}

function Get-GfrContext {
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $data = Assert-GfrPath (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'GreenwichFireResponder\Whisper')
    $wrapper = Assert-GfrPath (Join-Path $PSScriptRoot 'Run-Whisper.ps1') -ExistingFile
    $shell = Assert-GfrPath (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') -ExistingFile
    [pscustomobject]@{
        Sid = $sid; TaskName = "GFR-Whisper-$sid"; TaskPath = '\'
        Description = "Greenwich Fire Responder local Whisper v1; owner $sid"
        Data = $data; Config = Join-Path $data 'runtime.json'; State = Join-Path $data 'process.json'
        Logs = Join-Path $data 'logs'; Temp = Join-Path $data 'temp'
        Wrapper = $wrapper; Shell = $shell
    }
}

function Get-GfrTaskArguments {
    param($Context)
    return (@('-NoLogo', '-NoProfile', '-NonInteractive', '-WindowStyle', 'Hidden', '-ExecutionPolicy', 'RemoteSigned',
        '-File', $Context.Wrapper) | ForEach-Object { ConvertTo-GfrArgument $_ }) -join ' '
}

function Get-GfrModelHash {
    param([string]$Path)
    # Windows PowerShell 5.1 Get-FileHash uses ForEach-Object ProviderPath,
    # whose ShouldProcess inherits WhatIf and suppresses the read-only lookup.
    # Its InputStream parameter bypasses that lookup without changing WhatIf
    # preferences in any scope. OpenRead never modifies the model.
    $stream = [IO.File]::OpenRead($Path)
    try { return Get-FileHash -InputStream $stream -Algorithm SHA1 }
    finally { $stream.Dispose() }
}

function New-GfrConfiguration {
    param($Context, [string]$Executable, [string]$Model, [string]$FFmpeg,
        [ValidateRange(1,64)][int]$LogMaxMB = 10, [ValidateRange(1,10)][int]$LogArchives = 3)
    $Executable = Assert-GfrPath $Executable -ExistingFile
    $Model = Assert-GfrPath $Model -ExistingFile
    $FFmpeg = Assert-GfrPath $FFmpeg -ExistingFile
    if ([IO.Path]::GetFileName($Executable) -ine 'whisper-server.exe' -or
        [IO.Path]::GetFileName($Model) -ine 'ggml-small.en.bin' -or
        [IO.Path]::GetFileName($FFmpeg) -ine 'ffmpeg.exe') { throw 'Unexpected executable or small.en model filename.' }
    foreach ($path in @($Executable, $Model, $FFmpeg)) {
        if ($path.StartsWith($Context.Data + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Runtime inputs must be outside the managed data directory.' }
    }
    if ((Get-GfrModelHash $Model).Hash -ine $script:ModelSHA1) { throw 'small.en model checksum mismatch.' }
    Assert-GfrExecutable $Executable
    Assert-GfrExecutable $FFmpeg
    [pscustomobject]@{ Version = 1; OwnerSid = $Context.Sid; Executable = $Executable; Model = $Model; FFmpeg = $FFmpeg
        LogMaxMB = $LogMaxMB; LogArchives = $LogArchives }
}

function Assert-GfrExecutable {
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

function Read-GfrConfiguration {
    param($Context)
    $null = Assert-GfrPath $Context.Config -ExistingFile
    $config = Get-Content -LiteralPath $Context.Config -Raw | ConvertFrom-Json
    if ($config.Version -ne 1 -or $config.OwnerSid -ne $Context.Sid) { throw 'Runtime configuration ownership mismatch.' }
    return New-GfrConfiguration $Context $config.Executable $config.Model $config.FFmpeg $config.LogMaxMB $config.LogArchives
}

function Assert-GfrPortAvailable {
    param([ValidateRange(1024,65535)][int]$Port = 8001)
    # Reserving all IPv4 interfaces detects loopback and wildcard conflicts.
    $socket = [Net.Sockets.Socket]::new([Net.Sockets.AddressFamily]::InterNetwork, [Net.Sockets.SocketType]::Stream, [Net.Sockets.ProtocolType]::Tcp)
    try { $socket.ExclusiveAddressUse = $true; $socket.Bind([Net.IPEndPoint]::new([Net.IPAddress]::Any, $Port)) }
    catch { throw 'Port 8001 is occupied; stop its owner manually. No process was stopped.' }
    finally { $socket.Dispose() }
}

function Get-GfrWhisperArguments {
    param($Configuration, $Context)
    return (@('--model', $Configuration.Model, '--language', 'en', '--no-gpu', '--threads', '8', '--processors', '1',
        '--host', '127.0.0.1', '--port', '8001', '--inference-path', '/v1/audio/transcriptions', '--convert', '--tmp-dir', $Context.Temp) |
        ForEach-Object { ConvertTo-GfrArgument $_ }) -join ' '
}

function Get-GfrOwnedTask {
    param($Context)
    $task = Get-ScheduledTask -TaskPath $Context.TaskPath -ErrorAction Stop | Where-Object { $_.TaskName -ceq $Context.TaskName }
    if (-not $task) { return $null }
    if (@($task).Count -ne 1 -or @($task.Actions).Count -ne 1 -or $task.Description -cne $Context.Description -or
        $task.Principal.UserId -ne $Context.Sid -or $task.Actions[0].Execute -ine $Context.Shell -or
        $task.Actions[0].Arguments -cne (Get-GfrTaskArguments $Context)) { throw 'Task ownership mismatch; refusing to manage it.' }
    return $task
}

function New-GfrTaskDefinition {
    param($Context)
    $action = New-ScheduledTaskAction -Execute $Context.Shell -Argument (Get-GfrTaskArguments $Context) -WorkingDirectory $Context.Data
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $Context.Sid
    $principal = New-ScheduledTaskPrincipal -UserId $Context.Sid -LogonType Interactive -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -StartWhenAvailable -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Seconds 30) -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    return New-ScheduledTask -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description $Context.Description
}

function Install-GfrWhisper {
    [CmdletBinding(SupportsShouldProcess)]
    param([string]$Executable = (Join-Path $env:USERPROFILE 'RadioTools\whisper-cpp\Release\whisper-server.exe'),
        [string]$Model = (Join-Path $env:USERPROFILE 'RadioTools\whisper-cpp\models\ggml-small.en.bin'),
        [string]$FFmpeg, [ValidateRange(1,64)][int]$LogMaxMB = 10, [ValidateRange(1,10)][int]$LogArchives = 3)
    $context = Get-GfrContext
    if (Get-GfrOwnedTask $context) { throw 'The exact GFR task already exists; uninstall it before registering again.' }
    if (-not $FFmpeg) { $FFmpeg = (Get-Command ffmpeg.exe -CommandType Application -ErrorAction Stop).Source }
    $config = New-GfrConfiguration $context $Executable $Model $FFmpeg $LogMaxMB $LogArchives
    $null = Assert-GfrPath $context.Config
    Assert-GfrPortAvailable
    $definition = New-GfrTaskDefinition $context
    if ($PSCmdlet.ShouldProcess($context.TaskName, 'Create user-local runtime configuration and register logon task (do not start)')) {
        foreach ($path in @($context.Data, $context.Temp, $context.Logs)) { $null = Assert-GfrPath $path; [IO.Directory]::CreateDirectory($path) | Out-Null }
        $config | ConvertTo-Json | Set-Content -LiteralPath $context.Config -Encoding UTF8
        Register-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath -InputObject $definition -ErrorAction Stop | Out-Null
    }
}

function Start-GfrWhisper {
    [CmdletBinding(SupportsShouldProcess)] param()
    $context = Get-GfrContext; $task = Get-GfrOwnedTask $context
    if (-not $task) { throw 'GFR Whisper is not registered.' }
    if ($task.State -eq 'Running') { return }
    $null = Read-GfrConfiguration $context
    Assert-GfrPortAvailable
    if ($PSCmdlet.ShouldProcess($context.TaskName, 'Start exact scheduled task')) { Start-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath }
}

function Stop-GfrWhisper {
    [CmdletBinding(SupportsShouldProcess)] param()
    $context = Get-GfrContext; $task = Get-GfrOwnedTask $context
    if (-not $task) { return }
    if ($PSCmdlet.ShouldProcess($context.TaskName, 'Stop exact scheduled task and its job-contained children')) {
        Stop-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath
        Wait-GfrTaskStopped $context
    }
}

function Wait-GfrTaskStopped {
    param($Context)
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        $task = Get-GfrOwnedTask $Context
        if (-not $task -or $task.State -notin @('Running','Queued')) { return }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw 'The exact GFR task has not stopped; cleanup was not attempted.'
}

function Remove-GfrRuntimeData {
    [CmdletBinding(SupportsShouldProcess)] param($Context)
    $expected = Get-GfrContext
    if ($Context.Data -cne $expected.Data) { throw 'Cleanup target mismatch.' }
    if (-not $WhatIfPreference -and (Get-GfrOwnedTask $expected)) { throw 'Unregister the GFR task before cleanup.' }
    $null = Assert-GfrPath $Context.Data
    if (-not (Test-Path -LiteralPath $Context.Data)) { return }
    foreach ($item in Get-ChildItem -LiteralPath $Context.Data -Force) {
        if ($item.Name -notin @('runtime.json', 'process.json', 'logs', 'temp')) { throw 'Unknown data-directory contents; cleanup refused.' }
    }
    # Validate every descendant first. Never traverse a junction during deletion.
    $pending = [Collections.Generic.Queue[string]]::new(); $pending.Enqueue($Context.Data)
    $files = @(); $dirs = @()
    while ($pending.Count) {
        $dir = $pending.Dequeue()
        foreach ($item in Get-ChildItem -LiteralPath $dir -Force) {
            $full = Assert-GfrPath $item.FullName
            if (-not $full.StartsWith($Context.Data + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Cleanup escaped its target.' }
            if ($item.PSIsContainer) { $dirs += $full; $pending.Enqueue($full) } else { $files += $full }
        }
    }
    if ($PSCmdlet.ShouldProcess($Context.Data, 'Delete ONLY managed runtime configuration, temporary data and logs')) {
        foreach ($file in $files) { Remove-Item -LiteralPath $file -Force }
        foreach ($dir in ($dirs | Sort-Object Length -Descending)) { Remove-Item -LiteralPath $dir -Force }
        Remove-Item -LiteralPath $Context.Data -Force
    }
}

function Uninstall-GfrWhisper {
    [CmdletBinding(SupportsShouldProcess)] param([switch]$CleanupRuntimeData)
    $context = Get-GfrContext; $task = Get-GfrOwnedTask $context
    if ($task) {
        if (-not $PSCmdlet.ShouldProcess($context.TaskName, 'Stop and unregister ONLY the exact GFR task; retain runtime files')) {
            if ($WhatIfPreference -and $CleanupRuntimeData) { Remove-GfrRuntimeData $context -WhatIf }
            return
        }
        Stop-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath
        Wait-GfrTaskStopped $context
        Unregister-ScheduledTask -TaskName $context.TaskName -TaskPath $context.TaskPath -Confirm:$false
    }
    if ($CleanupRuntimeData) { Remove-GfrRuntimeData $context -WhatIf:$WhatIfPreference }
}

function Test-GfrHealth {
    param([ValidateRange(1024,65535)][int]$Port = 8001, [ValidateRange(50,5000)][int]$TimeoutMs = 2000)
    Add-Type -AssemblyName System.Net.Http
    $handler = [Net.Http.HttpClientHandler]::new(); $handler.AllowAutoRedirect = $false; $handler.UseProxy = $false
    $client = [Net.Http.HttpClient]::new($handler); $client.Timeout = [TimeSpan]::FromMilliseconds($TimeoutMs)
    try {
        $response = $client.GetAsync("http://127.0.0.1:$Port/", [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
        try { return [int]$response.StatusCode } finally { $response.Dispose() }
    } catch { return 0 } finally { $client.Dispose(); $handler.Dispose() }
}

function Test-GfrProcessIdentity {
    param($State, $Process, [string]$Executable)
    return $null -ne $Process -and $Process.Id -eq $State.Pid -and $Process.MainModule.FileName -ieq $Executable -and
        $Process.StartTime.ToUniversalTime().Ticks -eq [long]$State.StartTicks
}

function Get-GfrWhisperStatus {
    $context = Get-GfrContext; $task = $null; $taskState = 'Unavailable'
    try {
        $task = Get-GfrOwnedTask $context
        $taskState = if ($task) { [string]$task.State } else { 'NotRegistered' }
    } catch { $taskState = 'UnavailableOrOwnershipMismatch' }
    $processState = 'NotRecorded'; $ownedPid = $null
    if (Test-Path -LiteralPath $context.State) {
        try {
            $null = Assert-GfrPath $context.State -ExistingFile
            $state = Get-Content -LiteralPath $context.State -Raw | ConvertFrom-Json
            $config = Read-GfrConfiguration $context
            $process = Get-Process -Id ([int]$state.Pid) -ErrorAction Stop
            if (Test-GfrProcessIdentity $state $process $config.Executable) { $processState = 'ExactRuntimeRunning'; $ownedPid = $process.Id }
            else { $processState = 'StaleOrForeign' }
        } catch { $processState = 'UnavailableOrStale' }
    }
    $listeners = @([Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners() | Where-Object Port -eq 8001)
    $code = Test-GfrHealth
    [pscustomobject]@{ TaskName = $context.TaskName; TaskState = $taskState
        ProcessState = $processState; ProcessId = $ownedPid; Port8001 = $(if ($listeners.Count) { 'Listening' } else { 'Closed' })
        Addresses = @($listeners | ForEach-Object { $_.Address.ToString() }); HTTPStatus = $code; HTTPHealthy = ($code -eq 200) }
}

Export-ModuleMember -Function *-Gfr*
