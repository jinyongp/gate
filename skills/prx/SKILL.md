---
name: prx
description: Drive the prx local HTTPS reverse proxy and port registry from the command line. Use when a repository has a prx.toml and you need to map domains to local dev servers over trusted HTTPS, allocate or look up ports without hardcoding them, start a dev server on its assigned port, or inspect the machine-wide registry. Commands that document --json emit stable, parseable output.
---

# prx

`prx` maps local domains to dev servers over trusted HTTPS and manages a
machine-wide port registry. Commands write data to stdout and diagnostics to
stderr. Commands marked `--json` emit a single JSON value on success; JSON-mode
errors use a stderr envelope.

## When to use

- A project has a `prx.toml` declaring services (`domain` → optional `port`).
- You need a stable, non-conflicting port for a dev server.
- You need to bring routes up/down or check what is mapped.

## Binary resolution

Agent sessions may not have `$HOME/.local/bin` in `PATH`. Before running `prx`,
resolve the executable once and use `"$PRX_BIN"` for later commands:

```bash
if command -v prx >/dev/null 2>&1; then
  PRX_BIN="$(command -v prx)"
elif [ -x "$HOME/.local/bin/prx" ]; then
  PRX_BIN="$HOME/.local/bin/prx"
else
  echo "prx not found; install it or report the missing binary." >&2
  exit 1
fi
```

## Core commands

| command | purpose |
| --- | --- |
| `prx init [--json] [--name name] [--force]` | scaffold a starter `prx.toml` |
| `prx up [--json] [--dns localhost\|hosts]` | reserve/allocate ports, reflect DNS, push routes |
| `prx down [--json]` | deactivate this project's routes (reservations kept) |
| `prx ls [--json]` | list reservations with live/down status |
| `prx port [service] [-a|--all] [--json]` | print one service port, or list reserved ports |
| `prx run <service> -- <cmd...>` | run a command with `PORT` injected |
| `prx add <domain> <port> [--json]` | reserve a domain→port mapping |
| `prx rm <domain> [--json]` | remove a reservation |
| `prx prune [--json]` | GC reservations whose prx.toml is gone |
| `prx daemon start\|stop\|restart\|status [--json]\|logs` | manage the resident proxy; `--json` is for `status` |
| `prx trust` | install the root CA (one time) |
| `prx ca export [--out path]` | export the root CA for other devices |
| `prx expose <service> --via <provider> [--auth user:pass] [--json]` | reach a service externally |
| `prx upgrade [--yes]` | upgrade to the latest GitHub release |
| `prx skill path\|print` | locate or print this skill file |

## Exit codes

`0` ok · `1` error · `2` usage · `3` permission (needs sudo) · `4` port/domain conflict.

## Recipes

Start a dev server on its assigned port:

```bash
"$PRX_BIN" up
"$PRX_BIN" run web -- pnpm dev   # PORT is injected
```

Get a port for a script:

```bash
PORT=$("$PRX_BIN" port web) pnpm dev
```

List reserved ports:

```bash
"$PRX_BIN" port
"$PRX_BIN" port -a   # all projects
```

Inspect mappings as JSON:

```bash
"$PRX_BIN" ls --json
```

JSON-mode errors are written to stderr:

```json
{"error":{"code":"port_conflict","message":"port 4310 already reserved by myapp/web"}}
```

## prx.toml

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
domain = "${PRX_WEB_DOMAIN:-app.localhost}"
port = "${PRX_WEB_PORT:-3000}"

[services.api]
domain = "api.${PRX_BASE_DOMAIN:-localhost}"
port = "${PRX_API_PORT}"
```

`env_files` are resolved relative to `prx.toml`. Missing env files are ignored.
Process env overrides dotenv values; earlier env files override later env files.
`${NAME}` is required and errors when unset. `${NAME:-default}` is optional and
uses `default` when unset or empty.

Inside a project, `prx add`/`prx rm` edit this file in place, preserving comments.
Outside a project they create/remove adhoc registry reservations. Domains ending
in `.localhost` need no sudo; custom domains use `/etc/hosts` (sudo).
