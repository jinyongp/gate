# gate

Local HTTPS reverse proxy and port registry for local development.

> [!WARNING]
> gate is a local development tool and is still under active development. It is intended for testing local services, not for production traffic or hosted environments.

## Install

### Agent bootstrap (recommended)

Open the agent setup instructions directly:

```
https://raw.githubusercontent.com/jinyongp/gate/main/scripts/agent-bootstrap.md
```

### Manual install

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/gate/main/scripts/install.sh | sh
```

Supported platforms: macOS and Linux (darwin, linux) on arm64 and amd64.

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

## Development Quick Start

1. Run a built-in smoke server through the development binary:

```bash
just hello-go
```

or:

```bash
just hello-js
```

Custom-domain JS smoke:

```bash
just hello-js-custom
```

The default smoke recipes use the spec default front-proxy ports, HTTPS `:443` and HTTP `:80`, so the URLs have no port.
The upstream dev-server ports are still allocated by gate and injected into the child process with `gate run`.
If `:443`/`:80` are unavailable, the recipe prints the owning process. Stop that process before running the custom-domain recipe.
For a quick smoke that avoids `:443`, use `just hello-go-port` or `just hello-js-port`; those recipes use random front-proxy ports and print URLs with ports.
The `.localhost` recipes do not need sudo for DNS. The custom-domain recipe adds `hello-js.test` to `/etc/hosts` inside a dedicated `<gate:hello-js-custom-hosts>` block, so sudo may ask for your password once.
Remove that custom-domain hosts entry with `just hello-js-custom-clean`.

2. Open:

```bash
https://hello-go.localhost
# or
https://hello-js.localhost
# or custom-domain JS
https://hello-js.test
```

Use `Ctrl-C` to stop the sample server.

The first browser visit may show `ERR_CERT_AUTHORITY_INVALID`. That means the local gate CA is not trusted yet. For smoke testing, use the browser's advanced/proceed flow. To install trust, run the checkout-local `bin/gate trust` command; that may require OS administrator approval.

3. To remove the browser certificate warning, trust the local CA:

```bash
bin/gate trust
```

This installs gate's local CA into the OS/browser trust store. It may ask for administrator approval. Restart the browser if the warning remains.

4. For your own project, add `gate.toml` in its root — run the checkout-local binary with `init` to scaffold one interactively, or use `init -y` for defaults:

```bash
/path/to/gate/bin/gate init
# or non-interactive default
/path/to/gate/bin/gate init -y
```

You can also write it manually:

```toml
[project]
name = "my-project"

[services.web]
domain = "app.example.localhost"

[services.api]
domain = "api.example.localhost"
port = 3001
```

`domain` and `port` can read environment variables:

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

5. Start routing and run the service:

```bash
/path/to/gate/bin/gate up --daemon
/path/to/gate/bin/gate run web -- pnpm dev
```

6. Open:

```bash
https://app.example.localhost
https://api.example.localhost
```

7. Custom domain example:

```toml
[project]
name = "my-project"

[services.web]
domain = "app.example.test"
```

Then:

```bash
/path/to/gate/bin/gate trust
/path/to/gate/bin/gate up --daemon
/path/to/gate/bin/gate run web -- pnpm dev
```

Custom domains are not automatic like `.localhost`. They need `/etc/hosts` or another local DNS setup, so `up` may require administrator approval. TLS still needs `trust`.

8. Check status:

```bash
/path/to/gate/bin/gate ls
/path/to/gate/bin/gate daemon status
/path/to/gate/bin/gate down
```

## Common commands

```bash
just hello-go
just hello-js
just hello-js-custom
bin/gate up
bin/gate up --daemon
bin/gate run web -- pnpm dev
bin/gate ls
bin/gate daemon status
bin/gate upgrade
bin/gate down
```

For full usage and all options, run `bin/gate --help`.
