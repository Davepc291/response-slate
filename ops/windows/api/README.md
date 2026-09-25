# Windows production Go API runtime

See [docs/windows-production-deployment.md](../../../docs/windows-production-deployment.md)
for the full design, commands, and what has and hasn't been done.

Quick reference:

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Build-Api.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Install-Api.ps1 -WhatIf
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Install-Api.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Start-Api.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Status-Api.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Stop-Api.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Uninstall-Api.ps1 -WhatIf
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Test-Api.ps1
```

`Start-Api.ps1` refuses to run until
`%LOCALAPPDATA%\GreenwichFireResponder\Api\api.env` exists with real
production values (see `.env.production.example` at the repo root) — no
secret file is created by this runtime.
