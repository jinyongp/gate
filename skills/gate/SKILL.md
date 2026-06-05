---
name: gate
description: Drive the gate local HTTPS reverse proxy and port registry from the command line. Use when a repository has a gate.toml and you need to map domains to local dev servers over trusted HTTPS, allocate or look up ports without hardcoding them, start a dev server on its assigned port, or inspect the machine-wide registry. Commands that document --json emit stable, parseable output.
---

# gate

`gate` maps local domains to dev servers over trusted HTTPS and manages a
machine-wide port registry. Commands write data to stdout and diagnostics to
stderr. Commands marked `--json` emit a single JSON value on success; JSON-mode
errors use a stderr envelope. `gate doctor --json` emits its issue report on
stdout even when issues make it exit non-zero. Longer operations may show a
TTY-only activity indicator on stderr; JSON mode, redirected stderr,
`NO_COLOR`, `CI`, and `GATE_NO_INDICATOR=1` disable it. `FORCE_COLOR=1` and
`CLICOLOR_FORCE=1` force styled text only; they do not force activity
indicators.

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
| `gate init [--name name] [--force] [-y\|--yes] [--json]` | scaffold a starter `gate.toml` |
| `gate up [-d\|--daemon] [--dns localhost\|hosts] [-g\|--global] [-p name\|--project name] [--json]` | reserve current-project ports or activate scoped reservations |
| `gate ls [--route active\|inactive] [--upstream live\|down] [-g\|--global] [-p name\|--project name] [-a\|--all] [--json]` | list scoped reservations with route/upstream status |
| `gate port [-g\|--global] [-p name\|--project name] [-a\|--all] [service] [--json]` | print one scoped service port, or list reserved ports |
| `gate run [-g\|--global] [-p name\|--project name] <service> -- <cmd...>` | run a command with `PORT` injected |
| `gate down [-g\|--global] [-p name\|--project name] [--json]` | deactivate scoped routes (reservations kept) |
| `gate expose [--via <provider>] [--auth user:pass] [-g\|--global] [-p name\|--project name] <service> [--json]` | reach a scoped service externally |
| `gate expose ls [--via provider] [-g\|--global] [-p name\|--project name] [-a\|--all] [--json]` | list exposure records |
| `gate expose stop [--via <provider>] [--force] [-g\|--global] [-p name\|--project name] <service> [--json]` | stop one exposure |
| `gate daemon status [-a\|--all] [--json]` | inspect listener proxy status |
| `gate add [-g\|--global] [-p name\|--project name] <service> <domain> <port> [--json]` | reserve a scoped service/name mapping |
| `gate rm [-g\|--global] [-p name\|--project name] <service> [--json]` | remove one scoped reservation |
| `gate clear [-g\|--global] [-p name\|--project name] [-y\|--yes] [--json]` | remove all reservations in one scope |
| `gate prune [--json]` | GC reservations whose gate.toml is gone |
| `gate daemon start` | start the default listener proxy |
| `gate daemon stop [-a\|--all]` | stop listener proxy daemon(s) |
| `gate daemon restart` | restart the default listener proxy |
| `gate daemon logs [-a\|--all]` | print listener proxy logs |
| `gate doctor [--fix] [--json]` | check and repair local gate state |
| `gate trust` | install the root CA (one time) |
| `gate untrust` | remove the root CA from OS/browser trust stores |
| `gate ca export [--out path]` | export the root CA for other devices |
| `gate upgrade [-y\|--yes]` | upgrade to the latest GitHub release, then run doctor |
| `gate completion bash\|zsh\|fish` | print shell completion script |
| `gate skill path\|print` | locate or print this skill file |
| `gate uninstall [--keep-trust] [--keep-brew] [-y\|--yes]` | remove gate state, binaries, and Homebrew package when applicable |

`gate expose ls` status values are `live`, `down`, or `unverified`.
`unverified` means gate has a local exposure record but cannot prove the
external provider is currently serving it.

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

Inside a project, `gate add <service> <domain> <port>` and `gate rm <service>`
edit this file in place, preserving comments. Outside a project the default
scope is global; use `-g` explicitly when operating from inside a project.
`gate clear` removes scoped registry reservations and route/DNS state only; it
does not edit `gate.toml`.
Remove reservations by service/name. Use `gate clear -y` for whole-scope
removal; single-service `gate rm` does not prompt.
Domains ending in `.localhost` need no sudo; custom domains use `/etc/hosts`
(sudo). Active reservations are served by listener daemons, defaulting to
HTTPS `:443` / HTTP `:80`. If the relevant listener daemon is running,
`up`/`down`/`add`/`rm`/`clear` hot-reload the merged route table for that
listener. Use `gate daemon status --all` to inspect all known listener daemons.
Outside a project, `gate port <name>` and
`gate run <name> -- ...` resolve global reservations by name.

Shell completion is read-only, local-state aware, quiet on missing/broken local
state, and uses a stable task-oriented order. It completes root commands,
`daemon status|start|stop|restart|logs`, `ca export`, `expose ls|stop`,
`skill path|print`, and `completion bash|zsh|fish`. `--<tab>` shows long flags and `-<tab>` shows short
flags for the current command/subcommand. `--project` completes registry
project names. Scoped service/name arguments for `add`, `rm`, `run`, `port`,
and `expose` complete current-project services inside a project, global
reservation names outside a project, global names with `-g|--global`, and known
named-project services with `-p|--project`. Enum completions include
`ls --route` (`active|inactive`), `ls --upstream` (`live|down`),
`up --dns` (`localhost|hosts`), and
`expose --via` (`local|lan|cloudflared|tailscale`). `ca export --out` keeps
file path completion; service/name positionals do not mix in local file names.
