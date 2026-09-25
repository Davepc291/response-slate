# Windows production Caddy runtime

See [docs/windows-production-deployment.md](../../../docs/windows-production-deployment.md)
for the full design, commands, and what has and hasn't been done.

Caddy itself is **not installed** by anything here. `Install-Caddy.ps1`
validates a real `caddy.exe` before registering its task, so running these
scripts before Caddy is installed fails closed.

Quick reference:

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Install-Caddy.ps1 -WhatIf
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Install-Caddy.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Start-Caddy.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Status-Caddy.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Stop-Caddy.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Uninstall-Caddy.ps1 -WhatIf
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Test-Caddy.ps1
```

Review `Caddyfile.production` before ever installing against it. Automatic
HTTPS for `app.gfrapp.com` additionally needs DNS pointed at this host and
80/443 forwarded to it — neither is done here.
