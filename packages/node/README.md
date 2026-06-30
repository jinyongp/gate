# @jinyongp/gate

Agent-friendly Node API and CLI launcher for
[gate](https://github.com/jinyongp/gate), a local HTTPS reverse proxy and port
registry for development machines.

Use this package when JavaScript automation needs typed gate data, stable port
lookup, or the package-provided `gate` binary. The proxy, registry, DNS, and
trust behavior still live in the gate binary; the Node API is a wrapper around
the same JSON command contracts used by scripts.

## Install

```bash
npm install --save-dev @jinyongp/gate
npx gate --version
```

Or with pnpm:

```bash
pnpm add -D @jinyongp/gate
pnpm exec gate --version
```

Install only `@jinyongp/gate`. Platform binary packages are optional
dependencies and are selected automatically on supported hosts:

- macOS arm64/x64
- Linux arm64/x64

## CLI

The package exposes a `gate` bin. Run these inside a project with a `gate.toml`:

```bash
pnpm exec gate up -d
pnpm exec gate run --up web -- pnpm dev
```

Prefer `gate run` when launching dev servers. It injects `PORT`, peer
`GATE_<SERVICE>_*` values, and service-specific env values declared in
`gate.toml`.

## Node API

```ts
import { createGateClient } from '@jinyongp/gate'

const gate = createGateClient({ cwd: process.cwd() })
const web = await gate.service('web', { up: true })

console.log(web.port)
console.log(web.url)
console.log(web.loopbackUrl)
```

`service(name)` defaults to:

```ts
{ up: true, dns: 'localhost', daemon: false }
```

That means it can reserve and activate routes before reading service metadata.
It does not start the daemon unless `daemon: true` is passed. Use
`service(name, { up: false })`, `ls()`, or `port()` for read-only inspection.

Use inline project config when automation needs project-scoped behavior without
writing `gate.toml` into the repository:

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

Inline config is materialized as a generated TOML file in the user cache and
passed to the gate binary through `--config`. `scope.project` may be used with
inline config, but it must match `config.name`. `envFiles` are intentionally not
part of the Node API; load environment variables before calling gate if inline
values use `${NAME}` or `${NAME:-fallback}` references.
Custom domains still require `dns: 'hosts'` or `dns: 'preconfigured'`.

## Binary Resolution

Use `resolveGateBinary()` when another process needs the concrete binary path:

```ts
import { createGateClient, resolveGateBinary } from '@jinyongp/gate'

const bin = resolveGateBinary()
const gate = createGateClient({ bin })
```

You can also pass `bin` directly or set `GATE_BIN`.

## Errors

```ts
import { createGateClient, isGateError } from '@jinyongp/gate'

const gate = createGateClient()

try {
  await gate.service('web')
} catch (error) {
  if (isGateError(error, 'GATE_DNS_REQUIRED')) {
    // Use a .localhost base, or intentionally pass dns: 'hosts'/'preconfigured'.
  }
  throw error
}
```

Common error codes:

| code                        | action                                                               |
| --------------------------- | -------------------------------------------------------------------- |
| `GATE_DNS_REQUIRED`         | Use `.localhost`, or pass `dns: 'hosts'` / `dns: 'preconfigured'`.   |
| `GATE_INVALID_OPTIONS`      | Fix incompatible scope/config options before retrying.               |
| `GATE_BINARY_NOT_FOUND`     | Reinstall `@jinyongp/gate`, or pass an explicit `bin` / `GATE_BIN`.  |
| `GATE_UNSUPPORTED_PLATFORM` | Use a supported Darwin/Linux arm64/x64 host or provide `bin`.        |
| `GATE_PERMISSION_REQUIRED`  | Retry only after explicit approval for privileged DNS/trust changes. |
| `GATE_SERVICE_NOT_FOUND`    | Check scope, config path, service name, and reservations.            |
| `GATE_COMMAND_FAILED`       | Inspect `exitCode`, `gateCode`, stdout, and stderr.                  |
| `GATE_JSON_PARSE_FAILED`    | Treat as a gate/version mismatch or broken binary output.            |

## Project Config

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

See the full usage guide:

- https://github.com/jinyongp/gate#readme
- https://github.com/jinyongp/gate/blob/main/docs/usage.md
