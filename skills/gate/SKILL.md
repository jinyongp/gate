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

## Automation

Use the CLI for scripts and agents. Launch child processes through `gate run`:

```bash
"$GATE_BIN" run --up web -- pnpm dev
```

In an interactive terminal, `gate run --up` prints the selected route to stderr
before starting the child process. Use `--quiet` when only child stderr should
appear.

For scripts or agents that need structured route/env data without spawning a
child, use:

```bash
"$GATE_BIN" env web --json
```

`gate env` is read-only by default. Use `gate env --up web --json` only when the
script intentionally wants to reserve/activate routes first.
The JSON descriptor is the canonical route/env readiness contract. It includes
`service`, route URLs, loopback URL, `env`, `envKeys`, daemon readiness, and
diagnostics. `diagnostics[].actions` contains recommended next steps;
`suggestedCommand` remains for compatibility. Unknown fields should be ignored.

For sandboxed agents or tests, isolate gate state under a workspace-local,
git-ignored directory instead of writing registry locks, daemon sockets, logs,
or CA material under the user's normal gate state:

```bash
"$GATE_BIN" --isolated-root .gate-agent env --up web --json
"$GATE_BIN" --isolated-root .gate-agent run --up web -- pnpm dev
```

Use isolated state for temporary inspection, tests, and sandboxed setup checks.
Use normal gate state for real dev app launches that should share the user's
registry, trusted certificate material, and listener daemon.
`--isolated-root` does not isolate kernel listener ports such as HTTPS `:443` and
HTTP `:80`. For isolated daemon tests, use explicit non-default listener
addresses. Run `gate doctor --json` only for install/setup/CI/preflight or
explicit local state diagnosis.

## Linux and WSL low ports

The default daemon binds HTTPS `:443` and HTTP `:80`. On Linux, including WSL,
inspect setup without mutation:

```bash
"$GATE_BIN" daemon setup --check --json
```

If setup is missing, ask for user approval before running:

```bash
"$GATE_BIN" daemon setup
```

Never run the whole gate CLI with sudo. Setup grants only the installed gate
executable `CAP_NET_BIND_SERVICE`; arbitrary commands launched by `gate run` do
not inherit it. Setup is unavailable under `--isolated-root`.

In WSL, the binary must live on the Linux filesystem (for example under
`/home`), not `/mnt/c`. If a package-manager upgrade replaces the binary and a
low-port bind fails, rerun setup. macOS help and completion intentionally omit
this Linux-only command.

## Operational workflows

Use [`docs/usage.md`](../../docs/usage.md) for full command syntax, output
semantics, JSON behavior, troubleshooting, and exit codes.

Start or refresh routes, then launch a dev server on its assigned port:

```bash
"$GATE_BIN" up -d
"$GATE_BIN" run web -- pnpm dev   # PORT and peer service env are injected
```

Reserve first, then launch without a separate `up` command:

```bash
"$GATE_BIN" run --up web -- pnpm dev
```

Use `--config path/to/file.toml` when the project config is not named
`gate.toml` or is not discoverable from the current directory. Do not combine it
with `--global` or `--all`. Use `-g` for global reservations and `-p <name>` for
a named project.

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

Check local state when routing or trust looks wrong:

```bash
"$GATE_BIN" doctor
"$GATE_BIN" doctor --json
```

## gate.toml

```toml
[project]
name = "myapp"
base = "myapp.localhost"

[services.web]

[services.api]
port = 3001                      # fixed when needed
env = "API_URL"                  # injected as http://127.0.0.1:<api-port>
route_env = "PUBLIC_API_URL"     # injected as https://api.myapp.localhost
```

For browser-visible URLs, prefer framework-public `route_env` names such as
`VITE_API_BASE_URL`, `NEXT_PUBLIC_API_BASE_URL`, or
`NUXT_PUBLIC_API_BASE_URL` instead of hardcoded HTTPS URLs.

`base`, `domain`, `host`, and `port` support environment interpolation.
`env_files` are resolved relative to the selected project config file.

```toml
[project]
name = "myapp"
env_files = [".env.local", ".env"]
base = "${BASE_DOMAIN:-localhost}"

[services.web]
port = "${WEB_PORT:-3000}"

[services.api]
port = "${API_PORT}"
env = "API_URL"
route_env = "PUBLIC_API_URL"
```

Inside a project, `gate add <service> <port>` derives the service domain from
`[project] base`; use `--host` for a base label override or `--domain` for a
full-domain escape hatch. `gate add` and `gate rm <service>` edit this file in
place, preserving comments. `gate clear` removes scoped registry reservations
and route/DNS state only; it does not edit `gate.toml`.

## Exposure notes

Domains ending in `.localhost` need no sudo; custom domains use `/etc/hosts`
(sudo). Active reservations are served by listener daemons, defaulting to
HTTPS `:443` / HTTP `:80`. If the relevant listener daemon is running,
`up`/`down`/`add`/`rm`/`clear` hot-reload the merged route table for that
listener. Use `gate daemon status --all` to inspect all known listener daemons.
`gate expose <service> --via lan` derives a `.local` alias from the service
domain: `.local` stays unchanged, `.localhost` becomes `.local`, and other
domains append `.local`. Use `--domain name.local` to override the LAN alias.
Outside a project, `gate port <name>` and
`gate run <name> -- ...` resolve global reservations by name.
