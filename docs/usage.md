# gate usage

`gate` maps local domains to local dev servers over HTTPS and keeps a
machine-wide registry of domain and port reservations.

Use project mode when a repository should carry its routing in `gate.toml`.
Use global reservations when you want a machine-local mapping without adding a
project file.

## Quick Reference

Use `gate --help` for the root command list, or `gate <command> --help` for one
command's flags and positional arguments.

Global flags:

- `--isolated-root path`: isolate gate registry, daemon sockets, logs, and CA
  material under `path`. The flag may appear before the command or before a
  command's `--` child separator. Child arguments after `--` are left untouched.

| command                                                                                                                                                                                   | purpose                                                                      |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| `gate init [--name name] [--force] [-y\|--yes] [--json]`                                                                                                                                  | scaffold a starter `gate.toml`                                               |
| `gate up [-d\|--daemon] [--dns localhost\|hosts] [--config path] [-g\|--global] [-p name\|--project name] [--json]`                                                                       | reserve ports, activate routes, reflect DNS, and optionally start the daemon |
| `gate down [--config path] [-g\|--global] [-p name\|--project name] [--json]`                                                                                                             | deactivate scoped routes while keeping reservations                          |
| `gate ls [--route active\|inactive] [--upstream live\|down] [--config path] [-g\|--global] [-p name\|--project name] [-a\|--all] [--json]`                                                | list reservations with route and upstream status                             |
| `gate port [--config path] [-g\|--global] [-p name\|--project name] [-a\|--all] [service] [--json]`                                                                                       | print one service port or list reserved ports                                |
| `gate env [--up] [--config path] [-g\|--global] [-p name\|--project name] <service> [--json]`                                                                                              | print the environment for one scoped service                                 |
| `gate run [--up] [--quiet] [--config path] [-g\|--global] [-p name\|--project name] <service> -- <cmd...>`                                                                                  | run a child command with `PORT` injected                                     |
| `gate add [--config path] [-g\|--global] [-p name\|--project name] [--host host] [--domain domain] <service> <port> [--json]`                                                             | add or update one reservation                                                |
| `gate rm [--config path] [-g\|--global] [-p name\|--project name] <service> [--json]`                                                                                                     | remove one reservation                                                       |
| `gate clear [--config path] [-g\|--global] [-p name\|--project name] [-y\|--yes] [--json]`                                                                                                | remove all reservations in one scope                                         |
| `gate prune [--json]`                                                                                                                                                                     | remove stale project reservations whose config file is gone                  |
| `gate daemon status [-a\|--all] [--json]`                                                                                                                                                 | inspect listener daemon status                                               |
| `gate daemon setup [--check] [-y\|--yes] [--json]`                                                                                                                                       | configure Linux low-port access                                              |
| `gate daemon start`                                                                                                                                                                       | start or reuse the default listener daemon                                   |
| `gate daemon stop [-a\|--all]`                                                                                                                                                            | stop listener daemon(s)                                                      |
| `gate daemon restart`                                                                                                                                                                     | restart the default listener daemon                                          |
| `gate daemon logs [-a\|--all]`                                                                                                                                                            | print listener daemon logs                                                   |
| `gate trust`                                                                                                                                                                              | install the local root CA into OS/browser trust stores                       |
| `gate untrust`                                                                                                                                                                            | remove the local root CA from trust stores                                   |
| `gate ca export [--out path]`                                                                                                                                                             | export the local root certificate                                            |
| `gate doctor [--fix] [--json]`                                                                                                                                                            | check and repair gate-owned local state                                      |
| `gate expose [--via local\|lan\|cloudflared\|tailscale] [--domain name.local] [--auth user:pass] [--no-auth] [--config path] [-g\|--global] [-p name\|--project name] <service> [--json]` | expose a scoped service through a provider                                   |
| `gate expose ls [--via provider] [--config path] [-g\|--global] [-p name\|--project name] [-a\|--all] [--json]`                                                                           | list exposure records                                                        |
| `gate expose stop [--via provider] [--force] [--config path] [-g\|--global] [-p name\|--project name] <service> [--json]`                                                                 | stop or forget one exposure record                                           |
| `gate upgrade [-y\|--yes]`                                                                                                                                                                | upgrade to the latest release, then run doctor                               |
| `gate completion bash\|zsh\|fish`                                                                                                                                                         | print shell completion script                                                |
| `gate skill path\|print`                                                                                                                                                                  | locate or print the bundled agent skill                                      |
| `gate uninstall [--keep-trust] [--keep-brew] [-y\|--yes]`                                                                                                                                 | remove gate state, binaries, and Homebrew package when applicable            |

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/install.sh | sh
```

> [!TIP]
> The installer writes `gate` to `~/.local/bin` by default. If that directory is
> not in `PATH`, the installer offers to update your shell startup file and
> prints the exact line you can add manually.

### Linux and WSL low ports

The default listener uses HTTPS `:443` and HTTP `:80`. On Linux, including WSL,
configure the installed executable once:

```bash
gate daemon setup
```

An interactive standalone install offers this step and defaults to yes.
Fresh non-interactive installs never invoke sudo and print the command to run
later. Linux package-manager installs cannot perform this privileged
post-install step, so run it explicitly before starting the default listener.

The command applies only `cap_net_bind_service=ep` to the canonical gate
executable. Continue running gate as your normal user; do not use
`sudo gate up -d` or `sudo gate daemon start`.

WSL installations must place the binary on a Linux-native filesystem, such as
`/home/<user>/.local/bin`. Windows-mounted paths such as `/mnt/c` can reject the
file-capability xattr. Move or reinstall the binary on the Linux filesystem,
then rerun setup. Setup applies the file capability through an ephemeral,
root-owned copy of gate's fixed internal helper and does not invoke an external
`setcap` command. The helper removes itself before mutation; a later serialized
setup or uninstall cleans a copy left by interruption before helper startup.
They serialize on a private user-cache lock that remains stable while uninstall
removes gate's ordinary config, data, state, and binaries.
Replacing an existing standalone install uses the distribution's libcap
`getcap` tool to detect whether capability preservation is required. Every
standalone Linux install requires the util-linux `flock` tool and retains a
user-owned `gate.install.lock` coordination file beside the binary; configured
replacements use it to serialize replacement and crash recovery.

## Trust HTTPS

`gate` issues local certificates from a local root CA. To remove browser
certificate warnings, trust the CA once:

```bash
gate trust
```

> [!NOTE]
> This can require OS administrator approval. `.localhost` domains need no DNS
> setup. Custom domains can require `/etc/hosts` changes, so commands that
> reflect DNS may ask for permission.

Remove gate's root CA from local trust stores:

```bash
gate untrust
```

## Doctor

Check local gate-owned state:

```bash
gate doctor
```

Repair issues that do not require sudo:

```bash
gate doctor --fix
```

Use JSON for scripts:

```bash
gate doctor --json
```

`doctor` currently checks legacy single-daemon files, stale scoped daemon pid
files, and legacy registry entries from pre-scoped development builds. It exits
with `1` when issues remain. In JSON mode, the issue report is written to stdout
even when the command exits `1`; usage and internal errors still use stderr.
Use `doctor --json` for install, setup, CI, preflight, and explicit local state
diagnostics. Normal Node API flows such as `service()`, `ready()`, and `run()`
do not call `doctor`; they use command-specific JSON output and `GateError`
metadata for failures.

## Project Mode

Project mode uses a `gate.toml` file in the repository. This is the shareable,
repeatable path for a team.

By default, project commands discover `gate.toml` by walking upward from the
current directory. Pass `--config path/to/file.toml` to use a specific project
config file instead. Relative paths resolve from the current directory, and
`env_files` inside that config resolve relative to the selected config file.
`--config` can be paired with `--project name` to assert the project name, but it
cannot be used with `--global` or `--all`.

Create a starter config:

```bash
gate init
```

Use a specific project name:

```bash
gate init --name myapp
```

Non-interactive default:

```bash
gate init -y
```

Overwrite an existing config after confirmation:

```bash
gate init --force
```

Example `gate.toml`:

```toml
[project]
name = "myapp"
base = "myapp.localhost"

