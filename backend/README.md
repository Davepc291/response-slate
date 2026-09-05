# Greenwich Fire Responder V3 backend

Initial HTTP API foundation using only the Go standard library. The temporary
module name is `greenwich-fire-responder/backend`. Requires Go 1.26 or later.

## Run

From the repository root:

```sh
cd backend
go run ./cmd/api
```

The server listens on `127.0.0.1:8080` by default, accepting connections only
from this computer. Set `GFR_HTTP_ADDR` to override the listening address.
For example, in PowerShell, from `backend/`:

```powershell
$env:GFR_HTTP_ADDR = "127.0.0.1:9090"
go run ./cmd/api
```

For future Docker or production deployments, set `GFR_HTTP_ADDR` to `:8080`
to listen on all available interfaces.

An unset or empty `GFR_HTTP_ADDR` uses `127.0.0.1:8080`. Press Ctrl+C to shut down.
The server also handles SIGTERM and allows up to 10 seconds for active requests
to finish before closing remaining connections.

## Health endpoint

`GET http://127.0.0.1:8080/api/health` returns HTTP 200 with
`Content-Type: application/json` and the following body:

```json
{"status":"ok","service":"greenwich-fire-responder-api"}
```

This endpoint reports that the API is running; it does not check external services.

## Format, vet, and test

Run from `backend/`:

```sh
gofmt -w cmd/api internal/httpapi
go vet ./...
go test ./...
```

Tests cover the health response, unsupported POST requests, and unknown routes.

## Layout

- `cmd/api/main.go`: configuration, HTTP server lifecycle, and graceful shutdown.
- `internal/httpapi/`: routing, health handler, and HTTP tests.

The Angular application remains in `../web/` and runs separately.
