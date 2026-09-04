---
name: pp-power-monitor
description: "Printing Press CLI for Power Monitor. PG&E/Opower, Enphase, Emporia electricity monitoring"
author: "keithah"
license: "Apache-2.0"
argument-hint: "<command> [args] | install cli|mcp"
allowed-tools: "Read Bash"
metadata:
  openclaw:
    requires:
      bins:
        - power-monitor-pp-cli
    install:
      - kind: go
        bins: [power-monitor-pp-cli]
        module: github.com/mvanhorn/printing-press-library/library/monitoring/power-monitor/cmd/power-monitor-pp-cli
---

# Power Monitor — Printing Press CLI

## Prerequisites: Install the CLI

This staged CLI is not yet published as an audited immutable Printing Press
release. Do not substitute a live catalog lookup, `@latest`, or an arbitrary
source revision. Before invoking this skill outside this repository, obtain a
published release record that names both the package version and the immutable
source commit, then verify `power-monitor-pp-cli --version` from that release.

Until then, use only an audited checkout of this repository and its local build
and test workflow; do not present a registry install command as available.

PG&E/Opower, Enphase, Emporia electricity monitoring

## HTTP Transport

This CLI uses Chrome-compatible HTTP transport for browser-facing endpoints. It does not require a resident browser process for normal API calls.

## Command Reference

**collect** — Manage collect

- `power-monitor-pp-cli collect` — Collect

**status** — Manage status

- `power-monitor-pp-cli status` — Status


### Auth Setup

No authentication required.

Run `power-monitor-pp-cli doctor` to verify setup.

## Agent Mode


- **Pipeable** — JSON on stdout, errors on stderr

  ```bash
  ```
- **Previewable** — `--dry-run` shows the request without sending
- **Non-interactive** — never prompts, every input is a flag
- **Explicit retries** — use `--idempotent` only when an already-existing create should count as success

## Paths and state

Agents should treat the CLI's path resolver as part of the runtime contract:

- Use `--home <dir>` for one invocation, or set `POWER_MONITOR_HOME=<dir>` to relocate all four path kinds under one root.
- Use per-kind env vars only when a specific kind must diverge: `POWER_MONITOR_CONFIG_DIR`, `POWER_MONITOR_DATA_DIR`, `POWER_MONITOR_STATE_DIR`, `POWER_MONITOR_CACHE_DIR`.
- Resolution order is per-kind env var, `--home`, `POWER_MONITOR_HOME`, XDG (`XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`), then platform defaults.
- `config` contains settings like `config.toml` and profiles. `data` contains `credentials.toml`, `data.db`, cookies, and auth sidecars. `state` contains persisted queries, jobs, and `teach.log`. `cache` contains regenerable HTTP/cache files.
- Stored secrets live in `credentials.toml` under the data dir. Existing legacy `config.toml` secrets are read for compatibility and leave `config.toml` on the first auth write.
- Run `power-monitor-pp-cli doctor --fail-on warn` to surface path and credential-location warnings. `agent-context` exposes a schema v4 `paths` block for agents that need the resolved dirs.
- For MCP, pass relocation through the MCP host config. The MCP binary does not inherit CLI flags:

  ```json
  {
    "mcpServers": {
      "power-monitor": {
        "command": "power-monitor-pp-mcp",
        "env": {
          "POWER_MONITOR_HOME": "/srv/power-monitor"
        }
      }
    }
  }
  ```

Fleet precedence: an inherited per-kind env var overrides an explicit `--home` for that kind. Use `POWER_MONITOR_HOME` or per-kind vars as durable fleet levers, and use `--home` only for a single invocation. Relocation is not reversible by unsetting env vars; move files manually before clearing `POWER_MONITOR_HOME`, or `doctor` will not find credentials left under the former root.

## Power Monitor commands

```bash
power-monitor-pp-cli status
power-monitor-pp-cli setup list
power-monitor-pp-cli setup show <name>
power-monitor-pp-cli device list
power-monitor-pp-cli collect
power-monitor-pp-cli usage
power-monitor-pp-cli aggregate <rollup>
power-monitor-pp-cli report
power-monitor-pp-cli config validate
power-monitor-pp-cli config show <name>
```

Use only flags shown by the installed binary's help.

## Providers and rollups

Enphase supports multiple named systems. Emporia supports multiple devices and branch/subpanel channels. PG&E/Opower uses a portal session flow and reports `mfa_required` when interactive MFA continuation is needed; the client does not fabricate MFA state or persist raw credentials.

Rollups are explicit configuration. Parent mains plus contained child panels are rejected by default to prevent double counting. Use an explicit override only for known non-overlapping measurements.

## MCP

The companion `power-monitor-pp-mcp` binary serves native stateless Streamable HTTP MCP. Configure its address and bearer token through environment variables; never include token values in documents.