[services.web]

[services.api]
port = 3001
env = "API_URL"
route_env = "PUBLIC_API_URL"
```

Project names must be non-empty, cannot contain `/` or control/line-separator
characters, and cannot have leading or trailing whitespace. Service names must
match `[A-Za-z0-9_][A-Za-z0-9_-]*`; `ls` and `stop` are reserved service names.
An underscore in a service name is converted to a hyphen when gate derives the
default DNS host label. `PORT` and `GATE_*` are reserved and cannot be declared
through `env` or `route_env`. Configs
created by older development builds with other service names must rename those
tables before current gate commands can load them. Remove any old registry-only
name with `gate rm -g <old-name>` or `gate rm -p <project> <old-name>`; use
`gate clear` for a whole legacy scope.

Bring the project up and start the daemon:

```bash
gate up -d
```

Force the DNS mode when needed:

```bash
gate up --dns localhost
gate up --dns hosts
```

Run a dev server with its reserved port injected as `PORT`:

```bash
gate run web -- pnpm dev
```

Reserve first, then run the child command:

```bash
gate run --up web -- pnpm dev
```

When stderr is an interactive terminal, `gate run --up` prints the selected
route before starting the child process. The hint is written to stderr so child
stdout stays clean:

```text
gate route  web  https://web.myapp.localhost  ->  http://127.0.0.1:4310
gate fixable  daemon_not_running: listener daemon is not running
gate action  Start listener daemon: gate up --daemon --project myapp
```

Use `--quiet` to suppress gate's parent route/status hint without suppressing
the child process output:

```bash
gate run --up --quiet web -- pnpm dev
```

`gate run` also injects peer service values into the child process:
`GATE_<SERVICE>_PORT`, `GATE_<SERVICE>_URL`,
`GATE_<SERVICE>_ROUTE_URL`, and any service-declared env names such as
`API_URL`. `env` values use loopback URLs such as
`http://127.0.0.1:<port>`. `route_env` values use route URLs such as
`https://api.myapp.localhost`.

Print the same environment without spawning a child:

```bash
gate env web
gate env web --json
```

`gate env` is read-only by default. Pass `--up` when a script intentionally
wants to reserve or activate the selected scope before reading the environment:

```bash
gate env --up web --json
```

