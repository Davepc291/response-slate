# Windows production Angular build/deploy

See [docs/windows-production-deployment.md](../../../docs/windows-production-deployment.md)
for the full design.

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Build-Frontend.ps1 -WhatIf
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\Build-Frontend.ps1
```

Builds `web` in production configuration and copies
`web/dist/web/browser` to `%LOCALAPPDATA%\GreenwichFireResponder\Frontend`
by default — the path `ops/windows/caddy/Run-Caddy.ps1` points Caddy's
`file_server` at.
