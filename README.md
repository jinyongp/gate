# prx

Local HTTPS reverse proxy and port registry for local development.

## Install

### AI Agent bootstrap (recommended)

Open the agent setup instructions directly:

```
https://raw.githubusercontent.com/jinyongp/prx/main/scripts/install-with-agent.md
```

### Human install

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/prx/main/scripts/install.sh | sh
```

Supported platforms: macOS and Linux (darwin, linux) on arm64 and amd64.

### Upgrade

```bash
prx upgrade
```

This updates prx to the latest GitHub release.

## Quick start

1. Create `prx.toml` in your project root.

```toml
[project]
name = "my-project"

[services.web]
domain = "app.example.localhost"

[services.api]
domain = "api.example.localhost"
port = 3001
```

2. Start routing for the project:

```bash
prx up
```

3. Open from another terminal or browser:

```bash
https://app.example.localhost
https://api.example.localhost
```

4. Check status and stop when needed:

```bash
prx ls
prx daemon status
prx down
```

## Common command flow

```bash
prx up                  # apply project routes
prx ls                  # list reservations
prx run web -- pnpm dev  # run with injected PORT
prx run web -- ./gradlew bootRun  # framework-agnostic example
prx port web            # print assigned port
prx daemon status       # proxy daemon status
prx upgrade             # update prx to latest release
prx down               # stop current project routes
```

For full usage and all options, run `prx --help`.