`gate env --json` includes the selected service, local route URL, loopback URL,
route/upstream status, daemon readiness, diagnostics, and the environment map
that `gate run` would inject.

Example JSON shape:

```json
{
  "service": "web",
  "project": "myapp",
  "domain": "web.myapp.localhost",
  "port": 4310,
  "url": "https://web.myapp.localhost",
  "loopbackUrl": "http://127.0.0.1:4310",
  "route": "active",
  "upstream": "down",
  "env": {
    "PORT": "4310",
    "GATE_WEB_PORT": "4310",
    "GATE_WEB_URL": "http://127.0.0.1:4310",
    "GATE_WEB_ROUTE_URL": "https://web.myapp.localhost"
  },
  "envKeys": [
    "GATE_WEB_PORT",
    "GATE_WEB_ROUTE_URL",
    "GATE_WEB_URL",
    "PORT"
  ],
  "daemon": {
    "required": true,
    "running": false,
    "listener": "listener:https-443-http-80",
    "httpsAddr": ":443",
    "httpAddr": ":80"
  },
  "diagnostics": [
    {
      "code": "daemon_not_running",
      "severity": "fixable",
      "message": "listener daemon is not running",
      "suggestedCommand": "gate up --daemon --project myapp",
      "actions": [
        {
          "label": "Start listener daemon",
          "command": "gate up --daemon --project myapp"
        }
      ]
    }
  ]
}
```

`standalone: true` appears for global reservations. `project` is omitted when
the service is not project-scoped. Consumers should ignore unknown fields.
Descriptor fields are additive across minor releases; existing field types and
meanings are stable. `envKeys` is summary metadata for agents that need to know
which variables are available without inspecting every value. `diagnostics`
describe current readiness state; `actions` are recommended next steps.
`suggestedCommand` is kept for compatibility. Daemon diagnostic commands
preserve the selected scope and may include `--config`, `--global`,
`--https-addr`, or `--http-addr` when needed.

Open:

```text
https://web.myapp.localhost
```

### Tauri + Vite

Use gate's route URL as the app URL and gate's reserved port as Vite's dev
server port:

```toml
[project]
name = "desktop"
base = "app.localhost"

[services.desktop]
route_env = "TAURI_DEV_URL"
```

```ts
// vite.config.ts
import { defineConfig } from 'vite'

export default defineConfig({
  server: {
    host: '127.0.0.1',
    port: Number(process.env.PORT),
    strictPort: true,
  },
})
```

```bash
gate run --up desktop -- pnpm dev
```

Configure Tauri's `devUrl` to the same gate route, for example
`https://desktop.app.localhost`. If your Tauri config is JavaScript or
TypeScript, read `process.env.TAURI_DEV_URL` from `route_env`.

### Browser dev apps

For browser frameworks, publish route URLs through framework-public env names
instead of hardcoding `https://...` in app config:

```toml
[services.api]
route_env = [
  "VITE_API_BASE_URL",
  "NEXT_PUBLIC_API_BASE_URL",
  "NUXT_PUBLIC_API_BASE_URL",
]
```

Then start the app through gate:

```bash
gate run --up web -- pnpm dev
```

The child process receives gate-calculated route URLs. Custom listener ports are
included when gate can read the active listener daemon status.

Stop routing for the current project while keeping reservations:

```bash
gate down
```

## Agent and Sandbox Isolation

By default, gate uses the user's normal XDG/macOS state locations, such as
`~/.config/gate`, for registry locks, daemon sockets, logs, and certificate
material. Agents, tests, and sandboxed runners should use an isolated root when
they must avoid writing to the user's normal gate state:

```bash
gate --isolated-root .gate-agent env --up web --json
gate --isolated-root .gate-agent run --up web -- pnpm dev
```

`--isolated-root path` sets `GATE_ISOLATED_ROOT` for that command. Relative
paths resolve from the current working directory. `GATE_ISOLATED_ROOT` maps gate
state below:

```text
<root>/xdg/config/gate
<root>/xdg/state/gate
<root>/xdg/data/gate
<root>/run
```

`GATE_ISOLATED_ROOT` takes precedence over `XDG_CONFIG_HOME`, `XDG_STATE_HOME`,
and `XDG_DATA_HOME`. Existing XDG variables still work when
`GATE_ISOLATED_ROOT` is not set.

Use isolated state for temporary agent inspection, tests, and sandboxed setup
checks. Use the user's normal gate state for real dev app launches that should
share the user's registry, trusted certificate material, and listener daemon.
Isolated state does not isolate kernel listener ports such as HTTPS `:443` and
HTTP `:80`. Node API calls with `isolatedRoot` reject `daemon: true`; pass
`daemon: false` or omit it. For isolated daemon tests, use the CLI with explicit
non-default listener addresses:

```bash
gate --isolated-root .gate-agent daemon start --https-addr 127.0.0.1:18443 --http-addr 127.0.0.1:18080
```

Isolated state cannot safely reference-count the machine-wide `/etc/hosts`
block, so gate refuses system-hosts mutation while `GATE_ISOLATED_ROOT` is set.
Use `.localhost`, preconfigure DNS outside gate, or run the intentional hosts
operation without isolated mode.

## Node

`@jinyongp/gate` is intended for agents and JavaScript tooling that need to inspect
or control gate from code. It executes the gate binary and consumes the same
JSON command contracts as scripts.

