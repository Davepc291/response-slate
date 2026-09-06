# Windows local Whisper runtime

These scripts register an **optional**, per-user Task Scheduler task for the
already installed whisper.cpp server. They do not download software, alter models,
configure the Go API, or manage any other Whisper service. Registration does not
start the task. No NSSM, credentials, or third-party service wrapper is used.

## Validated runtime and defaults

- Executable: `$env:USERPROFILE\RadioTools\whisper-cpp\Release\whisper-server.exe`.
- Model: `$env:USERPROFILE\RadioTools\whisper-cpp\models\ggml-small.en.bin`.
- Required SHA-1: `DB8A495A91D927739E50B3FC1CC4C6B8F6C2D022`.
- FFmpeg: resolve `ffmpeg.exe` from PATH at registration; retain its absolute path.
- Listen only on `127.0.0.1:8001`; inference path `/v1/audio/transcriptions`.
- `--language en --no-gpu --threads 8 --processors 1 --convert`.
- Dedicated data directory:
  `[Environment]::GetFolderPath('LocalApplicationData')\GreenwichFireResponder\Whisper`.
  It contains `runtime.json`, `process.json`, `temp\`, and `logs\`.

Executable/model/FFmpeg paths must be absolute local drive paths. Spaces and
apostrophes are supported; UNC paths, relative paths, traversal, alternate streams,
reparse points and shell metacharacters are rejected. Inputs must reside outside
the managed data directory. Executables must exist, have the expected filenames
and Windows image signatures; the model must match the exact checksum. Keep the
executable's DLLs beside it. Matching the model hash verifies identity, not a
trusted software publisher or executable signature.

The installed executable's `--help` was inspected for the required flags. Its
dependencies and a future build's compatibility still need a post-installation
health/inference check. The earlier 21-recording local benchmark averaged about
3.38 seconds per request; that is an observation, not a runtime performance promise.

## Manual installation — later, not during development of these scripts

Use **Windows PowerShell 5.1**, opened as Administrator **under the same Windows
account that will use GFR**. Do not elevate with a different account: the current
SID, profile, model paths and logon trigger would belong to that account instead.
The scheduled runtime itself uses `Limited` privilege and an interactive token;
no password is stored. Some Windows policies permit registration without elevation,
but the documented deployment command uses an administrator shell.

1. Keep this checkout at a stable path; the task references its runtime wrapper
   and C# helper. Move it only after uninstalling/re-registering the task.
2. Review the scripts. If Windows marked a downloaded checkout as blocked, unblock
   the reviewed files through Windows file properties. Do not disable organization
   execution policy. Commands below set `RemoteSigned` for the child process only;
   they do not change machine/user execution policy or bypass Group Policy.
3. Close the **existing manually started local server using Ctrl+C in its own
   terminal**, after coordinating with any clients. Do not kill by executable
   name. The scripts refuse an occupied port and never take it over.
4. From the repository root, preview registration:

   ```powershell
   powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Install-Whisper.ps1 -WhatIf
   ```

   WhatIf still performs read-only validation, including checksum and port checks.
   It does not create runtime files, register a task, or start a server.
5. Register, then start explicitly:

   ```powershell
   powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Install-Whisper.ps1
   powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Start-Whisper.ps1
   powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Status-Whisper.ps1
   ```

   For nondefault locations, pass `-Executable`, `-Model`, and `-FFmpeg` as quoted
   absolute paths to the install command. Optional limits are `-LogMaxMB 10`
   (1–64) and `-LogArchives 3` (1–10). On registration failure, a newly created runtime.json is removed; an existing
   runtime.json is restored byte-for-byte, including an empty file. Rollback only
   touches that configuration. Directories, logs, models, binaries and recordings
   are retained. An exclusive file handle prevents concurrent configuration writes.
   Correct the error and rerun the same install command; configuration left by an
   older failed installer needs no manual deletion. Inspect status before retrying.
   This rollback covers caught failures, not abrupt process termination or power
   loss; Task Scheduler and the filesystem do not share an atomic transaction. Existing
   tasks are never overwritten, including a foreign task with the same name.
6. Confirm the exact task is running, the recorded process matches executable and
   creation time, port 8001 listens on loopback only, and HTTP status is 200.
   Root GET proves HTTP availability; it does not prove transcription accuracy or
   successful inference. Perform a separately authorized inference smoke test.
   Status reports inaccessible/mismatched Scheduler information explicitly and
   continues the independent TCP/HTTP checks; HTTP 200 alone does not establish
   that the listener belongs to the managed task.
7. Opt in to local transcription in the **Go API process environment**:

   ```powershell
   $env:GFR_TRANSCRIPTION_ENABLED = 'true'
   $env:GFR_TRANSCRIPTION_BASE_URL = 'http://127.0.0.1:8001'
   $env:GFR_TRANSCRIPTION_MODEL = 'small.en'
   $env:GFR_TRANSCRIPTION_LANGUAGE = 'en'
   ```

   Configure PostgreSQL/recordings separately. The API does not load `.env`.
   API health/readiness behavior is unchanged. This runtime adds no classification,
   incident, unit-status, WebSocket, or CAD authority.

## Scheduling, stop, and rollback

Task name is `GFR-Whisper-<current-user-SID>` in the root task folder. It starts at
that user's logon, uses `IgnoreNew` to prevent duplicate task instances, starts
when available, and allows at most three Scheduler restart attempts separated by
one minute after failure (the Windows-supported minimum). There is no execution-time limit. Battery operation is
allowed. It requires the user to be logged on and is not a boot-time Windows service.
Task Scheduler's restart bookkeeping is platform-managed; inspect Last Run Result
in Task Scheduler when all restart attempts are exhausted, correct the issue, and
start explicitly. A live-but-unhealthy process is reported by status, not endlessly
restarted by an application health loop.

The hidden wrapper owns a Windows Job Object. Whisper is created suspended,
assigned to that job, and then resumed; conversion children inherit containment.
Stopping the exact task ends its wrapper and closes the job, terminating its owned
processes. There is no process-name kill, PID-only kill, or port-owner kill. Stop
can interrupt an inference; the Go API handles the failed request through its own
bounded retry policy. The wrapper also uses a session mutex; the task policy and
exclusive port validation prevent duplicate managed listeners.

Stop and roll back from the same account/repository:

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Stop-Whisper.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Uninstall-Whisper.ps1 -WhatIf
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Uninstall-Whisper.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Status-Whisper.ps1
```

