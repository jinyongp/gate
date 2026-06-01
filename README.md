# prx

Local HTTPS reverse proxy and port registry for local development.

> [!WARNING]
> prx is still under active development. It is useful for local testing, but it is not ready for dependable day-to-day production-style development workflows yet.

## Install

### Agent bootstrap (recommended)

Open the agent setup instructions directly:

```
https://raw.githubusercontent.com/jinyongp/prx/main/scripts/agent-bootstrap.md
```

### Human install

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/prx/main/scripts/install.sh | sh
```

Supported platforms: macOS and Linux (darwin, linux) on arm64 and amd64.

For detailed setup notes and internals, see [docs/spec/plan.md](docs/spec/plan.md).

## Upgrade

```bash
prx upgrade
```

This updates prx to the latest GitHub release.

## Uninstall

Removes user-level config/data/state and binaries.

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/prx/main/scripts/uninstall.sh | sh
```

```bash
# non-interactive
curl -fsSL https://raw.githubusercontent.com/jinyongp/prx/main/scripts/uninstall.sh | sh -s -- -y
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
The upstream dev-server ports are still allocated by prx and injected into the child process with `prx run`.
If `:443`/`:80` are unavailable, the recipe prints the owning process. Stop that process before running the custom-domain recipe.
For a quick smoke that avoids `:443`, use `just hello-go-port` or `just hello-js-port`; those recipes use random front-proxy ports and print URLs with ports.
The `.localhost` recipes do not need sudo for DNS. The custom-domain recipe adds `hello-js.test` to `/etc/hosts` inside a dedicated `<prx:hello-js-custom-hosts>` block, so sudo may ask for your password once.
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

The first browser visit may show `ERR_CERT_AUTHORITY_INVALID`. That means the local prx CA is not trusted yet. For smoke testing, use the browser's advanced/proceed flow. To install trust, run the checkout-local `bin/prx trust` command; that may require OS administrator approval.

3. To remove the browser certificate warning, trust the local CA:

```bash
bin/prx trust
```

This installs prx's local CA into the OS/browser trust store. It may ask for administrator approval. Restart the browser if the warning remains.

4. For your own project, add `prx.toml` in its root — run the checkout-local binary with `init` to scaffold one, or write it manually:

```toml
[project]
name = "my-project"

[services.web]
domain = "app.example.localhost"

[services.api]
domain = "api.example.localhost"
port = 3001
```

5. Start routing and run the service:

```bash
/path/to/prx/bin/prx up --daemon
/path/to/prx/bin/prx run web -- pnpm dev
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
/path/to/prx/bin/prx trust
/path/to/prx/bin/prx up --daemon
/path/to/prx/bin/prx run web -- pnpm dev
```

Custom domains are not automatic like `.localhost`. They need `/etc/hosts` or another local DNS setup, so `up` may require administrator approval. TLS still needs `trust`.

8. Check status:

```bash
/path/to/prx/bin/prx ls
/path/to/prx/bin/prx daemon status
/path/to/prx/bin/prx down
```

## Common commands

```bash
just hello-go
just hello-js
just hello-js-custom
bin/prx up
bin/prx up --daemon
bin/prx run web -- pnpm dev
bin/prx ls
bin/prx daemon status
bin/prx upgrade
bin/prx down
```

For full usage and all options, run `bin/prx --help`.