Install only `@jinyongp/gate`; it provides the `gate` package binary and uses
platform optional binary packages for supported Darwin/Linux arm64/x64 hosts.
Do not install platform packages directly.

```bash
pnpm add -D @jinyongp/gate
pnpm exec gate --version
```

Use the package binary for child-process workflows, or resolve the binary from
code and pass it as `bin` or `GATE_BIN`:

```ts
import { createGateClient, resolveGateBinary } from '@jinyongp/gate'

const bin = resolveGateBinary()
const gate = createGateClient({ bin })
```

Core API:

```ts
import { createGateClient } from '@jinyongp/gate'

const gate = createGateClient()
const web = await gate.service('web', { up: true })
```

Use `ready()` when an agent needs to inspect the same descriptor as
`gate env --json` before deciding how to launch the child:

```ts
const ready = await gate.ready('web', { up: true })

console.log(ready.service.url)
console.log(ready.daemon?.running)

await gate.run(ready, ['pnpm', 'dev'])
```

Inline project config:

```ts
import { createGateClient, type GateInlineProjectConfig } from '@jinyongp/gate'

const config = {
  name: 'myapp',
  base: 'myapp.localhost',
  services: {
    web: {},
    api: {
      port: 3001,
      env: 'API_URL',
    },
  },
} satisfies GateInlineProjectConfig

const gate = createGateClient({ cwd: process.cwd() })
const web = await gate.service('web', {
  scope: { config },
})
```

Inline config gives Node API callers project-scoped behavior without a
checked-in `gate.toml`. The package writes a generated TOML file to the user
cache and passes that file to the gate binary with `--config`. `scope.project`
may be supplied, but it must match `config.name`. The inline shape supports
`name`, `base`, and service `domain`, `host`, `port`, `env`, and `routeEnv`
fields.
`envFiles` are intentionally excluded; load environment variables before
calling gate if inline values use `${NAME}` or `${NAME:-fallback}` references.

Typed error handling:

```ts
import { createGateClient, isGateError } from '@jinyongp/gate'

const gate = createGateClient()

try {
  await gate.service('web')
} catch (error) {
  if (isGateError(error, 'GATE_DNS_REQUIRED')) {
    // Switch to a .localhost base, or pass dns: 'hosts'/'preconfigured'.
  }
  throw error
}
```

By default, `service(name)` is not read-only: it behaves like
`service(name, { up: true, dns: 'localhost', daemon: false })`. It reserves and
activates the selected scope before reading service metadata, but it does not
start the daemon or edit `/etc/hosts`. Use `service(name, { up: false })`,
`ls()`, or `port()` when you only want to inspect existing state. Custom domains
must opt into hosts-file DNS or declare preconfigured DNS through options.
When `isolatedRoot` is set on the client or a per-call option, `daemon: true`
is invalid because isolated state cannot isolate the shared listener ports.
`daemon: false` remains valid.

Common `GateError` codes:

| code                        | agent action                                                                            |
| --------------------------- | --------------------------------------------------------------------------------------- |
| `GATE_DNS_REQUIRED`         | Use a `.localhost` base, or pass `dns: 'hosts'` / `dns: 'preconfigured'` intentionally. |
| `GATE_INVALID_OPTIONS`      | Fix incompatible scope/config options before retrying.                                  |
| `GATE_BINARY_NOT_FOUND`     | Reinstall `@jinyongp/gate`, or pass an explicit `bin` / `GATE_BIN`.                     |
| `GATE_UNSUPPORTED_PLATFORM` | Use a supported Darwin/Linux arm64/x64 host or provide `bin`.                           |
| `GATE_PERMISSION_REQUIRED`  | Retry only after explicit user approval for the privileged DNS/trust action.            |
| `GATE_SERVICE_NOT_FOUND`    | Check scope, config path, service name, and whether reservations exist.                 |
| `GATE_COMMAND_FAILED`       | Inspect `exitCode`, `gateCode`, stdout, and stderr before retrying.                     |
| `GATE_JSON_PARSE_FAILED`    | Treat as a gate/version mismatch or broken binary output.                               |

When the gate binary emits a JSON error envelope, `GateError` also preserves
`gateCode`, `severity`, `retryable`, `hint`, and `nextActions`.

## Global Reservations

Global reservations create machine-local mappings without `gate.toml`. It is useful
when you already know the domain and port and do not want a project file.

Add a mapping:

```bash
gate add -g web 3000 --domain web.localhost
```

Global reservations are served by the listener daemon. If that daemon is
running, routes are hot-reloaded. If it is stopped, starting it later loads all
active routes for that listener from the registry:

```bash
gate up -g
gate daemon start
```

Run a dev server through the global reservation:

```bash
gate run -g web -- pnpm dev
```

Or use the port in your own command:

```bash
PORT=$(gate port -g web) pnpm dev
```

Open:

```text
https://web.localhost
```

Remove the global mapping:

```bash
gate down -g
gate rm -g web
```

## Inspect Reservations

Current project reservations:

```bash
gate ls
```

All reservations:

```bash
gate ls -a
```

Filter by route state or upstream liveness:

```bash
gate ls --route active
gate ls --upstream down
```

