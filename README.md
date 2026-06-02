# gate

Local HTTPS reverse proxy and port registry for local development.

> [!WARNING]
> gate is a local development tool and is still under active development. It is intended for testing local services, not for production traffic or hosted environments.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/install.sh | sh
```

Supported platforms: macOS and Linux (darwin, linux) on arm64 and amd64.

The installer writes `gate` to `~/.local/bin` by default. If that directory is
not in `PATH`, the installer offers to update your shell startup file and also
prints the exact line you can add manually.

For full usage, see [docs/usage.md](docs/usage.md). For detailed setup notes
and internals, see [docs/spec.md](docs/spec.md).

## Upgrade

```bash
gate upgrade
```

This updates gate to the latest GitHub release.

## Uninstall

Removes user-level config/data/state and binaries.

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/uninstall.sh | sh
```

```bash
# non-interactive
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/uninstall.sh | sh -s -- -y
```

The script removes only files and directories it discovers on the current machine. If privileged setup artifacts were never created, they are not removed.

By default it asks for confirmation before removing files.
Use `-y` to skip it in automation.

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
domain = "app.example.localhost"

[services.api]
domain = "api.example.localhost"
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

Use the route printed by `gate up`. If you edited the generated config to match
the example above, open `https://app.example.localhost` instead.

`.localhost` domains need no DNS setup. Custom domains need `/etc/hosts` or
another local DNS setup, so `gate up` may ask for administrator approval.

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
just hello-go-port
just hello-js-port
```

Those recipes build the local `bin/gate` binary from this checkout and run
sample apps through gate on random front-proxy ports. For full command usage, see
[docs/usage.md](docs/usage.md).
