---
name: gate
description: Drive the gate local HTTPS reverse proxy and port registry from the command line. Use when a repository has a gate.toml and you need to map domains to local dev servers over trusted HTTPS, allocate or look up ports without hardcoding them, start a dev server on its assigned port, or inspect the machine-wide registry. Commands that document --json emit stable, parseable output.
---

# gate

`gate` maps local domains to dev servers over trusted HTTPS and manages a
machine-wide port registry. Commands write data to stdout and diagnostics to
stderr. Commands marked `--json` emit a single JSON value on success; JSON-mode
errors use a stderr envelope. `gate doctor --json` emits its issue report on
stdout even when issues make it exit non-zero.

## When to use

- A project has a `gate.toml` declaring services (`domain` → optional `port`).
- You need a stable, non-conflicting port for a dev server.
- You need to bring routes up/down or check what is mapped.

## Binary resolution

Agent sessions may not have `$HOME/.local/bin` in `PATH`. Before running `gate`,
resolve the executable once and use `"$GATE_BIN"` for later commands:

```bash
if command -v gate >/dev/null 2>&1; then
  GATE_BIN="$(command -v gate)"
elif [ -x "$HOME/.local/bin/gate" ]; then
  GATE_BIN="$HOME/.local/bin/gate"
else
  echo "gate not found; install it or report the missing binary." >&2
  exit 1
fi
```

## Core commands

| command | purpose |
| --- | --- |
| `gate init [--json] [--name name] [--force] [-y\|--yes]` | scaffold a starter `gate.toml` |
| `gate up [--json] [-d\|--daemon] [--dns localhost\|hosts] [--https-addr addr] [--http-addr addr]` | reserve/allocate ports, reflect DNS, push routes |
| `gate down [--json]` | deactivate this project's routes (reservations kept) |
| `gate ls [-a\|--all] [--status live\|down] [--json]` | list reservations with live/down status |
| `gate port [service] [-a\|--all] [--json]` | print one service port, or list reserved ports |
| `gate run <service> -- <cmd...>` | run a command with `PORT` injected |
| `gate add <domain> <port> [--json]` | reserve a domain→port mapping |
| `gate rm <domain> [--json]` / `gate rm --project [name] [--json]` | remove a reservation or project reservations |
| `gate prune [--json]` | GC reservations whose gate.toml is gone |
| `gate daemon start [-g\|--global] [-p name\|--project name] [--https-addr addr] [--http-addr addr]` | start one scoped proxy |
| `gate daemon stop [-g\|--global] [-p name\|--project name] [-a\|--all]` | stop scoped proxy daemon(s) |
| `gate daemon restart [-g\|--global] [-p name\|--project name] [--https-addr addr] [--http-addr addr]` | restart one scoped proxy |
| `gate daemon logs [-g\|--global] [-p name\|--project name] [-a\|--all]` | print scoped proxy logs |
| `gate daemon status [-g\|--global] [-p name\|--project name] [-a\|--all] [--json]` | inspect scoped proxy status |
| `gate doctor [--fix] [--json]` | check and repair local gate state |
| `gate trust` | install the root CA (one time) |
| `gate untrust` | remove the root CA from OS/browser trust stores |
| `gate ca export [--out path]` | export the root CA for other devices |
| `gate expose <service> --via <provider> [--auth user:pass] [--json]` | reach a service externally |
| `gate upgrade [-y\|--yes]` | upgrade to the latest GitHub release |
| `gate skill path\|print` | locate or print this skill file |

## Exit codes

`0` ok · `1` error · `2` usage · `3` permission (needs sudo) · `4` port/domain/daemon-listen conflict.

## Recipes

Start a dev server on its assigned port:

```bash
"$GATE_BIN" up
"$GATE_BIN" run web -- pnpm dev   # PORT is injected
```

Get a port for a script:

```bash
PORT=$("$GATE_BIN" port web) pnpm dev
```

List reserved ports:

```bash
"$GATE_BIN" port
"$GATE_BIN" port -a   # all projects
```

Inspect mappings as JSON:

```bash
"$GATE_BIN" ls --json
```

JSON-mode errors are written to stderr:

```json
{"error":{"code":"port_conflict","message":"port 4310 already reserved by myapp/web"}}
```

## gate.toml

```toml
[project]
name = "myapp"

[services.web]
domain = "app.example.com"       # port omitted -> auto-allocated

[services.api]
domain = "api.example.com"
port = 3001                      # fixed when needed
```

`domain` and `port` support environment interpolation:

```toml
[project]
name = "myapp"
env_files = [".env.local", ".env"]

[services.web]
domain = "${WEB_DOMAIN:-app.localhost}"
port = "${WEB_PORT:-3000}"

[services.api]
domain = "api.${BASE_DOMAIN:-localhost}"
port = "${API_PORT}"
```

`env_files` are resolved relative to `gate.toml`. Missing env files are ignored.
Process env overrides dotenv values; earlier env files override later env files.
`${NAME}` is required and errors when unset. `${NAME:-default}` is optional and
uses `default` when unset or empty.

Inside a project, `gate add`/`gate rm` edit this file in place, preserving comments.
Outside a project they create/remove standalone registry reservations. Domains ending
in `.localhost` need no sudo; custom domains use `/etc/hosts` (sudo).
Project reservations are served by that project's daemon. Standalone
reservations are served by the global daemon. If the relevant daemon is running,
`up`/`down`/`add`/`rm` hot-reload only that scope. If it is stopped,
`gate daemon start` inside a project starts the project daemon; outside a project
it starts the global daemon. Use `gate daemon status --all` to inspect all known
daemon scopes. Outside a project, `gate port <domain>` and
`gate run <domain> -- ...` resolve standalone reservations by domain.