In JSON output, every service includes `url` and `loopbackUrl`. `url` includes
the active daemon's non-default HTTPS port when gate can verify that listener;
`loopbackUrl` always targets the reserved upstream port on `127.0.0.1`.

Port-focused view for the current project:

```bash
gate port
```

All reserved ports:

```bash
gate port -a
```

One scoped service:

```bash
gate port web
gate port -g web
gate port -p myapp web
```

## Manage Reservations

Add a current-project service and fixed port:

```bash
gate add web 3000
```

Inside a project, this adds or updates the `[services.<name>]` block in
`gate.toml` and updates the registry. With `[project] base = "myapp.localhost"`,
the default service domain is `web.myapp.localhost`.

Override the service host label under the project base:

```bash
gate add api 3001 --host app
```

Use a full-domain escape hatch:

```bash
gate add admin 3002 --domain console.internal.example.com
```

Add a global reservation:

```bash
gate add -g web 3000 --domain web.localhost
```

Add a named project reservation:

```bash
gate add -p myapp web 3000 --domain app.localhost
```

Activate or deactivate existing global or named-project reservations from the
registry:

```bash
gate up -g
gate down -g
gate up -p myapp
gate down -p myapp
```

Remove one service/name:

```bash
gate rm web
gate rm -g web
gate rm -p myapp web
```

Inside the current project, `gate rm <service>` removes that `[services.<name>]`
block from `gate.toml` and updates the registry. `-g` and `-p` remove registry
reservations only.

Remove all reservations for the current project:

```bash
gate clear -y
```

Remove all global or named-project reservations:

```bash
gate clear -g -y
gate clear -p myapp -y
```

`gate clear` removes registry reservations and route/DNS state. It does not edit
or delete `gate.toml`; use `gate rm <service>` to remove one current-project
service block. Without `-y`, `gate clear` prompts in an interactive terminal and
refuses to run in JSON or non-interactive contexts. Single-service `gate rm`
does not prompt.

For registry entries created by older development builds, quoted exact names
remain removable even when they violate current creation grammar, including
surrounding whitespace: `gate rm -p ' old-project ' 'old.service'` or
`gate clear -p ' old-project ' -y`.

Prune stale reservations whose owning project config file no longer exists:

```bash
gate prune
```

Global reservations are not pruned by `gate prune` because they have no
owning config file. Pruning reloads each affected listener and removes managed
DNS for active stale routes. Inactive reservations do not trigger DNS changes.
If config inspection, route reload, or DNS cleanup fails, gate restores the
registry and reconciles route/DNS state before returning an error. Managed
`/etc/hosts` cleanup can require administrator approval.

Current-project `gate up` also performs conservative implicit cleanup before
allocating ports. Missing, unexposed project reservations are pruned; exposed
reservations and paths that cannot be inspected are preserved. Use explicit
`gate prune` when inspection errors should be reported instead of skipped.

An active exposure blocks `down`, `rm`, `clear`, `prune`, and any `up`/`add`
change that would alter its domain or listener. Run the exact `gate expose stop`
command shown in the error, then retry. Unrelated reservations remain usable.

## Daemon

Daemon processes are keyed by listener address pair. The default listener is
HTTPS `:443` and HTTP `:80`, so one default daemon serves active routes from all
projects and global reservations that target that listener.

On Linux, inspect or configure the executable's low-port capability:

```bash
gate daemon setup --check
gate daemon setup
gate daemon setup --yes
gate daemon setup --check --json
```

`--check` is read-only and exits with a permission-required result when setup
is missing. Mutation asks for confirmation unless `--yes` is supplied. JSON
mutation requires `--yes`; JSON inspection requires `--check`. Setup is
unavailable with `--isolated-root` because it changes the real installed
executable.

The setup command is advertised by help and completion on Linux only. On macOS,
daemon help and completion remain unchanged, and manually invoking setup
returns an unsupported-platform error.

Start, stop, restart, and inspect the default listener proxy:

```bash
gate daemon start
gate daemon stop
gate daemon restart
gate daemon status
gate daemon logs
```

Inspect, stop, or read logs from all known listener daemons:

```bash
gate daemon status --all
gate daemon stop --all
gate daemon logs --all
```

`gate up -d` starts the listener daemon when needed and reloads the merged route
table for that listener.

If a Linux low-port bind fails with `permission denied`, human output points to
`gate daemon setup`. JSON errors use code `low_port_bind_permission` with the
same command in `nextActions`. High-port listeners, unrelated permission
errors, macOS failures, and `address already in use` conflicts keep their
existing diagnostics. A custom unprivileged listener needs no capability:

```bash
gate daemon start --https-addr :8443 --http-addr :8080
```

`gate daemon status --json` includes additive machine-readable health fields
such as `status`, `listener`, `socket_path`, `pid_path`, `pid_alive`,
`https_addr`, `http_addr`, `running`, `pid`, and `routes`. `status` is
`running`, `stopped`, or `stale`.

## JSON Output

Commands that support `--json` usually write a single JSON object to stdout.
Commands that target multiple listener daemons, such as `gate daemon status --all
--json`, write a JSON array. Errors in JSON mode are written to stderr as an
error envelope:

