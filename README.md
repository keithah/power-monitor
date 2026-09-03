# power-monitor

Collect solar generation, whole-home usage, and utility meter data into SQLite.

This is a **small HTTP collector with a read-only CLI and native Streamable HTTP MCP endpoint**, not a Home Assistant add-on.

```text
Enphase  → generation
Emporia  → circuit / whole-home kWh
Opower   → utility import/export
           ↓
        SQLite
           ↓
   GET /api/report
   POST /mcp  ← native MCP Streamable HTTP
```

## Why this shape

| Surface | Use it for |
|---|---|
| **HTTP service (this repo)** | Always-on collection, health checks, MFA callbacks |
| **CLI (`cli.py`)** | One-shot `collect` / `status` / `devices` / `usage` / `report` against a running service |
| **MCP** | Later, as a thin read-only wrapper over `/api/report` — not the core |

MCP is a bad primary interface here: collection is long-running, PG&E MFA is interactive, and agents should query stored readings rather than log into utilities. Keep credentials on the collector host.

## Go rewrite (review / staging)

The generated Go replacement is tracked in [`go/`](go/). It provides the same
provider-oriented collector model, a shared CLI and native Streamable HTTP MCP,
explicit multi-setup rollups, and interval-safe summaries. It remains staged:
the Python service above is the active production collector until Go provider
acceptance—including the standalone PG&E MFA-start flow—has completed.

### REST compatibility staging

`power-monitor-pp-api` provides the existing `/health` and `/api/*` REST surface
on a separate loopback address (`127.0.0.1:8097` by default). It is intentionally
not exposed on port 8094 and does not replace the Python container. The staging
unit is [`go/deploy/systemd/power-monitor-go-api.service`](go/deploy/systemd/power-monitor-go-api.service).

Before any cutover, run it beside Python and compare status, devices, readings,
reports, collection outcomes, and a session renewal after PG&E MFA. The Go
collector's schedule must be deliberately aligned with Python's 900-second
interval; the current hourly timer is not a production-equivalence claim.

Run its independent checks from `go/`:

```bash
go test ./...
go vet ./...
go build ./...
```

## Providers

- **Enphase** — Enlighten username/password. Discovers sites and stores daily production. Optional LAN Envoy scan if cloud listing is empty.
- **Emporia Vue** — Cognito login via `pyemvue`, then the web app's v1 API (`/v1/customers/devices`, `/v1/customers/devices/usages`). Stores `Mains` plus branch channels on hour buckets. Skips computed `TotalUsage` / `Balance`.
- **Utility (Opower)** — Works with Opower utilities. PG&E MFA is first-class: start / select / verify, then persist login state.

## Quick start

1. Copy `.env.example` to `.env` and fill credentials.
2. `docker compose up -d --build`
3. Confirm: `python3 cli.py --url http://127.0.0.1:8094 status`
4. Collect: `python3 cli.py collect`
5. Readings: `python3 cli.py report --source emporia`

The container also polls on `COLLECT_INTERVAL_SECONDS` (default 900). Set `0` to collect only on demand.

## MCP over Streamable HTTP

The service exposes a stateless native MCP endpoint at `/mcp`; it does not spawn a
stdio subprocess or bridge to one. Configure an MCP client with:

```text
http://127.0.0.1:8080/mcp
```

The endpoint exposes read-only `status`, `devices`, `usage`, and `report` tools.
For a network-accessible deployment, set `MCP_AUTH_TOKEN` and send it as a
Bearer token. Keep the service behind TLS and an authenticated reverse proxy when
it is not loopback-only.

## HTTP API

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | liveness |
| GET | `/api/status` | which providers have credentials |
| GET | `/api/devices` | configured Emporia/Enphase devices |
| GET | `/api/usage` | stored normalized usage (`?provider=` / `?limit=`) |
| POST | `/api/collect` | one collection cycle |
| GET | `/api/report` | last 500 rows |
| GET | `/api/enphase/systems` | discovered Enphase sites |
| POST | `/api/pge/mfa/start` | begin utility MFA |
| POST | `/api/pge/mfa/select` | `{"option":"Email"}` or `"Phone"` |
| POST | `/api/pge/mfa/verify` | `{"code":"..."}` — writes `/data/pge-login.json` |

## Environment

See `.env.example`. Secrets stay in env / Docker env_file. Nothing in this repo is site-specific.

## Tests

```bash
pip install -r requirements.txt pytest
pytest -q
```

## Notes

- Emporia `HOUR` usage is cumulative inside the current UTC hour. Later polls overwrite the same `(ts, source, channel)` row.
- Enphase cloud rows currently use collection time, not each 15-minute interval timestamp.
- `/api/report` includes raw provider payloads and can be large. Prefer `cli.py report`.
