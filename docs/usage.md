# gate usage

`gate` maps local domains to local dev servers over HTTPS and keeps a
machine-wide registry of domain and port reservations.

Use project mode when a repository should carry its routing in `gate.toml`.
Use global reservations when you want a machine-local mapping without adding a
project file.

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

Non-interactive default:

```bash
gate init -y
```

Example `gate.toml`:

```toml
[project]
name = "myapp"

[services.web]
domain = "app.localhost"

[services.api]
domain = "api.localhost"
port = 3001
```

Bring the project up and start the daemon:

```bash
gate up -d
```

Run a dev server with its reserved port injected as `PORT`:

```bash
gate run web -- pnpm dev
```

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
gate add -g web web.localhost 3000
```

Global reservations are served by the global daemon. If the global daemon is
running, routes are hot-reloaded. If it is stopped, starting it later loads
active global routes from the registry:

```bash
gate up -g
gate daemon start -g
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

Only live or down reservations:

```bash
gate ls --status live
gate ls --status down
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
gate add web app.localhost 3000
```

Inside a project, this adds or updates the `[services.<name>]` block in
`gate.toml` and updates the registry.

Add a global reservation:

```bash
gate add -g web web.localhost 3000
```

Add a named project reservation:

```bash
gate add -p myapp web app.localhost 3000
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

Daemons are scoped. Inside a project, daemon commands target that project's
daemon by default. Outside a project, they target the global daemon.

Start, stop, restart, and inspect the current scoped proxy:

```bash
gate daemon start
gate daemon stop
gate daemon restart
gate daemon status
gate daemon logs
```

Control the global daemon from inside any project:

```bash
gate daemon status --global
gate daemon stop --global
```

Target a named project daemon:

```bash
gate daemon status --project myapp
```

Inspect or stop all known daemons:

```bash
gate daemon status --all
gate daemon stop --all
```

Start on custom front-proxy ports:

```bash
gate daemon start --https-addr 127.0.0.1:18443 --http-addr 127.0.0.1:18080
```

`gate up -d` starts the current project daemon when needed and reloads only that
project's routes.

## JSON Output

Commands that support `--json` usually write a single JSON object to stdout.
Commands that target multiple daemon scopes, such as `gate daemon status --all
--json`, write a JSON array. Errors in JSON mode are written to stderr as an
error envelope.

Some longer operations show a one-line activity indicator on stderr when stderr
is an interactive terminal. Indicators never appear in JSON mode or when stderr
is redirected. `NO_COLOR`, `CI`, and `GATE_NO_INDICATOR=1` disable them.

Text styling is enabled for terminals by default. `NO_COLOR=1` disables styling,
`FORCE_COLOR=1` or `CLICOLOR_FORCE=1` forces styling for non-TTY output, and
`CLICOLOR=0` disables default terminal styling unless a force variable is set.
`NO_COLOR` always wins. Force variables affect styling only; they do not force
activity indicators.

Examples:

```bash
gate up --json
gate ls --json
gate port -a --json
gate daemon status --json
gate add web app.localhost 3000 --json
gate rm web --json
gate clear -y --json
gate prune --json
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
[services.web]
domain = "app.localhost"
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

- Use a `.local` service domain.
- Start gate routes first with `gate up -d`.
- Start the dev server, usually with `gate run <service> -- ...`.
- Install the exported gate root CA on other devices if you want trusted HTTPS.
- Make sure the other device can resolve the `.local` name to the development
  machine. If name resolution does not work on your network, add a hosts entry
  or use another local DNS mechanism.

Limitations:

- `gate expose <service> --via lan` only accepts a `.local` domain.
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

[services.web]
domain = "myapp.local"
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

On another device, make sure `myapp.local` resolves to the development machine,
then open:

```text
https://myapp.local
```

If needed, find the development machine's LAN IP with your OS network settings
and map the name manually on the other device:

```text
192.168.0.42 myapp.local
```

### Public URL With Cloudflared

Use this when you want a temporary public URL. `cloudflared` must be installed
and available in `PATH`.

Prerequisites:

- Install [`cloudflared`](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/).
- Allow outbound internet access from the development machine.
- Start gate routes first with `gate up -d`.
- Start the dev server, usually with `gate run <service> -- ...`.
- Run `gate trust` on the development machine if `cloudflared` needs to trust
  gate's local HTTPS origin.

No Cloudflare account, zone, DNS record, or tunnel config file is required for
this quick-tunnel mode.

Example:

```bash
gate expose web --via cloudflared
```

The command starts a quick tunnel to `https://<service-domain>` and prints a
`trycloudflare.com` URL:

```text
web exposed via cloudflared
  https://random-name.trycloudflare.com -> app.localhost
```

For a public URL, prefer auth:

```bash
gate expose web --via cloudflared --auth user:pass
```

> [!IMPORTANT]
> Without `--auth`, anyone with the public URL can reach your dev server.

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
- The current implementation runs `tailscale serve --bg`; gate does not manage
  detailed Tailscale Serve state after that.
- Tear-down is manual with Tailscale commands.

```bash
gate expose web --via tailscale
```

This runs `tailscale serve --bg https://<service-domain>`. Tear it down with
Tailscale's serve controls, for example:

```bash
tailscale serve reset
```

### Expose Command Reference

Supported providers:

| provider | purpose | notes |
| --- | --- | --- |
| `local` | no external exposure | returns the local HTTPS URL |
| `lan` | same-network access | requires a `.local` service domain |
| `cloudflared` | temporary public URL | requires `cloudflared` |
| `tailscale` | tailnet access | requires `tailscale` |

`gate expose` targets a scoped service/name:

```bash
gate expose <service> --via <provider>
gate expose -g <name> --via <provider>
gate expose -p <project> <service> --via <provider>
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
returns no candidates instead of noisy shell errors. Candidates are
stable-sorted.

Installed completion offers:

- command/action candidates: root commands, `daemon start|stop|restart|status|logs`,
  `ca export`, `skill path|print`, and `completion bash|zsh|fish`
- flag candidates: `--<tab>` shows long flags and `-<tab>` shows short flags for
  the current command or subcommand, including common `-h|--help`
- scope candidates: `-g|--global`, `-p|--project`, and `-a|--all` where that
  command supports them; `--project <tab>` lists registry project names
- service/name candidates: scoped service names for `add`, `rm`, `run`, `port`,
  and `expose`; inside a project the default scope is the current project,
  outside a project it is global
- enum values: `ls --status` completes `live|down`, `up --dns` completes
  `localhost|hosts`, and `expose --via` completes
  `local|lan|cloudflared|tailscale`
- file paths only where meaningful, such as `ca export --out`

Completion stops offering gate arguments after `gate run <service> --`, because
everything after `--` belongs to the child command.

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