```json
{
  "error": {
    "code": "not_allocated",
    "message": "no reservation for \"web\" in project \"myapp\"",
    "severity": "fixable",
    "retryable": false,
    "hint": "Run `gate up` for the selected scope, or pass `--up` to commands that support it.",
    "nextActions": [
      {
        "label": "Bring up routes"
      }
    ]
  }
}
```

`code` and `message` are always present. `severity`, `retryable`, `hint`, and
`nextActions` are intended for scripts and agents; consumers should ignore
unknown fields. `nextActions.command` is optional and omitted when gate cannot
encode the selected scope safely.

Some longer operations show a one-line activity indicator on stderr when stderr
is an interactive terminal. Indicators never appear in JSON mode or when stderr
is redirected. `NO_COLOR`, `CI`, and `GATE_NO_INDICATOR=1` disable them.
When an activity phase completes after it was displayed, gate keeps a completed
line so later output still shows which long-running steps finished. Failed,
cancelled, or prompt-handoff phases clear the active line instead.

Text styling is enabled for terminals by default. `NO_COLOR=1` disables styling,
`FORCE_COLOR=1` or `CLICOLOR_FORCE=1` forces styling for non-TTY output, and
`CLICOLOR=0` disables default terminal styling unless a force variable is set.
`NO_COLOR` always wins. Force variables affect styling only; they do not force
activity indicators.

Examples:

```bash
gate up --json
gate down --json
gate ls --json
gate port -a --json
gate env web --json
gate daemon status --json
gate doctor --json
gate add web 3000 --json
gate rm web --json
gate clear -y --json
gate prune --json
gate expose web --via local --json
gate expose ls --json
gate expose stop web --via cloudflared --json
```

CI and automation scripts should combine JSON output with explicit state and
non-interactive output controls:

```bash
CI=1 NO_COLOR=1 GATE_NO_INDICATOR=1 gate --isolated-root .gate-agent env --up web --json
```

Use an explicit `--up` in CI when the script expects route allocation or daemon
state to be prepared as part of the command. Without `--up`, `env --json` is a
read-only descriptor lookup.

## Access From Another Device

Access from another device needs two separate pieces:

1. The other device must resolve the domain to a reachable address.
2. The other device must trust gate's root CA if you want a clean HTTPS page.

Export the root CA certificate:

```bash
gate ca export --out gate-root.crt
```

Install `gate-root.crt` on the other device as a trusted root certificate.

> [!IMPORTANT]
> Do not copy or share `root.key`; only export or share the `.crt` file.

### Same Machine

For browser access on the same machine, use `.localhost` domains when possible:

```toml
[project]
name = "myapp"
base = "app.localhost"

[services.web]
host = "."
```

Then:

```bash
gate trust
gate up -d
gate run web -- pnpm dev
```

Open:

```text
https://app.localhost
```

### LAN

Use LAN access when a phone, tablet, or another computer on the same network
must reach your dev server.

Prerequisites:

- Start gate routes first with `gate up -d`.
- Start the dev server, usually with `gate run <service> -- ...`.
- Install the exported gate root CA on other devices if you want trusted HTTPS.
- Make sure the other device can resolve the `.local` name to the development
  machine. If name resolution does not work on your network, add a hosts entry
  or use another local DNS mechanism.

Limitations:

- LAN exposure uses a `.local` name. By default, gate derives that name from the
  service domain: `.local` domains stay unchanged, `.localhost` becomes
  `.local`, and all other domains append `.local`.
- `gate expose <service> --via lan --domain <name.local>` overrides the derived
  LAN name for one exposure.
- The current LAN provider does not itself advertise mDNS or edit other devices'
  DNS/hosts files. It validates the domain and marks the running gate route as
  exposed.
- Devices must be on a network path that can reach the development machine.
- The HTTPS listener must not be loopback-only. The default `:443` listener is
  reachable on host interfaces; a daemon started with `127.0.0.1:<port>` must be
  restarted on `:<port>` or a LAN interface address before LAN exposure.
- Non-default listener ports are included in the LAN URL.
- Browser trust still depends on installing the gate root CA on each client
  device.

Example `gate.toml`:

```toml
[project]
name = "myapp"
base = "app.example.com"

[services.web]
host = "."
```

Start the proxy and service:

```bash
gate trust
gate up -d
gate run web -- pnpm dev
```

Expose the route for LAN clients:

```bash
gate expose web --via lan
```

This exposes `https://app.example.com.local` and forwards it to the primary
route `app.example.com`. On another device, make sure that `.local` name
resolves to the development machine, then open:

```text
https://app.example.com.local
```

If needed, find the development machine's LAN IP with your OS network settings
and map the name manually on the other device:

```text
192.168.0.42 app.example.com.local
```

### Public URL With Cloudflared

Use this when you want a temporary public URL. `cloudflared` must be installed
and available in `PATH`.

Prerequisites:

- Install [`cloudflared`](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/).
- Make sure `cloudflared` is available in `PATH`.
- Allow outbound internet access from the development machine.
- Start gate routes first with `gate up -d`.
- Start the dev server, usually with `gate run <service> -- ...`.
- Run `gate trust` on the development machine if `cloudflared` needs to trust
  gate's local HTTPS origin.

No Cloudflare account, zone, DNS record, or tunnel config file is required for
this quick-tunnel mode.

Example:

```bash
gate expose web --via cloudflared --auth user:pass
```

