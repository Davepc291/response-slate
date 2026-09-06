# Pester 3.4+; only temporary fixtures and in-memory scheduled-task mocks.
$root = Split-Path $PSScriptRoot -Parent
Import-Module (Join-Path $root 'WhisperRuntime.psm1') -Force
if (-not ('Gfr.Whisper.RotatingLog' -as [type])) { Add-Type -Path (Join-Path $root 'RuntimeHost.cs') }
if (-not ('GfrHealthFixture' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
public sealed class GfrHealthFixture : IDisposable {
    TcpListener listener; Task task;
    public int Port { get; private set; }
    public GfrHealthFixture(int status, int delay) {
        listener = new TcpListener(IPAddress.Loopback, 0); listener.Start(); Port = ((IPEndPoint)listener.LocalEndpoint).Port;
        task = Task.Run(() => { try { using(var c = listener.AcceptTcpClient()) {
            var stream=c.GetStream(); var buffer=new byte[2048]; stream.Read(buffer,0,buffer.Length);
            Thread.Sleep(delay); var bytes=Encoding.ASCII.GetBytes("HTTP/1.1 "+status+" Test\r\nLocation: http://127.0.0.1:1/\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
            stream.Write(bytes,0,bytes.Length);
        } } catch {} });
    }
    public void Dispose() {listener.Stop();task.Wait(2000);}
}
'@
}

Describe 'Native quoting and local path validation' {
    It 'quotes spaces, apostrophes, and a trailing slash without a shell' {
        (ConvertTo-GfrArgument "C:\a b\O'Brien\") | Should Be '"C:\a b\O''Brien\\"'
        (ConvertTo-GfrArgument 'a"b') | Should Be '"a\"b"'
        (ConvertTo-GfrArgument '') | Should Be '""'
    }
    It 'rejects relative, UNC, traversal, ADS, and command metacharacters' {
        foreach ($value in @('relative.exe','C:relative.exe','\\server\share\x','C:\a\..\x','C:\a:stream',
            'C:\a&b','C:\a;b','C:\a$(x)','C:\a`x','C:\%PATH%\x','C:\a!b',"C:\a`nb",'C:\a.\x')) {
            { Assert-GfrPath $value } | Should Throw
        }
    }
    It 'accepts a real file with spaces and apostrophes' {
        $path = Join-Path $TestDrive "O'Brien model.bin"
        Set-Content -LiteralPath $path -Value 'fixture'
        (Assert-GfrPath $path -ExistingFile) | Should Be $path
    }
    It 'rejects missing files and invalid log bounds' {
        { Assert-GfrPath (Join-Path $TestDrive 'missing.exe') -ExistingFile } | Should Throw
        { New-GfrConfiguration -LogMaxMB 0 } | Should Throw
    }
    It 'rejects a fake MZ file without a PE header' {
        $path=Join-Path $TestDrive 'fake.exe'
        [IO.File]::WriteAllBytes($path,[Text.Encoding]::ASCII.GetBytes('MZ'+('x'*100)))
        { Assert-GfrExecutable $path } | Should Throw
        { Assert-GfrExecutable (Join-Path $env:SystemRoot 'System32\cmd.exe') } | Should Not Throw
    }
    It 'builds only the required loopback CPU runtime command' {
        $args = Get-GfrWhisperArguments ([pscustomobject]@{Model='C:\models with spaces\ggml-small.en.bin'}) ([pscustomobject]@{Temp='C:\local data\temp'})
        $args | Should Be '"--model" "C:\models with spaces\ggml-small.en.bin" "--language" "en" "--no-gpu" "--threads" "8" "--processors" "1" "--host" "127.0.0.1" "--port" "8001" "--inference-path" "/v1/audio/transcriptions" "--convert" "--tmp-dir" "C:\local data\temp"'
    }
}

Describe 'Top-level installer WhatIf checksum regression' {
    # Preserve this loaded module and its scheduler mocks when the real entry
    # script imports it. All file, hash, PE and port validation stays real.
    Mock Import-Module {}
    Mock Get-GfrOwnedTask -ModuleName WhisperRuntime { $null }
    Mock New-GfrTaskDefinition -ModuleName WhisperRuntime { [pscustomobject]@{Kind='test definition'} }
    Mock Register-ScheduledTask -ModuleName WhisperRuntime { throw 'Registration must never run.' }
    Mock Set-Content -ModuleName WhisperRuntime { throw 'Configuration writes must never run.' }
    It 'hashes real bytes under StrictMode through Install-Whisper.ps1 -WhatIf' {
        Set-StrictMode -Version Latest
        $fixture=Join-Path $TestDrive 'runtime inputs with spaces'
        $null=[IO.Directory]::CreateDirectory($fixture)
        $exe=Join-Path $fixture 'whisper-server.exe'
        $ffmpeg=Join-Path $fixture 'ffmpeg.exe'
        $model=Join-Path $fixture 'ggml-small.en.bin'
        [IO.File]::Copy((Join-Path $env:SystemRoot 'System32\cmd.exe'),$exe)
        [IO.File]::Copy($exe,$ffmpeg)
        [IO.File]::WriteAllText($model,'abc',[Text.Encoding]::ASCII)
        $savedHash= & (Get-Module WhisperRuntime) { $script:ModelSHA1 }
        $savedPreference=$WhatIfPreference
        # A fixture checksum replaces only the expected digest, never hashing.
        & (Get-Module WhisperRuntime) { $script:ModelSHA1='A9993E364706816ABA3E25717850C26C9CD0D89D' }
        $entry=Join-Path (Split-Path $PSScriptRoot -Parent) 'Install-Whisper.ps1'
        # Use an ephemeral port for the real exclusive-bind check.
        Mock Assert-GfrPortAvailable -ModuleName WhisperRuntime {
            $socket=[Net.Sockets.Socket]::new([Net.Sockets.AddressFamily]::InterNetwork,[Net.Sockets.SocketType]::Stream,[Net.Sockets.ProtocolType]::Tcp)
            try { $socket.ExclusiveAddressUse=$true; $socket.Bind([Net.IPEndPoint]::new([Net.IPAddress]::Any,0)) }
            finally { $socket.Dispose() }
        }
        try {
            $WhatIfPreference=$true
            { & $entry -Executable $exe -Model $model -FFmpeg $ffmpeg -WhatIf } | Should Not Throw
            $WhatIfPreference | Should Be $true
            Assert-MockCalled Assert-GfrPortAvailable -ModuleName WhisperRuntime -Times 1 -Scope It
            Assert-MockCalled New-GfrTaskDefinition -ModuleName WhisperRuntime -Times 1 -Scope It
            Assert-MockCalled Register-ScheduledTask -ModuleName WhisperRuntime -Times 0 -Scope It
            Assert-MockCalled Set-Content -ModuleName WhisperRuntime -Times 0 -Scope It
            [IO.File]::WriteAllText($model,'wrong',[Text.Encoding]::ASCII)
            { & $entry -Executable $exe -Model $model -FFmpeg $ffmpeg -WhatIf } | Should Throw 'small.en model checksum mismatch.'
        }
        finally { $WhatIfPreference=$savedPreference; & (Get-Module WhisperRuntime) { param($digest) $script:ModelSHA1=$digest } $savedHash }
    }
}
InModuleScope WhisperRuntime {
    Describe 'Reparse-point refusal' {
        Mock Test-Path {$true}
        Mock Get-Item { [pscustomobject]@{Attributes=[IO.FileAttributes]::ReparsePoint} }
        It 'refuses a linked configuration path before it can be overwritten' {
            { Assert-GfrPath 'C:\fixture\runtime.json' } | Should Throw
        }
    }
    Describe 'Validation before registration' {
        Mock Assert-GfrPath { return $Path }
        Mock Get-GfrModelHash { [pscustomobject]@{Hash='wrong'} }
        It 'rejects a checksum mismatch' {
            $ctx=[pscustomobject]@{Data='C:\managed';Sid='fixture'}
            { New-GfrConfiguration $ctx 'C:\bin\whisper-server.exe' 'C:\model\ggml-small.en.bin' 'C:\bin\ffmpeg.exe' } | Should Throw
        }
        It 'rejects models or binaries inside cleanup scope' {
            $ctx=[pscustomobject]@{Data='C:\managed';Sid='fixture'}
            { New-GfrConfiguration $ctx 'C:\managed\whisper-server.exe' 'C:\model\ggml-small.en.bin' 'C:\bin\ffmpeg.exe' } | Should Throw
        }
        It 'accepts only the exact verified model checksum' {
            Mock Get-GfrModelHash { [pscustomobject]@{Hash='DB8A495A91D927739E50B3FC1CC4C6B8F6C2D022'} }
            Mock Assert-GfrExecutable {}
            $ctx=[pscustomobject]@{Data='C:\managed';Sid='fixture'}
            $config=New-GfrConfiguration $ctx 'C:\bin\whisper-server.exe' 'C:\model\ggml-small.en.bin' 'C:\bin\ffmpeg.exe'
            $config.OwnerSid | Should Be 'fixture'
            Assert-MockCalled Assert-GfrExecutable -Times 2 -Scope It
        }
    }
    Describe 'Task definition without registering a task' {
        Mock New-ScheduledTaskAction { [Microsoft.Management.Infrastructure.CimInstance]::new('MSFT_TaskAction') }
        Mock New-ScheduledTaskTrigger { [Microsoft.Management.Infrastructure.CimInstance]::new('MSFT_TaskTrigger') }
        Mock New-ScheduledTaskPrincipal { [Microsoft.Management.Infrastructure.CimInstance]::new('MSFT_TaskPrincipal') }
        Mock New-ScheduledTaskSettingsSet {
            $settings=[Microsoft.Management.Infrastructure.CimInstance]::new('MSFT_TaskSettings')
            $settings | Add-Member NoteProperty RestartInterval 'PT1M'
            $settings | Add-Member NoteProperty RestartCount 3
            return $settings
        }
        Mock New-ScheduledTask { [pscustomobject]@{Kind='task'} }
        It 'uses interactive current-user logon, least privilege, and bounded restart' {
            $ctx=Get-GfrContext
            $null=New-GfrTaskDefinition $ctx
            Assert-MockCalled New-ScheduledTaskTrigger -Times 1 -ParameterFilter {$AtLogOn -and $User -like 'S-1-*'}
            Assert-MockCalled New-ScheduledTaskPrincipal -Times 1 -ParameterFilter {$LogonType -eq 'Interactive' -and $RunLevel -eq 'Limited'}
            Assert-MockCalled New-ScheduledTaskSettingsSet -Times 1 -ParameterFilter {$MultipleInstances -eq 'IgnoreNew' -and $StartWhenAvailable -and $RestartCount -eq 3 -and $RestartInterval.TotalSeconds -eq 60 -and $ExecutionTimeLimit -eq [TimeSpan]::Zero}
            Assert-MockCalled New-ScheduledTaskAction -Times 1 -ParameterFilter {$Argument -like '*"-File"*"Run-Whisper.ps1"*' -or $Argument -like '*Run-Whisper.ps1*'}
        }
        It 'rejects sub-minute restart intervals and any count other than three' {
            Set-StrictMode -Version Latest
            foreach ($interval in @('PT0S','PT30S','PT59S')) {
                { Assert-GfrRestartPolicy ([pscustomobject]@{RestartInterval=$interval;RestartCount=3}) } | Should Throw
            }
            { Assert-GfrRestartPolicy ([pscustomobject]@{RestartInterval='PT1M';RestartCount=3}) } | Should Not Throw
            foreach ($count in @(0,2,4)) {
                { Assert-GfrRestartPolicy ([pscustomobject]@{RestartInterval='PT1M';RestartCount=$count}) } | Should Throw
            }
            Mock New-ScheduledTaskSettingsSet { [pscustomobject]@{RestartInterval='PT30S';RestartCount=3} }
            { New-GfrTaskDefinition (Get-GfrContext) } | Should Throw 'at least one minute'
            Assert-MockCalled New-ScheduledTask -Times 0 -Scope It
        }
    }
    Describe 'Registration failure configuration rollback' {
        Mock Get-GfrContext { $script:installContext }
        Mock Get-GfrOwnedTask { $null }
        Mock New-GfrConfiguration { [pscustomobject]@{Version=1;OwnerSid='fixture'} }
        Mock New-GfrTaskDefinition { [Microsoft.Management.Infrastructure.CimInstance]::new('MSFT_ScheduledTask') }
        Mock Assert-GfrPortAvailable {}
        Mock Register-ScheduledTask { throw 'Simulated registration failure' }
        It 'does not create configuration or directories during WhatIf' {
            $dir=Join-Path $TestDrive 'whatif-registration'
            $script:installContext=[pscustomobject]@{Data=$dir;Temp=(Join-Path $dir 'temp');Logs=(Join-Path $dir 'logs');Config=(Join-Path $dir 'runtime.json');TaskName='fixture';TaskPath='\'}
            Install-GfrWhisper -FFmpeg 'C:\fixture\ffmpeg.exe' -WhatIf
            (Test-Path -LiteralPath $dir) | Should Be $false
            Assert-MockCalled Register-ScheduledTask -Times 0 -Scope It
        }
        It 'removes only newly created configuration and safely retries after failure' {
            Set-StrictMode -Version Latest
            foreach ($variant in @('absent','existing','empty')) {
                $dir=Join-Path $TestDrive $variant
                $script:installContext=[pscustomobject]@{Data=$dir;Temp=(Join-Path $dir 'temp');Logs=(Join-Path $dir 'logs');Config=(Join-Path $dir 'runtime.json');TaskName='fixture';TaskPath='\'}
                $null=[IO.Directory]::CreateDirectory($script:installContext.Logs)
                $sentinel=Join-Path $script:installContext.Logs 'keep.log'
                [IO.File]::WriteAllText($sentinel,'preserve')
                $before=[byte[]]@(255,254,97,0,13,0,10,0)
                if ($variant -eq 'empty') { $before=[byte[]]@() }
                if ($variant -ne 'absent') { [IO.File]::WriteAllBytes($script:installContext.Config,$before) }
                Mock Register-ScheduledTask { throw 'Simulated registration failure' }
                for ($attempt=0; $attempt -lt 2; $attempt++) {
                    { Install-GfrWhisper -FFmpeg 'C:\fixture\ffmpeg.exe' -Confirm:$false } | Should Throw 'Simulated registration failure'
                    if ($variant -eq 'absent') { (Test-Path -LiteralPath $script:installContext.Config) | Should Be $false }
                    else { [Convert]::ToBase64String([IO.File]::ReadAllBytes($script:installContext.Config)) | Should Be ([Convert]::ToBase64String($before)) }
                    [IO.File]::ReadAllText($sentinel) | Should Be 'preserve'
                }
                Mock Register-ScheduledTask {}
                { Install-GfrWhisper -FFmpeg 'C:\fixture\ffmpeg.exe' -Confirm:$false } | Should Not Throw
                (Get-Content -LiteralPath $script:installContext.Config -Raw | ConvertFrom-Json).Version | Should Be 1
                [IO.File]::ReadAllText($sentinel) | Should Be 'preserve'
            }
            Assert-MockCalled Register-ScheduledTask -Times 9 -Scope It -ParameterFilter {$TaskName -eq 'fixture' -and $TaskPath -eq '\'}
        }
    }
    Describe 'Exact task targeting' {
        Mock Get-ScheduledTask { @() }
        It 'does not match other task names' { (Get-GfrOwnedTask (Get-GfrContext)) | Should BeNullOrEmpty }
        It 'refuses a same-name task with a foreign action' {
            Mock Get-ScheduledTask {
                $ctx=Get-GfrContext
                [pscustomobject]@{TaskName=$ctx.TaskName;TaskPath=$ctx.TaskPath;Description=$ctx.Description;Principal=[pscustomobject]@{UserId=$ctx.Sid;LogonType='Interactive';RunLevel='Limited'};Actions=@([pscustomobject]@{Execute=$ctx.Shell;WorkingDirectory=$ctx.Data;Arguments='foreign'})}
            }
            { Get-GfrOwnedTask (Get-GfrContext) } | Should Throw
        }
    }
    Describe 'Scheduler normalized principal ownership' {
        Mock ConvertTo-GfrAccountSid {
            if ($Account -in @(([Environment]::MachineName+'\User'),'DKFIRE-7090\User')) { return 'S-1-5-21-3926228357-1868843804-2423023326-1001' }
            if ($Account -in @('OTHERPC\User','OTHERDOMAIN\User')) { return 'S-1-5-21-111-222-333-1001' }
            throw 'Unresolvable fixture principal'
        }
        Mock Get-ScheduledTask { $script:principalTask }
        BeforeEach {
            $script:principalContext=Get-GfrContext
            $script:principalContext.Sid='S-1-5-21-3926228357-1868843804-2423023326-1001'
            $script:principalContext.TaskName='GFR-Whisper-'+$script:principalContext.Sid
            $script:principalContext.Description='Greenwich Fire Responder local Whisper v1; owner '+$script:principalContext.Sid
            $script:principalTask=[pscustomobject]@{TaskName=$script:principalContext.TaskName;TaskPath='\';State='Ready';Description=$script:principalContext.Description
                Principal=[pscustomobject]@{UserId='User';LogonType='Interactive';RunLevel='Limited'}
                Actions=@([pscustomobject]@{Execute=$script:principalContext.Shell;WorkingDirectory=$script:principalContext.Data;Arguments=(Get-GfrTaskArguments $script:principalContext)})}
        }
        It 'accepts the real normalized User principal and its qualified and SID forms' {
            Set-StrictMode -Version Latest
            foreach ($name in @('User','DKFIRE-7090\User','.\User',$script:principalContext.Sid)) {
                $script:principalTask.Principal.UserId=$name
                (Get-GfrOwnedTask $script:principalContext).State | Should Be 'Ready'
            }
            Assert-MockCalled ConvertTo-GfrAccountSid -ParameterFilter {$Account -eq ([Environment]::MachineName+'\User')} -Times 2 -Scope It
        }
        It 'rejects the same short username under another machine or domain' {
            foreach ($name in @('OTHERPC\User','OTHERDOMAIN\User','S-1-5-21-111-222-333-1001')) {
                $script:principalTask.Principal.UserId=$name
                { Get-GfrOwnedTask $script:principalContext } | Should Throw 'ownership mismatch'
            }
        }
        It 'fails closed for missing or unresolvable principals' {
            foreach ($name in @('','unknown-account')) {
                $script:principalTask.Principal.UserId=$name
                { Get-GfrOwnedTask $script:principalContext } | Should Throw 'could not be resolved'
            }
        }
        It 'still rejects altered path, privilege and action fields' {
            $script:principalTask.TaskPath='\Other\'
            { Get-GfrOwnedTask $script:principalContext } | Should Throw
            $script:principalTask.TaskPath='\'
            $script:principalTask.Principal.RunLevel='Highest'
            { Get-GfrOwnedTask $script:principalContext } | Should Throw
            $script:principalTask.Principal.RunLevel='Limited'
            $script:principalTask.Actions[0].WorkingDirectory='C:\foreign'
            { Get-GfrOwnedTask $script:principalContext } | Should Throw
        }
    }
    Describe 'Stop and uninstall mocks' {
        Mock Get-GfrOwnedTask { [pscustomobject]@{State='Running'} }
        Mock Stop-ScheduledTask {}
        Mock Wait-GfrTaskStopped {}
        Mock Unregister-ScheduledTask {}
        Mock Remove-GfrRuntimeData {}
        It 'stops only the exact SID-specific task and never a named process' {
            Stop-GfrWhisper -Confirm:$false
            Assert-MockCalled Stop-ScheduledTask -Times 1 -ParameterFilter {$TaskName -like 'GFR-Whisper-S-1-*' -and $TaskPath -eq '\'}
        }
        It 'uninstalls without cleanup by default' {
            Uninstall-GfrWhisper -Confirm:$false
            Assert-MockCalled Unregister-ScheduledTask -Times 1 -ParameterFilter {$TaskName -like 'GFR-Whisper-S-1-*' -and $TaskPath -eq '\'}
            Assert-MockCalled Remove-GfrRuntimeData -Times 0
        }
        It 'honors WhatIf before task mutations' {
            Uninstall-GfrWhisper -WhatIf
            Assert-MockCalled Unregister-ScheduledTask -Times 0 -Scope It
            Assert-MockCalled Stop-ScheduledTask -Times 0 -Scope It
        }
    }
    Describe 'Registration WhatIf and occupied port' {
        Mock Get-GfrOwnedTask { $null }
        Mock New-GfrConfiguration { [pscustomobject]@{Version=1} }
        Mock New-GfrTaskDefinition { [pscustomobject]@{} }
        Mock Assert-GfrPortAvailable {}
        Mock Register-ScheduledTask { throw 'Real registration must never occur in tests' }
        Mock Set-Content { throw 'Configuration must not be written in WhatIf' }
        It 'validates but does not register or write during WhatIf' {
            Install-GfrWhisper -Executable 'C:\fixture\whisper-server.exe' -Model 'C:\fixture\ggml-small.en.bin' -FFmpeg 'C:\fixture\ffmpeg.exe' -WhatIf
            Assert-MockCalled Assert-GfrPortAvailable -Times 1
            Assert-MockCalled Register-ScheduledTask -Times 0
            Assert-MockCalled Set-Content -Times 0
        }
        It 'refuses an occupied port before registration' {
            Mock Assert-GfrPortAvailable { throw 'Port 8001 is occupied' }
            { Install-GfrWhisper -FFmpeg 'C:\fixture\ffmpeg.exe' } | Should Throw
            Assert-MockCalled Register-ScheduledTask -Times 0
        }
    }
    Describe 'Start operation without a real task' {
        Mock Get-GfrOwnedTask { [pscustomobject]@{State='Ready'} }
        Mock Read-GfrConfiguration { [pscustomobject]@{} }
        Mock Assert-GfrPortAvailable {}
        Mock Start-ScheduledTask {}
        It 'starts only the owned task after validation' {
            Start-GfrWhisper -Confirm:$false
            Assert-MockCalled Read-GfrConfiguration -Times 1 -Scope It
            Assert-MockCalled Assert-GfrPortAvailable -Times 1 -Scope It
            Assert-MockCalled Start-ScheduledTask -Times 1 -Scope It -ParameterFilter {$TaskName -like 'GFR-Whisper-S-1-*' -and $TaskPath -eq '\'}
        }
        It 'does not start a duplicate running instance' {
            Mock Get-GfrOwnedTask { [pscustomobject]@{State='Running'} }
            Start-GfrWhisper
            Assert-MockCalled Start-ScheduledTask -Times 0 -Scope It
            Assert-MockCalled Assert-GfrPortAvailable -Times 0 -Scope It
        }
        It 'honors start WhatIf' {
            Mock Get-GfrOwnedTask { [pscustomobject]@{State='Ready'} }
            Start-GfrWhisper -WhatIf
            Assert-MockCalled Start-ScheduledTask -Times 0 -Scope It
        }
        It 'refuses missing tasks and occupied ports' {
            Mock Get-GfrOwnedTask { [pscustomobject]@{State='Ready'} }
            Mock Assert-GfrPortAvailable { throw 'occupied' }
            { Start-GfrWhisper } | Should Throw
            Mock Get-GfrOwnedTask {$null}
            { Start-GfrWhisper } | Should Throw
            Assert-MockCalled Start-ScheduledTask -Times 0 -Scope It
        }
    }
    Describe 'Task stop acknowledgement' {
        Mock Get-GfrOwnedTask { [pscustomobject]@{State='Ready'} }
        It 'acknowledges a stopped owned task without mutating it' {
            { Wait-GfrTaskStopped (Get-GfrContext) } | Should Not Throw
        }
    }
}

Describe 'Process identity and bounded logs' {
    It 'contains a finite fixture process and omits unrelated environment values' {
        $dir=Join-Path $TestDrive 'finite-runtime';$logs=Join-Path $dir 'logs'
        [IO.Directory]::CreateDirectory($logs) | Out-Null
        $exe=Join-Path $env:SystemRoot 'System32\cmd.exe'
        $old=$env:GFR_WHISPER_TEST_SECRET
        $env:GFR_WHISPER_TEST_SECRET='fixture-must-not-reach-child'
        try {
            $code=[Gfr.Whisper.RuntimeHost]::Run($exe,((ConvertTo-GfrArgument $exe)+' /d /c set'),$exe,$dir,$logs,
                (Join-Path $dir 'process.json'),('fixture-'+[Guid]::NewGuid()),65536,1)
            $code | Should Be 1
            $output=Get-Content -LiteralPath (Join-Path $logs 'stdout.log') -Raw
            $output.Contains('fixture-must-not-reach-child') | Should Be $false
            $output.Contains('TEMP='+$dir) | Should Be $true
            (Test-Path -LiteralPath (Join-Path $dir 'process.json')) | Should Be $true
        } finally {$env:GFR_WHISPER_TEST_SECRET=$old}
    }
    It 'rejects PID reuse or a different executable' {
        $start=[DateTime]::UtcNow
        $state=[pscustomobject]@{Pid=42;StartTicks=$start.Ticks}
        $process=[pscustomobject]@{Id=42;StartTime=$start;MainModule=[pscustomobject]@{FileName='C:\bin\whisper-server.exe'}}
        (Test-GfrProcessIdentity $state $process 'C:\bin\whisper-server.exe') | Should Be $true
        $state.StartTicks++
        (Test-GfrProcessIdentity $state $process 'C:\bin\whisper-server.exe') | Should Be $false
        $state.StartTicks=$start.Ticks
        (Test-GfrProcessIdentity $state $process 'C:\other\whisper-server.exe') | Should Be $false
    }
    It 'bounds current and archived logs even for a huge newline-free write' {
        $path=Join-Path $TestDrive 'stdout.log'
        $log=[Gfr.Whisper.RotatingLog]::new($path,16,2)
        try { $bytes=[Text.Encoding]::UTF8.GetBytes(('x'*101));$log.Write($bytes,$bytes.Length) } finally {$log.Dispose()}
        @((Get-ChildItem -LiteralPath $TestDrive -Filter 'stdout.log*')).Count | Should Be 3
        foreach($file in Get-ChildItem -LiteralPath $TestDrive -Filter 'stdout.log*') {($file.Length -le 16) | Should Be $true}
        (Get-Content -LiteralPath $path -Raw) | Should Be 'xxxxx'
    }
    It 'rotates an already full log at startup' {
        $path=Join-Path $TestDrive 'stderr.log';[IO.File]::WriteAllText($path,('x'*32))
        $log=[Gfr.Whisper.RotatingLog]::new($path,16,2);$log.Dispose()
        (Get-Item -LiteralPath $path).Length | Should Be 0
        (Test-Path -LiteralPath ($path+'.1')) | Should Be $true
        (Get-Item -LiteralPath ($path+'.1')).Length | Should Be 16
    }
    It 'enforces smaller archive count and size even with an empty current log' {
        $path=Join-Path $TestDrive 'retention.log'
        [IO.File]::WriteAllText($path,'')
        foreach($n in 1..5){[IO.File]::WriteAllText(($path+'.'+$n),('x'*32))}
        $log=[Gfr.Whisper.RotatingLog]::new($path,8,2);$log.Dispose()
        @(Get-ChildItem -LiteralPath $TestDrive -Filter 'retention.log*').Count | Should Be 3
        (Get-Item -LiteralPath ($path+'.1')).Length | Should Be 8
        (Get-Item -LiteralPath ($path+'.2')).Length | Should Be 8
    }
}

InModuleScope WhisperRuntime {
    Describe 'Cleanup safety and status' {
        It 'rejects any cleanup root other than the user-local GFR directory' {
            { Remove-GfrRuntimeData ([pscustomobject]@{Data='C:\Windows'}) -WhatIf } | Should Throw
        }
        It 'rejects unknown contents before cleanup and supports WhatIf' {
            $dir=Join-Path $TestDrive 'managed';[IO.Directory]::CreateDirectory($dir) | Out-Null
            Mock Get-GfrContext { [pscustomobject]@{Data=$dir} }
            Set-Content -LiteralPath (Join-Path $dir 'do-not-delete.bin') -Value 'fixture'
            { Remove-GfrRuntimeData ([pscustomobject]@{Data=$dir}) -WhatIf } | Should Throw
            (Test-Path -LiteralPath (Join-Path $dir 'do-not-delete.bin')) | Should Be $true
        }
        It 'reports unavailable task and HTTP health safely' {
            Mock Get-GfrContext { [pscustomobject]@{TaskName='fixture-task';State='C:\fixture\process.json'} }
            Mock Get-GfrOwnedTask {$null}
            Mock Test-GfrHealth {0}
            Mock Test-Path {$false}
            $status=Get-GfrWhisperStatus
            $status.TaskState | Should Be 'NotRegistered'
            $status.HTTPHealthy | Should Be $false
            $status.HTTPStatus | Should Be 0
            $status.ProcessState | Should Be 'NotRecorded'
        }
        It 'still reports HTTP health when Scheduler access is denied' {
            Mock Get-GfrContext { [pscustomobject]@{TaskName='fixture-task';State='C:\fixture\process.json'} }
            Mock Get-GfrOwnedTask {throw 'fixture-sensitive-error'}
            Mock Test-GfrHealth {200}
            Mock Test-Path {$false}
            $status=Get-GfrWhisperStatus
            $status.TaskState | Should Be 'UnavailableOrOwnershipMismatch'
            $status.HTTPHealthy | Should Be $true
            ($status | ConvertTo-Json).Contains('fixture-sensitive-error') | Should Be $false
        }
    }
    Describe 'Cleanup preview and live-task guard' {
        It 'keeps allowed files during cleanup WhatIf and removes only the fixture when approved' {
            $dir=Join-Path $TestDrive 'cleanup-fixture';$logs=Join-Path $dir 'logs'
            [IO.Directory]::CreateDirectory($logs) | Out-Null
            [IO.File]::WriteAllText((Join-Path $logs 'stdout.log'),'fixture')
            Mock Get-GfrContext { [pscustomobject]@{Data=$dir} }
            Mock Get-GfrOwnedTask {$null}
            Remove-GfrRuntimeData ([pscustomobject]@{Data=$dir}) -WhatIf
            (Test-Path -LiteralPath (Join-Path $logs 'stdout.log')) | Should Be $true
            Remove-GfrRuntimeData ([pscustomobject]@{Data=$dir}) -Confirm:$false
            (Test-Path -LiteralPath $dir) | Should Be $false
        }
        It 'refuses cleanup while the task is still registered' {
            $dir=Join-Path $TestDrive 'registered-fixture'
            Mock Get-GfrContext { [pscustomobject]@{Data=$dir} }
            Mock Get-GfrOwnedTask { [pscustomobject]@{State='Running'} }
            { Remove-GfrRuntimeData ([pscustomobject]@{Data=$dir}) -Confirm:$false } | Should Throw
        }
    }
}

Describe 'Bounded loopback health checks' {
    It 'rejects a genuinely occupied test port without stopping its owner' {
        $listener=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0);$listener.Start();$port=$listener.LocalEndpoint.Port
        try {{ Assert-GfrPortAvailable -Port $port } | Should Throw} finally {$listener.Stop()}
        { Assert-GfrPortAvailable -Port $port } | Should Not Throw
    }
    It 'reports HTTP 200' {
        $server=[GfrHealthFixture]::new(200,0)
        try {(Test-GfrHealth -Port $server.Port) | Should Be 200} finally {$server.Dispose()}
    }
    It 'reports non-200 without following redirects' {
        $server=[GfrHealthFixture]::new(302,0)
        try {(Test-GfrHealth -Port $server.Port) | Should Be 302} finally {$server.Dispose()}
    }
    It 'bounds a slow health endpoint' {
        $server=[GfrHealthFixture]::new(200,300)
        try {(Test-GfrHealth -Port $server.Port -TimeoutMs 50) | Should Be 0} finally {$server.Dispose()}
    }
    It 'reports an unavailable endpoint without raw errors' {
        $listener=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0);$listener.Start();$port=$listener.LocalEndpoint.Port;$listener.Stop()
        (Test-GfrHealth -Port $port) | Should Be 0
    }
}
