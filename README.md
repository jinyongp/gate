# gate

Local HTTPS reverse proxy and port registry for local development.

> [!NOTE]
> gate is a local development tool for developer machines. It is intended for
> testing local services, not for production traffic or hosted environments.

## Install

Using Homebrew:

```bash
brew install jinyongp/tap/gate
```

Or using the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/install.sh | sh
```

Supported platforms: macOS and Linux (darwin, linux) on arm64 and amd64.

> [!TIP]
> The install script writes `gate` to `~/.local/bin` by default. If that
> directory is not in `PATH`, the installer offers to update your shell startup
> file and also prints the exact line you can add manually.

For full usage, see [docs/usage.md](docs/usage.md). For detailed setup notes
and internals, see [docs/spec.md](docs/spec.md).

## Upgrade

Using Homebrew:

```bash
brew update
brew upgrade gate
```

Or using gate's built-in upgrade command:

```bash
gate upgrade
```

This updates gate to the latest GitHub release.
If the running `gate` binary is Homebrew-managed, `gate upgrade` uses
`brew upgrade gate`; otherwise it runs the standalone installer.
During installation gate shows a single status indicator and hides installer
logs unless the install command fails.

After upgrading from a development build, check for stale local state:

```bash
gate doctor
gate doctor --fix
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

```bash
# non-interactive
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/uninstall.sh | sh -s -- -y
```

The script first attempts to remove gate's trusted root CA from OS/browser trust
stores, then removes only files and directories it discovers on the current
machine. If privileged setup artifacts were never created, they are not removed.
Homebrew-managed symlinks are skipped by the script, so it does not remove the
Homebrew package itself.

## Quick Start

Run this inside your app repository.

1. Trust gate's local HTTPS certificate authority once:

   ```bash
   gate trust
   ```

   This may ask for administrator approval.

2. Create `gate.toml`:

   ```bash
   gate init
   ```

   For a non-interactive default:

   ```bash
   gate init -y
   ```

   Or edit the generated file to add more services:

   ```toml
   [project]
   name = "my-project"

   [services.web]
   domain = "web.my-project.localhost"

   [services.api]
   domain = "api.my-project.localhost"
   port = 3001
   ```

3. Start gate and run your dev server through the reserved port:

   ```bash
   gate up -d
   gate run web -- pnpm dev
   ```

   Replace `pnpm dev` with your app's dev-server command. `gate run` injects the
   reserved port as `PORT`.

4. Open:

   ```text
   https://web.my-project.localhost
   ```

   Use the route printed by `gate up`; if your project name or domain differs,
   the URL differs too.

`.localhost` domains need no DNS setup. Custom domains need `/etc/hosts` or
another local DNS setup, so `gate up` may ask for administrator approval.

Gate daemons are scoped. `gate up -d` starts or reloads the current project's
daemon, while global reservations are served by the global daemon. Use
`gate daemon status --all` to inspect every known daemon.

Global reservations are machine-local mappings without `gate.toml`:

```bash
gate add -g web web.localhost 3000
gate daemon start -g
gate run -g web -- pnpm dev
gate rm -g web
```

Registry commands use the current project by default when `gate.toml` is
discoverable, and the global scope otherwise. Use `-g`/`--global` for global
reservations or `-p`/`--project <name>` for a named project. `gate rm` removes by
service/name. Use `gate clear -y`, `gate clear -g -y`, or
`gate clear -p <project> -y` to remove every reservation in one scope.

### Environment-backed config

`domain` and `port` can read environment variables when a team needs
per-developer values:

```toml
[project]
name = "my-project"
env_files = [".env.local", ".env"]

[services.web]
domain = "${WEB_DOMAIN:-app.example.localhost}"
port = "${WEB_PORT:-3000}"

[services.api]
domain = "api.${BASE_DOMAIN:-example.localhost}"
port = "${API_PORT}"
```

`env_files` are resolved relative to `gate.toml`. Missing files are ignored, so
session environment variables still work without a local dotenv file. Process
environment values win over dotenv values, and earlier dotenv files win over
later ones. `${NAME}` is required and fails if unset; `${NAME:-default}` uses
the default when unset or empty.

## Contributing

Development uses the repository checkout and the `just` command runner.

Prerequisites:

- Go
- [`just`](https://github.com/casey/just)

Development recipes fetch Go tools such as `golangci-lint`, `govulncheck`, and
`goimports` with `go run`, so they do not need separate manual installation.

Set up a checkout:

```bash
git clone https://github.com/jinyongp/gate.git
cd gate
just build
```

Run from source:

```bash
just gate --help
just gate ls
```

Run validation before opening a pull request:

```bash
just check
```

Useful development commands:

```bash
just fmt
just test
just lint-json
just vuln
```

Local smoke tests are available for contributors:

```bash
just smoke check
just smoke serve
just smoke route
```

Those smoke recipes build the local `bin/gate` binary from this checkout, run
the sample app through gate on random front-proxy ports, and clean up their
temporary gate state when they exit. For full command usage, see
[docs/usage.md](docs/usage.md).
