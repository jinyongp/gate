# gate usage

`gate` maps local domains to local dev servers over HTTPS and keeps a
machine-wide registry of domain and port reservations.

Use project mode when a repository should carry its routing in `gate.toml`.
Use global reservations when you want a machine-local mapping without adding a
project file.

## Quick Reference

Use `gate --help` for the root command list, or `gate <command> --help` for one
command's flags and positional arguments.

| command | purpose |
| --- | --- |
| `gate init [--name name] [--force] [-y\|--yes] [--json]` | scaffold a starter `gate.toml` |
| `gate up [-d\|--daemon] [--dns localhost\|hosts] [-g\|--global] [-p name\|--project name] [--json]` | reserve ports, activate routes, reflect DNS, and optionally start the daemon |
| `gate down [-g\|--global] [-p name\|--project name] [--json]` | deactivate scoped routes while keeping reservations |
| `gate ls [--route active\|inactive] [--upstream live\|down] [-g\|--global] [-p name\|--project name] [-a\|--all] [--json]` | list reservations with route and upstream status |
| `gate port [-g\|--global] [-p name\|--project name] [-a\|--all] [service] [--json]` | print one service port or list reserved ports |
| `gate run [-g\|--global] [-p name\|--project name] <service> -- <cmd...>` | run a child command with `PORT` injected |
| `gate add [-g\|--global] [-p name\|--project name] [--host host] [--domain domain] <service> <port> [--json]` | add or update one reservation |
| `gate rm [-g\|--global] [-p name\|--project name] <service> [--json]` | remove one reservation |
| `gate clear [-g\|--global] [-p name\|--project name] [-y\|--yes] [--json]` | remove all reservations in one scope |
| `gate prune [--json]` | remove stale project reservations whose config file is gone |
| `gate daemon status [-a\|--all] [--json]` | inspect listener daemon status |
| `gate daemon start` | start or reuse the default listener daemon |
| `gate daemon stop [-a\|--all]` | stop listener daemon(s) |
| `gate daemon restart` | restart the default listener daemon |
| `gate daemon logs [-a\|--all]` | print listener daemon logs |
| `gate trust` | install the local root CA into OS/browser trust stores |
| `gate untrust` | remove the local root CA from trust stores |
| `gate ca export [--out path]` | export the local root certificate |
| `gate doctor [--fix] [--json]` | check and repair gate-owned local state |
| `gate expose [--via local\|lan\|cloudflared\|tailscale] [--domain name.local] [--auth user:pass] [--no-auth] [-g\|--global] [-p name\|--project name] <service> [--json]` | expose a scoped service through a provider |
| `gate expose ls [--via provider] [-g\|--global] [-p name\|--project name] [-a\|--all] [--json]` | list exposure records |
| `gate expose stop [--via provider] [--force] [-g\|--global] [-p name\|--project name] <service> [--json]` | stop or forget one exposure record |
| `gate upgrade [-y\|--yes]` | upgrade to the latest release, then run doctor |
| `gate completion bash\|zsh\|fish` | print shell completion script |
| `gate skill path\|print` | locate or print the bundled agent skill |
| `gate uninstall [--keep-trust] [--keep-brew] [-y\|--yes]` | remove gate state, binaries, and Homebrew package when applicable |

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/install.sh | sh
```

> [!TIP]
> The installer writes `gate` to `~/.local/bin` by default. If that directory is
> not in `PATH`, the installer offers to update your shell startup file and
> prints the exact line you can add manually.

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

## Project Mode

Project mode uses a `gate.toml` file in the repository. This is the shareable,
repeatable path for a team.

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
```

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

`gate run` also injects peer service values into the child process:
`GATE_<SERVICE>_PORT`, `GATE_<SERVICE>_URL`,
`GATE_<SERVICE>_ROUTE_URL`, and any service-declared env names such as
`API_URL`. Loopback URLs use `http://127.0.0.1:<port>`.

Open:

```text
https://app.localhost
```

Stop routing for the current project while keeping reservations:

```bash
gate down
```

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

Prune stale reservations whose owning `gate.toml` no longer exists:

```bash
gate prune
```

Global reservations are not pruned by `gate prune` because they have no
owning config file.

## Daemon

Daemon processes are keyed by listener address pair. The default listener is
HTTPS `:443` and HTTP `:80`, so one default daemon serves active routes from all
projects and global reservations that target that listener.

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

## JSON Output

Commands that support `--json` usually write a single JSON object to stdout.
Commands that target multiple listener daemons, such as `gate daemon status --all
--json`, write a JSON array. Errors in JSON mode are written to stderr as an
error envelope.

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
enabled, not the password. If a later route reload reports the auth secret as
missing, run `gate expose web --via cloudflared --auth user:pass` again.

The command starts a quick tunnel to `https://<service-domain>` and prints a
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

Limitations:

- Access is limited to devices allowed by the tailnet and ACLs.
- The current implementation uses `tailscale serve --bg` and `tailscale serve
  reset`. Resetting Tailscale Serve affects the machine's Serve configuration.
- gate allows one Tailscale exposure at a time because Tailscale Serve reset is
  machine-wide.

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
  https://my-mac.tail6c50d7.ts.net:10443 -> local.stamp.is
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

For safety, gate only resets Tailscale Serve by default when the exposure record
matches a command gate created. If the record is stale or ownership is unclear,
pass `--force` to reset Tailscale Serve anyway and forget the record.
Because Tailscale Serve reset is machine-wide, stopping a Tailscale exposure
also forgets other Tailscale exposure records if stale records are present.

### Expose Command Reference

Supported providers:

| provider | purpose | notes |
| --- | --- | --- |
| `local` | no external exposure | returns the local HTTPS URL |
| `lan` | same-network access | uses a derived or overridden `.local` LAN name |
| `cloudflared` | temporary public URL | requires `cloudflared` |
| `tailscale` | tailnet access | requires `tailscale` |

`gate expose ls` reports provider runtime state as `live`, `down`, or
`unverified`. `unverified` means gate has a local exposure record but cannot
prove the external provider is currently serving it.

The `AUTH` column reports whether a persisted exposure expects session-scoped
basic auth to still be present in the running route table:

| value | meaning |
| --- | --- |
| `off` | the exposure does not require basic auth |
| `active` | auth is enabled and the daemon/session route still has the secret |
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
they do not turn a successful upgrade into an upgrade failure. If gate is
already up to date, it prints the up-to-date status and exits without restarting
daemons or running `doctor`.

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

Completion is read-only. It reads local registry state and nearby `gate.toml`
files when available, but it does not start daemons, edit DNS, trust
certificates, or write project/config files. Broken or missing local state
returns no candidates instead of noisy shell errors. Candidates use a stable
task-oriented order.

Installed completion offers:

- command/action candidates: root commands, `daemon status|start|stop|restart|logs`,
  `ca export`, `expose ls|stop`, `skill path|print`, and
  `completion bash|zsh|fish`
- flag candidates: `--<tab>` shows long flags and `-<tab>` shows short flags for
  the current command or subcommand, including common `-h|--help`
- scope candidates: `-g|--global`, `-p|--project`, and `-a|--all` where that
  command supports them; `--project <tab>` lists registry project names
- service/name candidates: scoped service names for `add`, `rm`, `run`, `port`,
  and `expose`; inside a project the default scope is the current project,
  outside a project it is global
- enum values: `ls --route` completes `active|inactive`, `ls --upstream`
  completes `live|down`, `up --dns` completes `localhost|hosts`, and
  `expose --via` completes
  `local|lan|cloudflared|tailscale`
- file paths only where meaningful, such as `ca export --out`

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

Legacy single-daemon cleanup, for pre-scoped development builds:

```bash
gate doctor --fix
```

## Exit Codes

| code | meaning |
| --- | --- |
| 0 | success |
| 1 | error |
| 2 | usage error |
| 3 | permission required |
| 4 | port, domain, or daemon-listen conflict |
