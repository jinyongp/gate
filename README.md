# prx

Local HTTPS reverse proxy and port registry for local development.

## Install

### AI Agent bootstrap (recommended)

Open the AI agent setup instructions directly:

```
https://raw.githubusercontent.com/jinyongp/prx/main/scripts/install-with-agent.md
```

### Human install

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/prx/main/scripts/install.sh | sh
```

Supported platforms: macOS and Linux (darwin, linux) on arm64 and amd64.

For detailed setup notes and internals, see [docs/README.md](/Users/jinyongp/Workspaces/github/jinyongp/prx/docs/README.md).

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

## Quick start

1. Add `prx.toml` in your project root.

```toml
[project]
name = "my-project"

[services.web]
domain = "app.example.localhost"

[services.api]
domain = "api.example.localhost"
port = 3001
```

2. Start routing:

```bash
prx up
```

3. Open:

```bash
https://app.example.localhost
https://api.example.localhost
```

4. Check status:

```bash
prx ls
prx daemon status
prx down
```

## Common commands

```bash
prx up
prx run web -- pnpm dev
prx ls
prx daemon status
prx upgrade
prx down
```

For full usage and all options, run `prx --help`.
