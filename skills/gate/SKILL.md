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

## Node API

Use `@jinyongp/gate` when JavaScript automation needs typed gate data instead of
shell parsing. Install only `@jinyongp/gate`; it exposes the `gate` binary and
loads platform optional binary packages for supported Darwin/Linux arm64/x64
hosts.

```ts
import { createGateClient, isGateError } from '@jinyongp/gate'

const gate = createGateClient({ cwd: process.cwd() })
const web = await gate.service('web', { up: true })
const ready = await gate.ready('web', { up: true })
await gate.run(ready, ['pnpm', 'dev'])

try {
  await gate.service('web')
} catch (error) {
  if (isGateError(error, 'GATE_DNS_REQUIRED')) {
    // Use .localhost, or opt into dns: 'hosts'/'preconfigured'.
  }
  throw error
}
```

For JS/package installs, use the package `gate` bin or call
`resolveGateBinary()` and pass it as `bin`/`GATE_BIN`; the earlier `$GATE_BIN`
snippet is for system-installed gate. Prefer the CLI for launching child
processes:

```bash
pnpm exec gate run --up web -- pnpm dev
```

In an interactive terminal, `gate run --up` prints the selected route to stderr
before starting the child process. Use `--quiet` when only child stderr should
appear.

For scripts or agents that need structured route/env data without spawning a
child, use:

```bash
pnpm exec gate env web --json
```

`gate env` is read-only by default. Use `gate env --up web --json` only when the
script intentionally wants to reserve/activate routes first.
The JSON descriptor is the canonical route/env readiness contract. It includes
`service`, route URLs, loopback URL, `env`, daemon readiness, and diagnostics.
Unknown fields should be ignored.

For sandboxed agents or tests, isolate gate state under a workspace-local,
git-ignored directory instead of writing registry locks, daemon sockets, logs,
or CA material under the user's normal gate state:

```bash
pnpm exec gate --isolated-root .gate-agent env --up web --json
pnpm exec gate --isolated-root .gate-agent run --up web -- pnpm dev
```

The CLI flag sets `GATE_ISOLATED_ROOT` for that command. Node callers should
use `createGateClient({ isolatedRoot: '.gate-agent' })`.

`service(name)` defaults to `{ up: true, dns: 'localhost', daemon: false }`. It
can reserve/activate routes, but it does not start the daemon unless
`daemon: true` is passed. Use `service(name, { up: false })`, `ls()`, or
`port()` for read-only inspection. Custom domains must explicitly choose
`dns: 'hosts'` or `dns: 'preconfigured'`.
Use `ready(name, options)` when automation needs the canonical descriptor before
spawning, then pass that snapshot to `run(ready, argv)` to avoid duplicate
resolution.

Use inline project config when an agent needs project-scoped Node API behavior
without creating `gate.toml`:

```ts
import { createGateClient, type GateInlineProjectConfig } from '@jinyongp/gate'

const config = {
  name: 'myapp',
  base: 'myapp.localhost',
  services: { web: {} },
} satisfies GateInlineProjectConfig

const gate = createGateClient({ cwd: process.cwd() })
const web = await gate.service('web', { scope: { config } })
```

Inline config is written to the user cache as generated TOML and passed through
`--config`. `scope.project`, if present, must match `config.name`. Do not put
`envFiles` in Node API options; load environment variables before calling gate
when inline values use `${NAME}` or `${NAME:-fallback}` references.

Common Node error actions:

- `GATE_DNS_REQUIRED`: use `.localhost`, or intentionally pass `dns: 'hosts'` /
  `dns: 'preconfigured'`.
- `GATE_INVALID_OPTIONS`: fix incompatible scope/config options before retrying.
- `GATE_BINARY_NOT_FOUND` / `GATE_UNSUPPORTED_PLATFORM`: reinstall `@jinyongp/gate`
  or pass an explicit `bin` / `GATE_BIN`.
- `GATE_PERMISSION_REQUIRED`: stop unless the user approved the privileged
  DNS/trust action.
- `GATE_SERVICE_NOT_FOUND`: check scope, config path, service name, and whether
  reservations exist.
- `GATE_COMMAND_FAILED` / `GATE_JSON_PARSE_FAILED`: inspect command metadata and
  stderr before retrying.
When a JSON error envelope is available, `GateError` also carries `severity`,
`retryable`, `hint`, and `nextActions`.

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
