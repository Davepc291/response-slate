# Greenwich Fire Responder V3 backend

HTTP API foundation using the Go standard library and `github.com/jackc/pgx/v5/pgxpool`.
The temporary
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
to finish before closing remaining connections. The PostgreSQL pool is closed
after HTTP shutdown, including on server startup failure.

## PostgreSQL configuration

The API reads process environment variables only. It does **not** automatically
load `.env`; Docker Compose's `.env` settings are not passed to a Go process
running on Windows. Use the local PostgreSQL setup in `../docs/development.md`
and the separate migration commands in `../database/README.md`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `GFR_HTTP_ADDR` | `127.0.0.1:8080` | API listening address |
| `GFR_DATABASE_URL` | Empty | PostgreSQL connection string |
| `GFR_DATABASE_REQUIRED` | `false` | Require a successful startup database ping |

An empty URL leaves the database unconfigured. Optional mode keeps the API running
when PostgreSQL is unavailable. A valid pool is retained and readiness can recover
when PostgreSQL becomes reachable. An invalid connection string in optional mode
leaves readiness unavailable until configuration is corrected and the API restarted.

When `GFR_DATABASE_REQUIRED=true`, a missing URL, invalid connection configuration,
or failed startup ping stops startup with a safe error. Invalid boolean values also
fail configuration validation. A later database outage makes readiness return 503;
it does not stop the API or affect liveness.

Connection attempts are capped at 5 seconds; startup and readiness pings have a
2-second deadline, including pool acquisition. Database errors and connection
strings are never logged or included in readiness responses. Pool construction is
followed by an explicit ping because [pgxpool creates connections lazily](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#hdr-Creating_a_Pool).

From `backend/`, set the variables explicitly in PowerShell. This URL contains only
a placeholder; substitute your local credentials and configured host port privately
and percent-encode special characters in URL credentials:

```powershell
$env:GFR_DATABASE_URL = "postgres://gfr_dev:replace_me@127.0.0.1:5432/greenwich_fire_responder_dev?sslmode=disable"
$env:GFR_DATABASE_REQUIRED = "true"
go run ./cmd/api
```

`sslmode=disable` is for the local loopback development database. Use appropriate
TLS settings for remote connections. Never commit credentials or recordings.
The API performs connectivity checks only; it does not run migrations or write
radio, incident, or board state. No Docker API service is included.

## Health endpoint

`GET http://127.0.0.1:8080/api/health` returns HTTP 200 with
`Content-Type: application/json` and the following body:

```json
{"status":"ok","service":"greenwich-fire-responder-api"}
```

This endpoint reports that the API is running; it does not check external services.

## Readiness endpoint

`GET http://127.0.0.1:8080/api/ready` pings PostgreSQL. It returns HTTP 200 when
reachable:

```json
{"status":"ready","database":"ok"}
```

When unavailable or unconfigured, it returns HTTP 503 with the same safe body for
both conditions:

```json
{"status":"not_ready","database":"unavailable"}
```

Both responses use `Content-Type: application/json` and `Cache-Control: no-store`.
Readiness checks connectivity, not migration version or application data.

## Format, vet, and test

Run from `backend/`:

```sh
gofmt -w cmd/api internal/config internal/database internal/httpapi
go vet ./...
go test ./...
```

Tests cover liveness independence, ready/unavailable/unconfigured responses,
configuration defaults and validation, explicit environment-only loading, optional
recovery, required startup checks, safe errors, deadlines, and pool cleanup. They
use small fake interfaces and do not require a running PostgreSQL database.

## Layout

- `cmd/api/main.go`: configuration, HTTP server lifecycle, and graceful shutdown.
- `internal/config/`: environment configuration and validation tests.
- `internal/database/`: pgxpool lifecycle, safe ping checks, and tests.
- `internal/httpapi/`: routing, liveness/readiness handlers, and HTTP tests.

The Angular application remains in `../web/` and runs separately.