Uninstall stops/removes only the task whose SID, description, executable and exact
action match this checkout. It retains models, binaries, recordings, repository
files, runtime configuration, temporary data, and logs. Restore the desired Go API
provider setting explicitly, or set `GFR_TRANSCRIPTION_ENABLED=false`.

Optional **separate, destructive cleanup** of the managed data directory only:

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Uninstall-Whisper.ps1 -CleanupRuntimeData -WhatIf
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Uninstall-Whisper.ps1 -CleanupRuntimeData
```

Cleanup rejects a different root, linked descendants, or unknown top-level files.
It also refuses real cleanup while the task remains registered. Stop waits up to
10 seconds for Scheduler to acknowledge termination before uninstall proceeds;
declining uninstall confirmation prevents cleanup as well.
It does not touch external model/binary/recording paths. Do not put unrelated files
in this dedicated data directory. Runtime temporary files are retained until this
explicit cleanup; whisper.cpp may remove its own conversion files during normal use.

## Logs and security limits

Stdout and stderr are streamed separately as raw bytes. Each has a 10 MiB current
log plus three archives by default: **at most 80 MiB combined**, excluding filesystem
metadata. Rotation also handles very large writes without newlines. Lowering the
limit truncates oversized retained files and removes archives beyond the new
retention count, including when the current log is empty. A forced task stop may lose output still
in the process/pipe buffers; rotation can split a UTF-8 sequence across files.
These are bounded diagnostic logs, not an immutable transcript audit.

Logs may contain radio transcripts and request details from whisper.cpp. Treat them
and conversion temp files as sensitive; never commit them. The child environment
contains only Windows runtime paths, the validated FFmpeg/executable directories,
the dedicated TEMP/TMP path, and the color-control flag. Database settings, bearer
tokens, and the parent environment are not passed through. Status prints no
environment, configuration contents, response body, or raw exception details.

Loopback binding prevents LAN exposure but is not authentication against other
local users/processes. This setup does not harden or patch whisper.cpp's HTTP API,
isolate it from the current user's files, or administer the Linux service. Path
checks assume the current user does not maliciously replace directories while an
operation is in progress. Keep models/binaries and this checkout trusted.

## Tests without administrator access or registration

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\ops\windows\whisper\Test-Whisper.ps1
```

Requires the built-in ScheduledTasks module and Pester 3.4 or later. The runner
parses all PowerShell sources, checks prohibited command patterns, compiles the
C# helper, and runs Pester tests with task cmdlets mocked. Tests use temporary
files, ephemeral loopback health listeners, and a finite job-contained command
fixture; they never register a task, start Whisper, or require elevation.
Actual logon, failure-restart, and real-task stop behavior require a later manual
installation test and are deliberately not exercised by this assignment.