The auth secret is session-scoped. `exposures.json` records only that auth is
enabled, not the password. After a new gate process loses that secret, route
reload omits the protected origin instead of publishing it without auth.
`gate expose ls` reports `auth_status: "missing"`; run
`gate expose web --via cloudflared --auth user:pass` again to restore access.

gate requires the selected listener daemon to be running. For authenticated
exposure, it installs the guarded route before starting `cloudflared`; a
provider or persistence failure restores the previous route and exposure
state.

The command starts a quick tunnel to the active listener URL (including a
non-default HTTPS port) and prints a
`trycloudflare.com` URL:

```text
web exposed via cloudflared
  https://random-name.trycloudflare.com -> app.localhost
```

For an intentionally unauthenticated public URL, pass `--no-auth`:

```bash
gate expose web --via cloudflared --no-auth
```

> [!IMPORTANT]
> With `--no-auth`, anyone with the public URL can reach your dev server.

List exposure records:

```bash
gate expose ls
gate expose ls --all --json
```

Stop one exposure after provider teardown succeeds:

```bash
gate expose stop web --via cloudflared
```

Use `--force` only to forget stale local exposure records when the provider state
is already gone or must be cleaned up manually.

Limitations:

- The URL is temporary and random.
- The URL is not tied to your own domain.
- The tunnel lasts only while the `cloudflared` process keeps running.
- The tunnel targets gate's local HTTPS origin. If origin certificate trust fails,
  run `gate trust` on the development machine and retry.
- This mode is not a stable production tunnel configuration.

### Tailnet With Tailscale

Use this when devices are on the same Tailscale tailnet. `tailscale` must be
installed, logged in, and available in `PATH`.

Prerequisites:

