# PostgreSQL development

This Compose project provides PostgreSQL 16 for local development. It does not
connect the Go backend to the database. Run the commands below in PowerShell
from the repository root, with Docker Desktop configured for Linux containers.

## Configure

Create your local configuration only if `.env` does not already exist:

```powershell
if (-not (Test-Path -LiteralPath .env)) {
    Copy-Item -LiteralPath .env.example -Destination .env
}
```

Edit `.env` and replace the example password with a unique local password before
starting PostgreSQL. Keep this file private; Git ignores it. The example values
are placeholders, not production credentials.

| Variable | Purpose |
| --- | --- |
| `POSTGRES_DB` | Initial database name (required) |
| `POSTGRES_USER` | Initial database administrator username (required) |
| `POSTGRES_PASSWORD` | Initial password (required and non-empty) |
| `POSTGRES_PORT` | Windows host port; defaults to `5432` |

PostgreSQL binds to `127.0.0.1` on the host. If port 5432 is already occupied,
change `POSTGRES_PORT`, for example to `5433`. The container port stays 5432.
Variables already set in your shell take precedence over `.env` values.

Database initialization settings apply when the data volume is first initialized.
Editing `.env` later does not change an existing database's name, user, or password.

## Validate without starting containers

Validate the checked-in template:

```powershell
docker compose --env-file .env.example config
```

For your actual local configuration, suppress the rendered values so the password
is not printed:

```powershell
docker compose --env-file .env config --quiet
```

Compose rejects missing or empty required values using its
[required-variable interpolation syntax](https://docs.docker.com/reference/compose-file/interpolation/).

## Start

```powershell
docker compose --env-file .env up -d postgres
```

This may download `postgres:16-alpine` and creates a project-scoped Docker volume
for `postgres_data`, named `greenwich-fire-responder_postgres_data` with the
default project name. That volume stores the database across container restarts.

## Status and logs

```powershell
docker compose --env-file .env ps postgres
docker compose --env-file .env logs --tail 100 -f postgres
```

Wait for the service to report `healthy` before connecting. The health check uses
`pg_isready` to check whether PostgreSQL accepts connections. Press Ctrl+C to
stop following logs; the database keeps running.

## Connect

In a PostgreSQL client on Windows, use:

- Host: `127.0.0.1`
- Port: your `POSTGRES_PORT` value, or `5432` by default
- Database, username, and password: the values configured in your local `.env`

With `psql` installed on Windows, the template database and username can be used
as follows; adjust them and the port if you changed `.env`. `-W` prompts for the
password rather than putting it in the command line:

```powershell
psql -h 127.0.0.1 -p 5432 -U gfr_dev -d greenwich_fire_responder_dev -W
```

Alternatively, use the container's client, with the same adjustments:

```powershell
docker compose --env-file .env exec postgres psql -h 127.0.0.1 -U gfr_dev -d greenwich_fire_responder_dev -W
```

Type `\q` to leave `psql`.

## Stop

```powershell
docker compose --env-file .env stop postgres
```

This preserves the container and database volume. Run the start command again
to resume. The service uses `restart: unless-stopped`.
