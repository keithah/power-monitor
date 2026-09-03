# Power Monitor

Generated Printing Press Go project with shared Cobra CLI and native Streamable HTTP MCP.

Configuration is JSON with public topology and `credential_env` references only. Copy
`config.example.json`; set `POWER_MONITOR_CONFIG` and provider environment variables at
runtime. No credentials are stored or printed.

```sh
go run ./cmd/power-monitor-pp-cli config validate
go run ./cmd/power-monitor-pp-cli setup list
go run ./cmd/power-monitor-pp-mcp
```

Rollups use qualified `setup:channel` inputs. A parent mains plus contained child is
rejected unless the rollup sets `override: true`. MCP binds loopback by default and
requires `POWER_MONITOR_MCP_TOKEN` whenever a token is configured.

## PG&E / Opower MFA

The PG&E client follows the Salesforce Aura login/session-cookie flow, discovers
accounts through the Opower customer endpoint, and reads interval usage through
DataBrowser-v1. When login returns `verifymfa :`, use `pge mfa-start`,
`pge mfa-select --option Email|Phone`, and `pge mfa-verify --code CODE`.
MFA options are masked and codes, cookies, and tokens are never returned. The
verified wrapper state is persisted at `POWER_MONITOR_PGE_LOGIN_PATH` (or
`/data/pge-login.json`) with mode 0600, and collection resumes it when usable.

## Interval-safe reporting

`summary --period day|week|month|year` exposes provider measurements and their
coverage; it does **not** infer a household energy balance. `balance_available`
remains false unless matching, complete windows exist for every source.

- Enphase `/today` values are current-day generation **snapshots**, not completed
  historical intervals.
- Emporia `HOUR` values are stored with explicit hour start/end timestamps.
  Only completed intervals contribute to `emporia_completed_consumption_kwh`;
  mains are included, while branch and subpanel channels are broken out separately.
- PG&E/Opower values retain their signed provider value as `pge_net_energy_kwh`.
  They are not relabeled as consumption; each discovered account is stored separately
  with its source window.
- Legacy rows without an upstream interval are retained for audit/usage queries but
  excluded from summary totals.

For reliable historical Emporia coverage, run `collect` hourly. The staged deployment
uses a persistent systemd user timer with a short randomized delay; production should
use an equivalent scheduler only after live acceptance and publication review.