- Install [Tailscale](https://tailscale.com/download).
- Log the development machine into a tailnet.
- Make sure target devices are also in the same tailnet, or otherwise allowed by
  your tailnet policy.
- Enable/use [Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve)
  support for the machine.
- Start gate routes first with `gate up -d`.
- Start the dev server, usually with `gate run <service> -- ...`.

gate requires the selected listener daemon to be running. The provider targets
gate through loopback, so enabling Tailscale does not make the gate listener
directly reachable from other non-loopback clients.

Limitations:

- Access is limited to devices allowed by the tailnet and ACLs.
- The current implementation uses `tailscale serve --bg`.
- gate allows one gate-managed Tailscale exposure at a time and assigns it a
  dedicated non-443 HTTPS port. Stop uses `tailscale serve --https=<port> off`,
  so unrelated Tailscale Serve ports are preserved; gate never resets the
  machine-wide Serve configuration.

```bash
gate expose web --via tailscale
```

This runs
`tailscale serve --bg --https=<serve-port> https+insecure://<service-domain>`
and prints the Tailscale Serve URL for the current machine. The Tailscale
listener uses a non-443 HTTPS port so the local gate URL remains usable on this
machine.

```text
web exposed via tailscale
  https://my-mac.tail6c50d7.ts.net:10443 -> web.myapp.localhost
```

Open the Tailscale URL from another device in the same tailnet:

```text
https://my-mac.tail6c50d7.ts.net:10443
```

gate also reloads a route alias for the Tailscale URL host so host-routed
services continue to resolve to the exposed service.

Stop the exposure with gate:

```bash
gate expose stop web --via tailscale
```

For safety, gate queries `tailscale serve status --json` and disables the
recorded HTTPS port only when its current root handler exactly matches the
target gate created. If ownership is unclear, gate preserves the record and
handler; `--force` explicitly disables only that recorded HTTPS port. Other
Tailscale Serve ports and handlers are not reset.

### Expose Command Reference

Supported providers:

| provider      | purpose              | notes                                          |
| ------------- | -------------------- | ---------------------------------------------- |
| `local`       | no external exposure | returns the local HTTPS URL                    |
| `lan`         | same-network access  | uses a derived or overridden `.local` LAN name |
| `cloudflared` | temporary public URL | requires `cloudflared`                         |
| `tailscale`   | tailnet access       | requires `tailscale`                           |

`gate expose ls` reports provider runtime state as `live`, `down`, or
`unverified`. `unverified` means gate has a local exposure record but cannot
prove the external provider is currently serving it.

The `AUTH` column reports whether a persisted exposure expects session-scoped
basic auth to still be present in the running route table:

| value     | meaning                                                               |
| --------- | --------------------------------------------------------------------- |
| `off`     | the exposure does not require basic auth                              |
| `active`  | auth is enabled and the daemon/session route still has the secret     |
| `missing` | the exposure was recorded with auth, but the in-memory secret is gone |

If auth is `missing`, rerun `gate expose ... --auth user:pass` for that service.

`gate expose` targets a scoped service/name:

```bash
gate expose <service> --via <provider>
gate expose -g <name> --via <provider>
gate expose -p <project> <service> --via <provider>
```

Use the `local` provider when you want an exposure record and URL without
starting an external tunnel:

```bash
gate expose web --via local
```

## CA Export

Export the root CA certificate for another device:

```bash
gate ca export --out gate-root.crt
```

## Upgrade

```bash
gate upgrade
```

When a newer release is available, gate shows the current and latest versions
and asks whether to upgrade.
If the running `gate` binary is Homebrew-managed, `gate upgrade` uses
`brew upgrade gate`; otherwise it runs the standalone installer.
During installation gate shows a single status indicator and hides installer
logs unless the install command fails.
After a successful upgrade, gate restarts any daemons that were running before
the upgrade and automatically runs `doctor`. Any remaining issues are reported
in the upgrade output with the matching `gate doctor --fix` repair hint, but
they do not turn a successful upgrade into an upgrade failure. Daemon restart
failures are reported as warnings with the manual `gate daemon restart` or
`gate daemon stop` / `gate up -d` recovery command. If gate is already up to
date, it prints the up-to-date status and exits without restarting daemons or
running `doctor`.

On Linux, a gate-managed upgrade checks whether the current executable has
low-port access before replacement. Standalone upgrades restore the previous
binary if capability preservation fails. Homebrew-managed upgrades configure
and verify the replacement before version verification and daemon restart; on
failure, gate stops and prints the explicit `gate daemon setup` recovery path.
Upgrades performed directly by a package manager are outside gate's
transaction, so rerun setup afterward when the default listener reports a
permission failure.

Skip confirmation:

```bash
gate upgrade -y
```

## Completion

```bash
gate completion bash
gate completion zsh
gate completion fish
```

Completion is read-only. It reads local registry state, nearby `gate.toml`
files, and explicit `--config` paths when available, but it does not start
daemons, edit DNS, trust certificates, or write project/config files. Broken or
missing local state returns no candidates instead of noisy shell errors.
Candidates use a stable task-oriented order.

Installed completion offers:

- command/action candidates: root commands, `daemon status|start|stop|restart|logs`,
  `ca export`, `expose ls|stop`, `skill path|print`, and
  `completion bash|zsh|fish`
- flag candidates: `--<tab>` shows long flags and `-<tab>` shows short flags for
  the current command or subcommand, including common `-h|--help`
- scope candidates: `--config`, `-g|--global`, `-p|--project`, and `-a|--all`
  where that command supports them; `--project <tab>` lists registry project
  names, or the selected config's project name when `--config` is present
- file candidates: file paths for `--config` and other path-valued flags such
  as `ca export --out`
- service/name candidates: scoped service names for `add`, `rm`, `run`, `port`,
  and `expose`; inside a project the default scope is the current project,
  outside a project it is global
- enum values: `ls --route` completes `active|inactive`, `ls --upstream`
  completes `live|down`, `up --dns` completes `localhost|hosts`, and
  `expose --via` completes
  `local|lan|cloudflared|tailscale`
- file paths only where meaningful, such as `ca export --out`

On Linux, daemon completion additionally offers `setup` and its
`--check`, `--yes`, and `--json` flags. It is omitted on macOS.

Completion stops offering gate arguments after `gate run <service> --`, because
everything after `--` belongs to the child command.

## Agent Skill

Print the path to the bundled agent skill:

```bash
gate skill path
```

Print the bundled skill contents:

```bash
gate skill print
```

## Uninstall

Remove gate's local state, trust entry, managed hosts/PATH blocks, and known
binaries:

```bash
gate uninstall
```

Non-interactive:

```bash
gate uninstall -y
```

If the running `gate` binary is Homebrew-managed, `gate uninstall` runs
`brew uninstall gate` as its final step. Use `--keep-brew` to leave the
Homebrew package installed. Use `--keep-trust` to leave trust store entries in
place.

If the `gate` binary is already gone, use the standalone uninstall script:

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/uninstall.sh | sh
```

Non-interactive:

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/uninstall.sh | sh -s -- -y
```

> [!NOTE]
> The uninstall script removes user-level config, data, state, and known binary
> paths that exist on the machine. Before deleting local CA data, it attempts to
> remove gate's trusted root CA from OS/browser trust stores. Use `--keep-trust`
> to leave trust store entries in place. Homebrew-managed symlinks are skipped,
> so the script does not remove the Homebrew package itself.

Linux low-port access is a file capability on the installed gate binary.
Deleting or replacing that binary removes the capability with it; uninstall
also removes any root-owned temporary helper left by an interrupted setup.
Standalone uninstall holds the adjacent installation lock while removing the
binary and any interrupted replacement transaction. The empty user-owned lock
file remains so a concurrent installer can never switch to a new lock inode.
There is no service, sudo policy, or system-wide port setting to remove.

The built-in uninstaller blocks routes and stops persisted external exposures,
then stops every known daemon before deleting state. Any stop failure aborts
file deletion. With `GATE_ISOLATED_ROOT`, uninstall never edits the shared
`/etc/hosts` block.

If local CA data still exists and the `gate` binary is gone, standalone
uninstall stops before deleting that CA because it cannot verify trust-store
cleanup. Reinstall gate or provide a trusted `GATE_BIN` first, then rerun; use
`--keep-trust` only when intentionally retaining the trust-store entry after
local gate files are removed.

Legacy single-daemon cleanup, for pre-scoped development builds:

```bash
gate doctor --fix
```

## Exit Codes

| code | meaning                                 |
| ---- | --------------------------------------- |
| 0    | success                                 |
| 1    | error                                   |
| 2    | usage error                             |
| 3    | permission required                     |
| 4    | state, ownership, exposure, port, domain, or listener conflict |
