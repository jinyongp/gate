# gate usage

`gate` maps local domains to local dev servers over HTTPS and keeps a
machine-wide registry of domain and port reservations.

Use project mode when a repository should carry its routing in `gate.toml`.
Use standalone mode when you want a machine-local mapping without adding a
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

## Standalone Mode

Standalone mode creates machine-local mappings without `gate.toml`. It is useful
when you already know the domain and port and do not want a project file.

Add a mapping:

```bash
gate add web.localhost 3000
```

Standalone reservations are served by the global daemon. If the global daemon is
running, routes are hot-reloaded. If it is stopped, starting it later loads
active standalone routes from the registry:

```bash
gate daemon start --global
```

Run a dev server through the standalone reservation:

```bash
gate run web.localhost -- pnpm dev
```

Or use the port in your own command:

```bash
PORT=$(gate port web.localhost) pnpm dev
```

Open:

```text
https://web.localhost
```

Remove the standalone mapping:

```bash
gate rm web.localhost
```

Standalone mode is local-machine routing. `gate expose` currently works with
project services, not standalone domains.

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

One service or standalone domain:

```bash
gate port web
gate port web.localhost
```

## Manage Reservations

Add a domain and fixed port:

```bash
gate add web.localhost 3000
```

Inside a project, this appends a `[services.<name>]` block to `gate.toml` and
updates the registry. Outside a project, this creates a standalone registry
reservation.

Remove a domain:

```bash
gate rm web.localhost
```

Inside a project, if the domain belongs to a service in `gate.toml`, that service
block is removed and the registry is updated. Otherwise, the matching registry
reservation is removed.

Remove all reservations for the current project:

```bash
gate rm --project
```

Remove all reservations for a named project:

```bash
gate rm --project myapp
```

Prune stale reservations whose owning `gate.toml` no longer exists:

```bash
gate prune
```

Standalone reservations are not pruned by `gate prune` because they have no
owning config file.

## Daemon

Daemons are scoped. Inside a project, daemon commands target that project's
daemon by default. Outside a project, they target the global daemon for
standalone reservations.

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
gate add web.localhost 3000 --json
gate rm web.localhost --json
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
- `gate expose` currently works with project services, not standalone domains.
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
- `gate expose` currently works with project services, not standalone domains.
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

- `gate expose` currently works with project services, not standalone domains.
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

`gate expose` currently requires a project service name:

```bash
gate expose <service> --via <provider>
```

It does not currently accept standalone domains directly.

## CA Export

Export the root CA certificate for another device:

```bash
gate ca export --out gate-root.crt
```

## Upgrade

```bash
gate upgrade
```

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

## Uninstall

Homebrew install:

```bash
brew uninstall gate
```

Install-script install:

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
> to leave trust store entries in place. Homebrew-managed symlinks are skipped;
> use `brew uninstall gate` for Homebrew installs.

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
